package mt

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"mtsh/internal/logx"

	meshtastic "github.com/exepirit/meshtastic-go/pkg/meshtastic"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/proto"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/serial"
)

type Device struct {
	D          *meshtastic.Device
	T          *serial.StreamTransport
	info       NodeDetails
	ackMu      sync.Mutex
	ackWaiters map[uint32]chan *proto.QueueStatus
	ackTimeout time.Duration
	ctx        context.Context
	textCh     chan RxText
	errCh      chan error
	cancel     context.CancelFunc
	pendingMu  sync.Mutex
	pending    []RxText
	flushing   bool
	flushWg    sync.WaitGroup
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
	logx.Debugf("mt: opening serial port %s", port)
	t, err := serial.NewTransport(port)
	if err != nil {
		logx.Debugf("mt: serial transport error on %s: %v", port, err)
		return nil, err
	}
	dev := &meshtastic.Device{Transport: t}
	state, err := dev.Config().GetState(ctx)
	if err != nil {
		_ = t.Close()
		logx.Debugf("mt: device configuration error on %s: %v", port, err)
		return nil, err
	}
	if state.MyInfo != nil {
		dev.NodeID = state.MyInfo.GetMyNodeNum()
	}
	info := buildNodeDetails(state)
	logx.Debugf("mt: device ready on port %s (node=%d)", port, info.Number)
	if ackTimeout <= 0 {
		ackTimeout = 20 * time.Second
	}
	bgCtx, cancel := context.WithCancel(ctx)
	dv := &Device{
		D:          dev,
		T:          t,
		info:       info,
		ackWaiters: make(map[uint32]chan *proto.QueueStatus),
		ackTimeout: ackTimeout,
		ctx:        bgCtx,
		textCh:     make(chan RxText, 128),
		errCh:      make(chan error, 1),
		cancel:     cancel,
	}
	go dv.readLoop(bgCtx)
	return dv, nil
}

func (d *Device) Close() error {
	if d.cancel != nil {
		d.cancel()
	}
	if d.T != nil {
		logx.Debugf("mt: closing serial transport")
		return d.T.Close()
	}
	return nil
}

func (d *Device) Info() NodeDetails {
	return d.info
}

type RxText struct {
	FromNode uint32
	ToNode   uint32 // 0 or broadcast / depends on packet
	Channel  uint32
	Text     string
	PacketID uint32
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

// destNode: meshtastic.BroadcastNodenum for channel broadcast; otherwise DM.
func (d *Device) SendText(ctx context.Context, channel uint32, destNode uint32, text string) error {
	logx.Debugf("mt: TX channel=%d dest=%d bytes=%d text=%q", channel, destNode, len(text), text)
	data := &proto.Data{
		Portnum: proto.PortNum_TEXT_MESSAGE_APP,
		Payload: []byte(text),
	}
	packet := &proto.MeshPacket{
		To:      destNode,
		Channel: channel,
		PayloadVariant: &proto.MeshPacket_Decoded{
			Decoded: data,
		},
		WantAck:  true,
		HopLimit: 6,
	}
	if err := d.D.SendToMesh(ctx, packet); err != nil {
		logx.Debugf("mt: TX error: %v", err)
		return err
	}

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
		p := frame.GetPacket()
		if p == nil {
			continue
		}
		dec := p.GetDecoded()
		if dec == nil {
			continue
		}
		if dec.GetPortnum() != proto.PortNum_TEXT_MESSAGE_APP {
			continue
		}
		text := strings.TrimRight(string(dec.GetPayload()), "\x00\r\n")
		rx := RxText{
			FromNode: p.GetFrom(),
			ToNode:   p.GetTo(),
			Channel:  p.GetChannel(),
			Text:     text,
			PacketID: p.GetId(),
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
