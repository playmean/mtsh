package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var msgCmd = &cobra.Command{
	Use:   "msg <message>",
	Short: "Send a text message over Meshtastic",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		message := strings.Join(args, " ")
		dest, err := parseNodeNumber(flagTo)
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
	rootCmd.AddCommand(msgCmd)
}
