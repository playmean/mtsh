package mt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mtsh/internal/logx"

	meshtastic "github.com/exepirit/meshtastic-go/pkg/meshtastic"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/connect"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/proto"
	"google.golang.org/protobuf/encoding/protojson"
	pbproto "google.golang.org/protobuf/proto"
	"tinygo.org/x/bluetooth"
)

const (
	BroadcastDest     uint32 = 0xFFFFFFFF
	keepAliveInterval        = 30 * time.Second
	keepAliveDelay           = 3 * time.Minute
	defaultHopLimit          = 6
	bleConnectTimeout        = 20 * time.Second
)

type Device struct {
	D              *meshtastic.Device
	T              meshtastic.HardwareTransport
	info           NodeDetails
	transportName  string
	ackMu          sync.Mutex
	ackWaiters     map[uint32]chan *proto.QueueStatus
	ackTimeout     time.Duration
	ctx            context.Context
	textCh         chan RxText
	errCh          chan error
	cancel         context.CancelFunc
	pendingMu      sync.Mutex
	pending        []RxText
	flushing       bool
	flushWg        sync.WaitGroup
	infoMu         sync.Mutex
	configLogged   bool
	loggedNodes    map[uint32]bool
	savedNodes     []*proto.NodeInfo
	defaultChannel uint32
	hopLimit       uint32
	lastSendMu     sync.Mutex
	lastSendTime   time.Time
}

type NodeDetails struct {
	Number    uint32
	UserID    string
	LongName  string
	ShortName string
	Firmware  string
	Hardware  string
}

func (n NodeDetails) String() string {
	var parts []string

	parts = append(parts, fmt.Sprintf("num=%d", n.Number))

	displayName := n.LongName
	if displayName == "" {
		displayName = n.ShortName
	}
	if displayName == "" {
		displayName = n.UserID
	}
	if displayName != "" {
		parts = append(parts, fmt.Sprintf("name=%s", displayName))
	}
	if n.UserID != "" && n.UserID != displayName {
		parts = append(parts, fmt.Sprintf("id=%s", n.UserID))
	}
	if n.ShortName != "" && n.ShortName != displayName {
		parts = append(parts, fmt.Sprintf("short=%s", n.ShortName))
	}
	if n.Firmware != "" {
		parts = append(parts, fmt.Sprintf("fw=%s", n.Firmware))
	}
	if n.Hardware != "" {
		parts = append(parts, fmt.Sprintf("hw=%s", n.Hardware))
	}

	return strings.Join(parts, " ")
}

func Open(ctx context.Context, port string, ackTimeout time.Duration) (*Device, error) {
	t, transportName, err := openTransport(port)
	if err != nil {
		logx.Debugf("mt: transport error on %s: %v", transportName, err)
		return nil, err
	}
	dev := &meshtastic.Device{Transport: t}
	state, err := dev.Config().GetState(ctx)
	if err != nil {
		_ = connect.CloseTransport(t)
		logx.Debugf("mt: device configuration error on %s: %v", transportName, err)
		return nil, err
	}
	if state.MyInfo != nil {
		dev.NodeID = state.MyInfo.GetMyNodeNum()
	}
	info := buildNodeDetails(state)
	logx.Debugf("mt: device ready on %s (node=%d)", transportName, info.Number)
	if ackTimeout <= 0 {
		ackTimeout = 20 * time.Second
	}
	bgCtx, cancel := context.WithCancel(ctx)
	dv := &Device{
		D:             dev,
		T:             t,
		info:          info,
		transportName: transportName,
		savedNodes:    append([]*proto.NodeInfo(nil), state.Nodes...),
		ackWaiters:    make(map[uint32]chan *proto.QueueStatus),
		ackTimeout:    ackTimeout,
		ctx:           bgCtx,
		textCh:        make(chan RxText, 128),
		errCh:         make(chan error, 1),
		cancel:        cancel,
		lastSendTime:  time.Now(),
	}
	go dv.readLoop(bgCtx)
	go dv.keepAliveLoop(bgCtx)
	return dv, nil
}

func (d *Device) Close() error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.T != nil {
		logx.Debugf("mt: closing transport %s", d.transportName)
		return connect.CloseTransport(d.T)
	}
	return nil
}

