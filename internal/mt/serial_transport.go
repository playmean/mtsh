package mt

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"mtsh/internal/logx"

	"github.com/exepirit/meshtastic-go/pkg/meshtastic"
	"github.com/exepirit/meshtastic-go/pkg/meshtastic/proto"
	serialdrv "go.bug.st/serial"
	protobuf "google.golang.org/protobuf/proto"
)

const (
	serialMagicFirst  = 0x94
	serialMagicSecond = 0xc3
	serialHeaderSize  = 4
	serialMaxFrame    = 1024
	serialReadTimeout = 250 * time.Millisecond
)

type serialTransport struct {
	port    serialdrv.Port
	name    string
	readMu  sync.Mutex
	writeMu sync.Mutex
}

func newSerialTransport(portName string) (*serialTransport, error) {
	mode := &serialdrv.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serialdrv.NoParity,
		StopBits: serialdrv.OneStopBit,
	}
	port, err := serialdrv.Open(portName, mode)
	if err != nil {
		return nil, fmt.Errorf("open serial port %s: %w", portName, err)
	}
	if err := port.SetReadTimeout(serialReadTimeout); err != nil {
		_ = port.Close()
		return nil, fmt.Errorf("set serial read timeout: %w", err)
	}
	logx.Debugf("serial: transport ready port=%s timeout=%s", portName, serialReadTimeout)
	return &serialTransport{port: port, name: portName}, nil
}

func (t *serialTransport) Close() error {
	if t == nil || t.port == nil {
		return nil
	}
	logx.Debugf("serial: closing port %s", t.name)
	return t.port.Close()
}

func (t *serialTransport) ReceiveFromRadio(ctx context.Context) (*proto.FromRadio, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()

	payload, err := t.readFrame(ctx)
	if err != nil {
		return nil, err
	}
	logx.Debugf("serial: rx bytes=%d port=%s", len(payload), t.name)
	packet := new(proto.FromRadio)
	if err := protobuf.Unmarshal(payload, packet); err != nil {
		logx.Debugf("serial: protobuf decode error port=%s err=%v payload-hex=%s", t.name, err, hex.EncodeToString(payload))
		return nil, meshtastic.ErrInvalidPacketFormat
	}
	return packet, nil
}

func (t *serialTransport) SendToRadio(ctx context.Context, packet *proto.ToRadio) error {
	payload, err := protobuf.Marshal(packet)
	if err != nil {
		return fmt.Errorf("marshal packet: %w", err)
	}
	if len(payload) > serialMaxFrame {
		return fmt.Errorf("packet too long: %d bytes", len(payload))
	}

	frame := make([]byte, serialHeaderSize+len(payload))
	frame[0] = serialMagicFirst
	frame[1] = serialMagicSecond
	binary.BigEndian.PutUint16(frame[2:4], uint16(len(payload)))
	copy(frame[serialHeaderSize:], payload)

	t.writeMu.Lock()
	defer t.writeMu.Unlock()

	logx.Debugf("serial: tx bytes=%d port=%s", len(payload), t.name)
	return t.writeAll(ctx, frame)
}

func (t *serialTransport) readFrame(ctx context.Context) ([]byte, error) {
	var lengthBuf [2]byte

	for {
		if err := t.syncHeader(ctx); err != nil {
			return nil, err
		}
		if err := t.readExact(ctx, lengthBuf[:]); err != nil {
			return nil, err
		}
		length := int(binary.BigEndian.Uint16(lengthBuf[:]))
		if length < 0 || length > serialMaxFrame {
			continue
		}
		payload := make([]byte, length)
		if err := t.readExact(ctx, payload); err != nil {
			return nil, err
		}
		// logx.Debugf("serial: raw rx bytes=%d hex=%s", len(payload), hex.EncodeToString(payload))
		return payload, nil
	}
}

func (t *serialTransport) syncHeader(ctx context.Context) error {
	buf := make([]byte, 1)
	seenFirst := false
	for {
		if err := t.readExact(ctx, buf); err != nil {
			return err
		}
		b := buf[0]
		if !seenFirst {
			seenFirst = b == serialMagicFirst
			continue
		}
		if b == serialMagicSecond {
			return nil
		}
		seenFirst = b == serialMagicFirst
	}
}

func (t *serialTransport) readExact(ctx context.Context, dst []byte) error {
	for read := 0; read < len(dst); {
		if err := contextErr(ctx); err != nil {
			return err
		}
		n, err := t.port.Read(dst[read:])
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		read += n
	}
	return nil
}

func (t *serialTransport) writeAll(ctx context.Context, data []byte) error {
	for len(data) > 0 {
		if err := contextErr(ctx); err != nil {
			return err
		}
		n, err := t.port.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		data = data[n:]
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
