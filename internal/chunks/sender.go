package chunks

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
)

// ErrAckTimeout indicates that an acknowledgment was not received before the timeout expired.
var ErrAckTimeout = errors.New("chunk ack timeout")

// Builder creates the on-air text message for a chunk.
type Builder func(id string, seq int, last bool, total int, data []byte) string

// Waiter is invoked after each chunk transmission when acknowledgments are enabled.
// Returning ErrAckTimeout asks the transport to retry (subject to AckRetries).
type Waiter func(ctx context.Context, seq int, last bool) error

// Sender encapsulates chunk transmission logic with optional ACK tracking.
type Sender struct {
	Device     *mt.Device
	Channel    uint32
	Builder    Builder
	WaitAck    Waiter
	RequireAck bool

	AckRetries int
	ChunkDelay time.Duration
}

// SendPayload compresses (if requested), chunks, and transmits the entire payload.
func (s Sender) SendPayload(ctx context.Context, id string, payload []byte, chunkBytes int, allowCompress bool) (bool, error) {
	if s.Device == nil || s.Builder == nil {
		return false, errors.New("chunk sender missing device or builder")
	}
	if chunkBytes <= 0 {
		chunkBytes = 180
	}

	data := payload
	compressed := false
	if allowCompress {
		var ok bool
		data, ok = MaybeCompress(payload)
		compressed = ok
	}

	chunks := chunkBytesSlice(data, chunkBytes)
	totalChunks := len(chunks)
	for i, chunk := range chunks {
		last := i == totalChunks-1
		if err := s.SendChunk(ctx, id, i, last, totalChunks, chunk); err != nil {
			return compressed, err
		}
	}
	return compressed, nil
}

// SendChunk transmits a single chunk (already prepared by the caller).
func (s Sender) SendChunk(ctx context.Context, id string, seq int, last bool, total int, data []byte) error {
	if s.Device == nil || s.Builder == nil {
		return errors.New("chunk sender missing device or builder")
	}
	attempt := 0
	for {
		msg := s.Builder(id, seq, last, total, data)
		if err := s.Device.SendText(ctx, s.Channel, mt.BroadcastDest, msg); err != nil {
			return err
		}

		var waitErr error
		if s.RequireAck && s.WaitAck != nil {
			waitErr = s.WaitAck(ctx, seq, last)
		} else if !last && s.ChunkDelay > 0 {
			waitErr = sleepWithContext(ctx, s.ChunkDelay)
		}

		if waitErr == nil {
			return nil
		}

		if s.RequireAck && errors.Is(waitErr, ErrAckTimeout) && attempt < s.AckRetries {
			attempt++
			continue
		}
		return waitErr
	}
}

func chunkBytesSlice(b []byte, max int) [][]byte {
	if max <= 0 {
		max = 180
	}
	if len(b) == 0 {
		return [][]byte{[]byte{}}
	}
	var res [][]byte
	for len(b) > 0 {
		n := max
		if len(b) < n {
			n = len(b)
		}
		res = append(res, b[:n])
		b = b[n:]
	}
	return res
}

// MaybeCompress returns a gzipped copy of data if it reduces size.
func MaybeCompress(data []byte) ([]byte, bool) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		logx.Debugf("chunks gzip compression failed, sending raw: err=%v", err)
		_ = zw.Close()
		return data, false
	}
	if err := zw.Close(); err != nil {
		logx.Debugf("chunks gzip close failed, sending raw: err=%v", err)
		return data, false
	}
	if buf.Len() >= len(data) {
		logx.Debugf("chunks skipping compression: in=%d out=%d (no gain)", len(data), buf.Len())
		return data, false
	}
	logx.Debugf("chunks compressed payload: in=%d out=%d", len(data), buf.Len())
	return buf.Bytes(), true
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
