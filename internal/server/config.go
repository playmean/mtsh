package server

import "time"

// Config holds runtime parameters for the Meshtastic server loop.
type Config struct {
	Channel         uint32
	DedupTTL        time.Duration
	DedupCap        int
	Shell           string
	CmdTimeout      time.Duration
	ChunkBytes      int
	ChunkDelay      time.Duration
	ChunkAckTimeout time.Duration
	ChunkAckRetries int
}
