package cmd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/exepirit/meshtastic-go/pkg/meshtastic/proto"
	"github.com/spf13/cobra"
)

var nodesCmd = &cobra.Command{
	Use:   "nodes",
	Short: "List saved nodes from the device",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()

		fmt.Printf("nodes: port=%s channel=%d\n", port, flagChannel)
		fmt.Printf("node: %s\n", dev.Info())

		nodes := dev.SavedNodes()
		if len(nodes) == 0 {
			fmt.Println("nodes: none")
			return nil
		}

		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].GetNum() < nodes[j].GetNum()
		})

		for _, node := range nodes {
			fmt.Printf("node %s\n", formatNodeInfo(node))
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(nodesCmd)
}

func formatNodeInfo(node *proto.NodeInfo) string {
	if node == nil {
		return "num=0"
	}
	parts := []string{
		fmt.Sprintf("num=%d", node.GetNum()),
	}

	if user := node.GetUser(); user != nil {
		display := nodeDisplayName(user)
		if display != "" {
			parts = append(parts, fmt.Sprintf("name=%s", display))
		}
		if id := user.GetId(); id != "" && id != display {
			parts = append(parts, fmt.Sprintf("id=%s", id))
		}
		if short := user.GetShortName(); short != "" && short != display {
			parts = append(parts, fmt.Sprintf("short=%s", short))
		}
	}

	if node.HopsAway != nil {
		parts = append(parts, fmt.Sprintf("hops=%d", node.GetHopsAway()))
	}
	if ch := node.GetChannel(); ch != 0 {
		parts = append(parts, fmt.Sprintf("channel=%d", ch))
	}
	if snr := node.GetSnr(); snr != 0 {
		parts = append(parts, fmt.Sprintf("snr=%.1f", snr))
	}
	if last := node.GetLastHeard(); last != 0 {
		parts = append(parts, fmt.Sprintf("last_heard=%s", formatLastHeard(last)))
	}
	if node.GetViaMqtt() {
		parts = append(parts, "via-mqtt=true")
	}
	if node.GetIsFavorite() {
		parts = append(parts, "favorite=true")
	}
	if node.GetIsIgnored() {
		parts = append(parts, "ignored=true")
	}

	return strings.Join(parts, " ")
}

func nodeDisplayName(user *proto.User) string {
	if user == nil {
		return ""
	}
	if name := user.GetLongName(); name != "" {
		return name
	}
	if name := user.GetShortName(); name != "" {
		return name
	}
	return user.GetId()
}

func formatLastHeard(lastHeard uint32) string {
	heardAt := time.Unix(int64(lastHeard), 0)
	dur := time.Since(heardAt)
	if dur < 0 {
		dur = 0
	}
	return dur.Truncate(time.Second).String()
}
