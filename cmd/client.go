package cmd

import (
	"fmt"
	"time"

	"mtsh/internal/client"
	"mtsh/internal/logx"

	"github.com/spf13/cobra"
)

var (
	flagWaitTimeout  time.Duration
	flagStreamChunks bool
	flagNoProgress   bool
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Terminal proxy: send commands over Meshtastic and print replies",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		requested := flagPort
		logx.Debugf("client command starting: requestedPort=%s channel=%d", requested, flagChannel)
		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()
		logx.Debugf("client command connected to device: port=%s", port)

		fmt.Printf("client: port=%s channel=%d wait-timeout=%s\n", port, flagChannel, flagWaitTimeout)
		nodeInfo := dev.Info()
		fmt.Printf("node: %s\n", nodeInfo)
		logx.Debugf("client connected node: %s", nodeInfo)
		logx.Debugf("client using wait-timeout=%s", flagWaitTimeout)

		useChunkAck := !flagNoChunkAck

		cfg := client.Config{
			Channel:      flagChannel,
			WaitTimeout:  flagWaitTimeout,
			ChunkAck:     useChunkAck,
			StreamChunks: flagStreamChunks,
			ShowProgress: !flagNoProgress,
		}

		return client.Run(ctx, dev, cfg)
	},
}

func init() {
	clientCmd.Flags().DurationVar(&flagWaitTimeout, "wait-timeout", 2*time.Minute, "Timeout waiting for remaining response chunks")
	clientCmd.Flags().BoolVar(&flagStreamChunks, "stream-chunks", false, "Print chunks as they arrive (disables progress and buffering)")
	clientCmd.Flags().BoolVar(&flagNoProgress, "no-progress", false, "Disable chunk download progress for buffered responses")
}
