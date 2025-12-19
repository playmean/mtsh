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
	flagShell      string
	flagAckTimeout time.Duration

	flagDM       uint32
	flagDMOnly   bool
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

	rootCmd.PersistentFlags().StringVar(&flagPort, "port", "", "Serial port (e.g. /dev/ttyUSB0, COM5)")
	rootCmd.PersistentFlags().Uint32Var(&flagChannel, "channel", 0, "Meshtastic channel index (0..)")
	rootCmd.PersistentFlags().IntVar(&flagChunkBytes, "chunk-bytes", 128, "Max payload bytes per outgoing text message chunk")
	rootCmd.PersistentFlags().StringVar(&flagShell, "shell", "sh", "Shell to execute commands (server)")
	rootCmd.PersistentFlags().DurationVar(&flagAckTimeout, "ack-timeout", 2*time.Minute, "Timeout waiting for radio ACK from Meshtastic device")

	rootCmd.PersistentFlags().Uint32Var(&flagDM, "dm", 0, "Send as DM to nodenum (client). If 0 => broadcast on channel")
	rootCmd.PersistentFlags().BoolVar(&flagDMOnly, "dm-only", false, "Server: accept only DM packets (ignore broadcast)")
	rootCmd.PersistentFlags().DurationVar(&flagDedupTTL, "dedup-ttl", 10*time.Minute, "Deduplication TTL")
	rootCmd.PersistentFlags().IntVar(&flagDedupCap, "dedup-cap", 2048, "Deduplication LRU capacity")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "Enable verbose debug logging")

	_ = rootCmd.MarkPersistentFlagRequired("port")

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(clientCmd)
}