func openTransport(target string) (meshtastic.HardwareTransport, string, error) {
	if isBLETarget(target) {
		normalized := normalizeBLETarget(target)
		if err := enableBLEAdapter(); err != nil {
			return nil, normalized, err
		}
		logx.Debugf("mt: opening BLE transport %s", normalized)
		t, err := openBLETransport(normalized, bleConnectTimeout)
		return t, normalized, err
	}
	logx.Debugf("mt: opening serial port %s", target)
	t, err := newSerialTransport(target)
	return t, target, err
}

func isBLETarget(target string) bool {
	return strings.HasPrefix(strings.ToLower(target), "ble:")
}

func normalizeBLETarget(target string) string {
	if strings.HasPrefix(strings.ToLower(target), "ble://") {
		return target
	}
	return "ble://" + strings.TrimPrefix(target, "ble:")
}

func enableBLEAdapter() error {
	if err := bluetooth.DefaultAdapter.Enable(); err != nil {
		return fmt.Errorf("enable BLE adapter: %w", err)
	}
	return nil
}

func openBLETransport(target string, timeout time.Duration) (meshtastic.HardwareTransport, error) {
	type result struct {
		transport meshtastic.HardwareTransport
		err       error
	}
	ch := make(chan result, 1)
	go func() {
		transport, err := connect.NewTransport(target)
		ch <- result{transport: transport, err: err}
	}()
	select {
	case res := <-ch:
		return res.transport, res.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("BLE connection timeout after %s", timeout)
	}
}

func (d *Device) Info() NodeDetails {
	return d.info
}

func (d *Device) SavedNodes() []*proto.NodeInfo {
	nodes := make([]*proto.NodeInfo, len(d.savedNodes))
	copy(nodes, d.savedNodes)
	return nodes
}

func (d *Device) SetDefaultChannel(ch uint32) {
	d.defaultChannel = ch
}

func (d *Device) SetHopLimit(limit uint32) {
	d.hopLimit = limit
}

type RxText struct {
	FromNode uint32
	ToNode   uint32 // 0 or broadcast / depends on packet
	Channel  uint32
	Text     string
	PacketID uint32
	HopStart uint32
	HopLimit uint32
	Hops     uint32
}

func (d *Device) RecvText(ctx context.Context) (RxText, error) {
	select {
	case rx, ok := <-d.textCh:
		if !ok {
			select {
			case err := <-d.errCh:
				if err != nil {
					return RxText{}, err
				}
			default:
			}
			return RxText{}, context.Canceled
		}
		return rx, nil
	case <-ctx.Done():
		return RxText{}, ctx.Err()
	}
}

// destNode identifies the packet destination; broadcast uses meshtastic.BroadcastNodenum.
func (d *Device) SendText(ctx context.Context, channel uint32, destNode uint32, text string) error {
	logx.Debugf("mt: TX channel=%d dest=%d bytes=%d text=%q", channel, destNode, len(text), text)
	data := &proto.Data{
		Portnum: proto.PortNum_TEXT_MESSAGE_APP,
		Payload: []byte(text),
	}
	hopLimit := d.hopLimit
	if hopLimit == 0 {
		hopLimit = defaultHopLimit
	}
	packet := &proto.MeshPacket{
		To:      destNode,
		Channel: channel,
		PayloadVariant: &proto.MeshPacket_Decoded{
			Decoded: data,
		},
		WantAck:  true,
		HopLimit: hopLimit,
	}
	if err := d.D.SendToMesh(ctx, packet); err != nil {
		logx.Debugf("mt: TX error: %v", err)
		return err
	}
	d.noteSend()

	logx.Debugf("mt: waiting for TX ACK")

	packetID := packet.GetId()
	waitCh := make(chan *proto.QueueStatus, 1)
	d.ackMu.Lock()
	d.ackWaiters[packetID] = waitCh
	d.ackMu.Unlock()
	defer func() {
		d.ackMu.Lock()
		delete(d.ackWaiters, packetID)
		d.ackMu.Unlock()
		close(waitCh)
	}()

	select {
	case qs := <-waitCh:
		if qs.GetRes() != 0 {
			name := proto.Routing_Error_name[qs.GetRes()]
			if name == "" {
				name = fmt.Sprintf("code_%d", qs.GetRes())
			}
			err := fmt.Errorf("mesh ack error %s for packet %d", name, packetID)
			logx.Debugf("mt: TX ack error: %v", err)
			return err
		}
		logx.Debugf("mt: TX ack received packet id=%d", packetID)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d.ackTimeout):
		return fmt.Errorf("mt: ack timeout for packet %d", packetID)
	}
}

