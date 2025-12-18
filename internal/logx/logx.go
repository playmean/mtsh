package logx

import (
	"log"
	"os"
	"sync/atomic"
)

var (
	debugEnabled atomic.Bool
	logger       = log.New(os.Stderr, "[debug] ", log.LstdFlags)
)

// EnableDebug toggles runtime debug logging.
func EnableDebug(enable bool) {
	debugEnabled.Store(enable)
}

// Debugf prints a formatted message when debug logging is enabled.
func Debugf(format string, args ...interface{}) {
	if !debugEnabled.Load() {
		return
	}
	logger.Printf(format, args...)
}
