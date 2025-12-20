package client

import (
	"fmt"
	"os"
)

func renderChunkProgress(processed, total int, done bool) {
	if total <= 0 {
		return
	}
	if processed > total {
		processed = total
	}
	msg := fmt.Sprintf("[mtsh] chunks %d/%d", processed, total)
	if done {
		fmt.Fprintf(os.Stderr, "\r%s\n", msg)
	} else {
		fmt.Fprintf(os.Stderr, "\r%s", msg)
	}
}