func buildNodeDetails(state meshtastic.DeviceState) NodeDetails {
	var info NodeDetails
	if state.MyInfo != nil {
		info.Number = state.MyInfo.GetMyNodeNum()
	}
	if node, ok := state.CurrentNodeInfo(); ok {
		if user := node.GetUser(); user != nil {
			info.UserID = user.GetId()
			info.LongName = user.GetLongName()
			info.ShortName = user.GetShortName()
			if info.Hardware == "" {
				info.Hardware = hardwareModelString(user.GetHwModel())
			}
		}
	}
	if state.Device != nil {
		info.Firmware = state.Device.GetFirmwareVersion()
		if info.Hardware == "" {
			info.Hardware = hardwareModelString(state.Device.GetHwModel())
		}
	}
	return info
}

func hardwareModelString(hw proto.HardwareModel) string {
	if hw == proto.HardwareModel_UNSET {
		return ""
	}
	if name, ok := proto.HardwareModel_name[int32(hw)]; ok {
		return strings.ToLower(name)
	}
	return fmt.Sprintf("hw_%d", hw)
}

func (d *Device) dispatchQueueStatus(qs *proto.QueueStatus) {
	packetID := qs.GetMeshPacketId()
	if packetID == 0 {
		return
	}
	d.ackMu.Lock()
	ch := d.ackWaiters[packetID]
	d.ackMu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- qs:
	default:
	}
}

func (d *Device) readLoop(ctx context.Context) {
	defer func() {
		d.flushWg.Wait()
		close(d.textCh)
	}()

	for {
		frame, err := d.T.ReceiveFromRadio(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			logx.Debugf("mt: receive error: %v", err)
			select {
			case d.errCh <- err:
			default:
			}
			return
		}
		if qs := frame.GetQueueStatus(); qs != nil {
			d.dispatchQueueStatus(qs)
			continue
		}
		if d.handleMetaFrame(frame) {
			continue
		}
		p := frame.GetPacket()
		if p == nil {
			variant := "nil_variant"
			if frame.PayloadVariant != nil {
				variant = fmt.Sprintf("%T", frame.PayloadVariant)
			}
			logx.Debugf("mt: ignoring from-radio message variant=%s", variant)
			continue
		}
		dec := p.GetDecoded()
		if dec == nil {
			logx.Debugf("mt: ignoring packet without decoded payload id=%d from=%d", p.GetId(), p.GetFrom())
			continue
		}
		port := dec.GetPortnum()
		if port != proto.PortNum_TEXT_MESSAGE_APP {
			portName := proto.PortNum_name[int32(port)]
			if portName == "" {
				portName = fmt.Sprintf("port_%d", port)
			}
			logx.Debugf("mt: ignoring packet id=%d from=%d to=%d channel=%d port=%s", p.GetId(), p.GetFrom(), p.GetTo(), p.GetChannel(), strings.ToLower(portName))
			continue
		}
		text := strings.TrimRight(string(dec.GetPayload()), "\x00\r")
		hopStart := p.GetHopStart()
		hopLimit := p.GetHopLimit()
		rx := RxText{
			FromNode: p.GetFrom(),
			ToNode:   p.GetTo(),
			Channel:  p.GetChannel(),
			Text:     text,
			PacketID: p.GetId(),
			HopStart: hopStart,
			HopLimit: hopLimit,
			Hops:     computeHopCount(hopStart, hopLimit),
		}
		d.enqueueRx(rx)
	}
}

func (d *Device) enqueueRx(rx RxText) {
	select {
	case d.textCh <- rx:
		return
	default:
	}

	d.pendingMu.Lock()
	d.pending = append(d.pending, rx)
	if !d.flushing {
		d.flushing = true
		d.flushWg.Add(1)
		go d.flushPending()
	}
	d.pendingMu.Unlock()
}

func (d *Device) flushPending() {
	defer d.flushWg.Done()
	for {
		d.pendingMu.Lock()
		if len(d.pending) == 0 {
			d.flushing = false
			d.pendingMu.Unlock()
			return
		}
		rx := d.pending[0]
		d.pending = d.pending[1:]
		d.pendingMu.Unlock()

		select {
		case d.textCh <- rx:
		case <-d.ctx.Done():
			return
		}
	}
}

