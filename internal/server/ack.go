package server

import (
	"context"
	"errors"
	"sync"
	"time"

	"mtsh/internal/chunks"
	"mtsh/internal/logx"
	"mtsh/internal/protofmt"
)

type chunkAckEvent struct {
	ack      protofmt.ChunkAck
	hops     uint32
	hopStart uint32
	hopLimit uint32
}

type chunkAckManager struct {
	mu      sync.Mutex
	waiters map[string]chan chunkAckEvent
}

func newChunkAckManager() *chunkAckManager {
	return &chunkAckManager{
		waiters: make(map[string]chan chunkAckEvent),
	}
}

func (m *chunkAckManager) register(id string) <-chan chunkAckEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.waiters[id]; ok {
		close(ch)
	}
	ch := make(chan chunkAckEvent, 8)
	m.waiters[id] = ch
	return ch
}

func (m *chunkAckManager) unregister(id string) {
	m.mu.Lock()
	ch := m.waiters[id]
	if ch != nil {
		delete(m.waiters, id)
	}
	m.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (m *chunkAckManager) deliver(evt chunkAckEvent) bool {
	m.mu.Lock()
	ch := m.waiters[evt.ack.ID]
	m.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- evt:
	default:
	}
	return true
}

var (
	errChunkAckTimeout       = chunks.ErrAckTimeout
	errChunkAckChannelClosed = errors.New("chunk ack channel closed")
)

func waitForClientAck(ctx context.Context, id string, ch <-chan chunkAckEvent, seq int, timeout time.Duration) error {
	if ch == nil {
		return nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return errChunkAckChannelClosed
			}
			if evt.ack.Seq == seq {
				logx.Debugf("server received chunk ack confirmation: id=%s seq=%d hops=%d hopStart=%d hopLimit=%d",
					id, seq, evt.hops, evt.hopStart, evt.hopLimit)
				return nil
			}
			logx.Debugf("server ignoring mismatched chunk ack: id=%s want=%d got=%d", id, seq, evt.ack.Seq)
		case <-timer.C:
			return errChunkAckTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
