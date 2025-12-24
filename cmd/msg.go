package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var flagMsgDest string

var msgCmd = &cobra.Command{
	Use:   "msg <message>",
	Short: "Send a text message over Meshtastic",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		message := strings.Join(args, " ")
		dest, err := parseNodeNumber(flagMsgDest)
		if err != nil {
			return err
		}

		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()

		fmt.Printf("msg: port=%s channel=%d dest=%d\n", port, flagChannel, dest)
		fmt.Printf("node: %s\n", dev.Info())

		return dev.SendText(ctx, flagChannel, dest, message)
	},
}

func init() {
	msgCmd.Flags().StringVar(&flagMsgDest, "to", "0xffffffff", "Destination node number (decimal or hex, default broadcast)")
	rootCmd.AddCommand(msgCmd)
}

func parseNodeNumber(raw string) (uint32, error) {
	n, err := strconv.ParseUint(raw, 0, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid destination %q (want decimal or hex)", raw)
	}
	return uint32(n), nil
}