func (d *Device) handleMetaFrame(frame *proto.FromRadio) bool {
	if cfg := frame.GetConfig(); cfg != nil {
		d.logInitialConfig(cfg)
		return true
	}
	if node := frame.GetNodeInfo(); node != nil {
		d.logNodeAnnouncement(node)
		return true
	}
	return false
}

func (d *Device) logInitialConfig(cfg *proto.Config) {
	if cfg == nil {
		return
	}
	d.infoMu.Lock()
	if d.configLogged {
		d.infoMu.Unlock()
		return
	}
	d.configLogged = true
	d.infoMu.Unlock()

	logx.Debugf("mt: initial config %s", protoCompact(cfg))
}

func (d *Device) logNodeAnnouncement(node *proto.NodeInfo) {
	if node == nil {
		return
	}
	num := node.GetNum()
	d.infoMu.Lock()
	if d.loggedNodes == nil {
		d.loggedNodes = make(map[uint32]bool)
	}
	if d.loggedNodes[num] {
		d.infoMu.Unlock()
		return
	}
	d.loggedNodes[num] = true
	d.infoMu.Unlock()

	userName := userDisplayName(node.GetUser())
	userID := ""
	if u := node.GetUser(); u != nil {
		userID = u.GetId()
	}
	location := ""
	if pos := node.GetPosition(); pos != nil {
		lat := pos.GetLatitudeI()
		lon := pos.GetLongitudeI()
		if lat != 0 || lon != 0 {
			location = fmt.Sprintf(" lat=%.5f lon=%.5f", float64(lat)/1e7, float64(lon)/1e7)
		}
		if alt := pos.GetAltitude(); alt != 0 {
			location = fmt.Sprintf("%s alt=%dm", location, alt)
		}
	}
	logx.Debugf("mt: discovered node num=%d name=%s id=%s channel=%d hops=%d snr=%.1f via-mqtt=%v favorite=%v ignored=%v%s",
		num, userName, userID, node.GetChannel(), node.GetHopsAway(), node.GetSnr(), node.GetViaMqtt(), node.GetIsFavorite(), node.GetIsIgnored(), location)
}

func userDisplayName(user *proto.User) string {
	if user == nil {
		return ""
	}
	if name := user.GetLongName(); name != "" {
		return name
	}
	if name := user.GetShortName(); name != "" {
		return name
	}
	return user.GetId()
}

func computeHopCount(hopStart, hopLimit uint32) uint32 {
	if hopStart == 0 {
		return 0
	}
	if hopLimit > hopStart {
		return 0
	}
	return hopStart - hopLimit
}

var protoLogOptions = protojson.MarshalOptions{
	EmitUnpopulated: false,
	UseEnumNumbers:  false,
}

func protoCompact(m pbproto.Message) string {
	if m == nil {
		return ""
	}
	data, err := protoLogOptions.Marshal(m)
	if err != nil {
		if s, ok := any(m).(fmt.Stringer); ok {
			return s.String()
		}
		return fmt.Sprintf("%T", m)
	}
	return string(data)
}

func (d *Device) keepAliveLoop(ctx context.Context) {
	if keepAliveInterval <= 0 {
		return
	}
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if d.timeSinceLastSend() < keepAliveDelay {
				continue
			}
			if err := d.sendPrivateKeepAlive(ctx); err != nil {
				logx.Debugf("mt: keepalive send failed: %v", err)
			} else {
				logx.Debugf("mt: keepalive packet sent: channel=%d", d.defaultChannel)
			}
		}
	}
}

func (d *Device) sendPrivateKeepAlive(ctx context.Context) error {
	params := meshtastic.SendDataParams{
		PortNum:      proto.PortNum_PRIVATE_APP,
		Payload:      []byte{},
		DestNodeNum:  BroadcastDest,
		ChannelIndex: d.defaultChannel,
		WantAck:      false,
	}
	if err := d.D.SendData(ctx, params); err != nil {
		return err
	}
	d.noteSend()
	return nil
}

func (d *Device) noteSend() {
	d.lastSendMu.Lock()
	d.lastSendTime = time.Now()
	d.lastSendMu.Unlock()
}

func (d *Device) timeSinceLastSend() time.Duration {
	d.lastSendMu.Lock()
	last := d.lastSendTime
	d.lastSendMu.Unlock()
	if last.IsZero() {
		return keepAliveDelay
	}
	return time.Since(last)
}
