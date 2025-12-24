package cmd

import (
	"fmt"
	"strings"

	"mtsh/internal/mt"

	"github.com/spf13/cobra"
)

var msgCmd = &cobra.Command{
	Use:   "msg <message>",
	Short: "Send a text message over Meshtastic",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		message := strings.Join(args, " ")

		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()

		fmt.Printf("send: port=%s channel=%d\n", port, flagChannel)
		fmt.Printf("node: %s\n", dev.Info())

		return dev.SendText(ctx, flagChannel, mt.BroadcastDest, message)
	},
}

func init() {
	rootCmd.AddCommand(msgCmd)
}
