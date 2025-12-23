package server

import (
	"context"
	"sync"
)

type requestCanceler struct {
	mu      sync.Mutex
	cancels map[string]*cancelEntry
}

type cancelEntry struct {
	cancel context.CancelFunc
}

func newRequestCanceler() *requestCanceler {
	return &requestCanceler{
		cancels: make(map[string]*cancelEntry),
	}
}

func (rc *requestCanceler) register(id string, cancel context.CancelFunc) func() {
	entry := &cancelEntry{cancel: cancel}
	rc.mu.Lock()
	rc.cancels[id] = entry
	rc.mu.Unlock()
	return func() {
		rc.mu.Lock()
		if c, ok := rc.cancels[id]; ok && c == entry {
			delete(rc.cancels, id)
		}
		rc.mu.Unlock()
	}
}

func (rc *requestCanceler) cancelAll() {
	rc.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(rc.cancels))
	for id, entry := range rc.cancels {
		cancels = append(cancels, entry.cancel)
		delete(rc.cancels, id)
	}
	rc.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (rc *requestCanceler) cancel(id string) bool {
	rc.mu.Lock()
	entry, ok := rc.cancels[id]
	if ok {
		delete(rc.cancels, id)
	}
	rc.mu.Unlock()
	if ok {
		entry.cancel()
	}
	return ok
}
