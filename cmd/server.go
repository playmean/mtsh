package cmd

import (
	"fmt"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/server"

	"github.com/spf13/cobra"
)

var (
	flagShell           string
	flagCmdTimeout      time.Duration
	flagChunkDelay      time.Duration
	flagChunkAckTimeout time.Duration
	flagChunkAckRetries int
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run commands received over Meshtastic and reply with output",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		requested := flagPort
		logx.Debugf("server command starting: requestedPort=%s channel=%d shell=%s chunkBytes=%d", requested, flagChannel, flagShell, flagChunkBytes)
		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()
		logx.Debugf("server command connected to device: port=%s", port)

		fmt.Printf("server: port=%s channel=%d\n", port, flagChannel)
		nodeInfo := dev.Info()
		fmt.Printf("node: %s\n", nodeInfo)
		logx.Debugf("server connected node: %s", nodeInfo)

		cfg := server.Config{
			Channel:         flagChannel,
			DedupTTL:        flagDedupTTL,
			DedupCap:        flagDedupCap,
			Shell:           flagShell,
			CmdTimeout:      flagCmdTimeout,
			ChunkBytes:      flagChunkBytes,
			ChunkDelay:      flagChunkDelay,
			ChunkAckTimeout: flagChunkAckTimeout,
			ChunkAckRetries: flagChunkAckRetries,
		}

		return server.Run(ctx, dev, cfg)
	},
}

func init() {
	serverCmd.Flags().StringVar(&flagShell, "shell", "sh", "Shell to execute commands")
	serverCmd.Flags().DurationVar(&flagCmdTimeout, "cmd-timeout", 20*time.Second, "Per-command execution timeout")
	serverCmd.Flags().DurationVar(&flagChunkDelay, "chunk-delay", 5*time.Second, "Delay between response chunks when client does not request ACK")
	serverCmd.Flags().DurationVar(&flagChunkAckTimeout, "chunk-ack-timeout", time.Minute, "Timeout waiting for client chunk ACK")
	serverCmd.Flags().IntVar(&flagChunkAckRetries, "chunk-ack-retries", 3, "Retries for chunk resend if ACK not received")
}
