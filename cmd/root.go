package cmd

import (
	"fmt"
	"os"
	"time"

	"mtsh/internal/logx"

	"github.com/spf13/cobra"
)

var (
	flagPort       string
	flagChannel    uint32
	flagChunkBytes int
	flagAckTimeout time.Duration

	flagDedupTTL time.Duration
	flagDedupCap int

	flagVerbose bool
)

var rootCmd = &cobra.Command{
	Use:   "mtsh",
	Short: "Remote shell over Meshtastic text messages",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(func() {
		logx.EnableDebug(flagVerbose)
		if flagVerbose {
			logx.Debugf("debug logging enabled")
		}
	})

	rootCmd.PersistentFlags().StringVar(&flagPort, "port", "", "Serial port (e.g. /dev/ttyUSB0, COM5). Auto-detected if unset")
	rootCmd.PersistentFlags().Uint32Var(&flagChannel, "channel", 0, "Meshtastic channel index (0..)")
	rootCmd.PersistentFlags().IntVar(&flagChunkBytes, "chunk-bytes", 128, "Max payload bytes per outgoing text message chunk")
	rootCmd.PersistentFlags().DurationVar(&flagAckTimeout, "ack-timeout", 10*time.Second, "Timeout waiting for radio ACK from Meshtastic device")

	rootCmd.PersistentFlags().DurationVar(&flagDedupTTL, "dedup-ttl", 10*time.Minute, "Deduplication TTL")
	rootCmd.PersistentFlags().IntVar(&flagDedupCap, "dedup-cap", 2048, "Deduplication LRU capacity")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose debug logging")

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(clientCmd)
}
