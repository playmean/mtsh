package client

import (
	"fmt"
	"os"
)

func renderChunkProgress(processed, total int, hops uint32) {
	if total <= 0 {
		return
	}

	if processed > total {
		processed = total
	}

	fmt.Fprintf(os.Stderr, "[mtsh] chunks %d/%d hops=%d\n", processed, total, hops)
}
