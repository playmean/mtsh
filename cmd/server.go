package cmd

import (
	"fmt"
	"strings"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/server"

	"github.com/spf13/cobra"
)

var (
	flagShell      string
	flagCmdTimeout time.Duration
	flagDmOnly     bool
	flagDmWhitelist []string
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run commands received over Meshtastic and reply with output",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		allowChannel := false
		if flag := cmd.Flag("channel"); flag != nil && flag.Changed {
			allowChannel = true
		}
		if flagDmOnly {
			allowChannel = false
		}
		dmWhitelist, err := parseNodeWhitelist(flagDmWhitelist)
		if err != nil {
			return err
		}

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
			AllowChannel:    allowChannel,
			DmWhitelist:     dmWhitelist,
			DedupTTL:        flagDedupTTL,
			DedupCap:        flagDedupCap,
			Shell:           flagShell,
			CmdTimeout:      flagCmdTimeout,
			ChunkBytes:      flagChunkBytes,
			ChunkDelay:      flagChunkDelay,
			ChunkAckTimeout: flagChunkAckTimeout,
			ChunkAckRetries: flagChunkAckRetries,
			ChunkAck:        !flagNoChunkAck,
		}

		return server.Run(ctx, dev, cfg)
	},
}

func init() {
	serverCmd.Flags().StringVar(&flagShell, "shell", "sh", "Shell to execute commands")
	serverCmd.Flags().DurationVar(&flagCmdTimeout, "cmd-timeout", 20*time.Second, "Per-command execution timeout")
	serverCmd.Flags().BoolVar(&flagDmOnly, "dm-only", false, "Accept only direct messages addressed to this node")
	serverCmd.Flags().StringSliceVar(&flagDmWhitelist, "dm-whitelist", nil, "Comma-separated list of node IDs allowed to reach the server")
}

func parseNodeWhitelist(raw []string) (map[uint32]struct{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make(map[uint32]struct{})
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		}) {
			if part == "" {
				continue
			}
			id, err := parseNodeNumber(part)
			if err != nil {
				return nil, err
			}
			out[id] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
