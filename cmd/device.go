package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"mtsh/internal/logx"
	"mtsh/internal/mt"

	serial "go.bug.st/serial"
)

func openDevice(ctx context.Context) (*mt.Device, string, error) {
	if flagPort != "" {
		dev, err := mt.Open(ctx, flagPort, flagAckTimeout)
		if err != nil {
			return nil, flagPort, err
		}
		dev.SetDefaultChannel(flagChannel)
		return dev, flagPort, nil
	}

	ports, err := serial.GetPortsList()
	if err != nil {
		return nil, "", fmt.Errorf("list serial ports: %w", err)
	}
	if len(ports) == 0 {
		return nil, "", fmt.Errorf("no serial ports detected; connect your Meshtastic device or pass --port")
	}

	sort.Sort(sort.Reverse(sort.StringSlice(ports)))
	var errs []error
	for _, port := range ports {
		logx.Debugf("auto-detect trying serial port %s", port)
		dev, err := mt.Open(ctx, port, flagAckTimeout)
		if err == nil {
			logx.Debugf("auto-detect selected serial port %s", port)
			flagPort = port
			dev.SetDefaultChannel(flagChannel)
			return dev, port, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", port, err))
		logx.Debugf("auto-detect rejected serial port %s: %v", port, err)
	}

	joined := errors.Join(errs...)
	return nil, "", fmt.Errorf("auto-detect serial port failed (tried %s): %w", strings.Join(ports, ", "), joined)
}
