package cmd

import (
	"fmt"
	"os"
	"time"

	"mtsh/internal/logx"

	"github.com/spf13/cobra"
)

var (
	flagPort            string
	flagChannel         uint32
	flagChunkBytes      int
	flagAckTimeout      time.Duration
	flagChunkDelay      time.Duration
	flagChunkAckTimeout time.Duration
	flagChunkAckRetries int
	flagNoChunkAck      bool
	flagHopLimit        uint32

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

	rootCmd.PersistentFlags().StringVar(&flagPort, "port", "", "Device port (e.g. /dev/ttyUSB0, COM5, ble://00:11:22:33:44:55). Auto-detected if unset")
	rootCmd.PersistentFlags().Uint32Var(&flagChannel, "channel", 0, "Meshtastic channel index (0..)")
	rootCmd.PersistentFlags().IntVar(&flagChunkBytes, "chunk-bytes", 128, "Max payload bytes per outgoing text message chunk")
	rootCmd.PersistentFlags().DurationVar(&flagAckTimeout, "ack-timeout", 10*time.Second, "Timeout waiting for radio ACK from Meshtastic device")
	rootCmd.PersistentFlags().DurationVar(&flagChunkDelay, "chunk-delay", 5*time.Second, "Delay between response chunks when acknowledgments are disabled")
	rootCmd.PersistentFlags().DurationVar(&flagChunkAckTimeout, "chunk-ack-timeout", 30*time.Second, "Timeout waiting for chunk acknowledgments")
	rootCmd.PersistentFlags().IntVar(&flagChunkAckRetries, "chunk-ack-retries", 5, "Retries for chunk resend if ACK not received")
	rootCmd.PersistentFlags().BoolVar(&flagNoChunkAck, "no-chunk-ack", false, "Disable chunk acknowledgments")
	rootCmd.PersistentFlags().Uint32Var(&flagHopLimit, "hop-limit", 6, "Hop limit for outgoing Meshtastic messages")

	rootCmd.PersistentFlags().DurationVar(&flagDedupTTL, "dedup-ttl", 10*time.Minute, "Deduplication TTL")
	rootCmd.PersistentFlags().IntVar(&flagDedupCap, "dedup-cap", 2048, "Deduplication LRU capacity")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose debug logging")

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(clientCmd)
}
