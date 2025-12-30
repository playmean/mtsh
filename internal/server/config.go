package server

import "time"

// Config holds runtime parameters for the Meshtastic server loop.
type Config struct {
	Channel         uint32
	AllowChannel    bool
	DmWhitelist     map[uint32]struct{}
	DedupTTL        time.Duration
	DedupCap        int
	Shell           string
	CmdTimeout      time.Duration
	ChunkBytes      int
	ChunkDelay      time.Duration
	ChunkAckTimeout time.Duration
	ChunkAckRetries int
	ChunkAck        bool
}
