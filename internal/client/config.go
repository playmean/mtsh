package client

import "time"

// Config holds runtime parameters for the interactive client.
type Config struct {
	Channel     uint32
	WaitTimeout time.Duration
	ChunkAck    bool
}
