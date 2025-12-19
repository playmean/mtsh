package client

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
)

type inputEvent struct {
	line string
	err  error
}

func readInput(ch chan<- inputEvent) {
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadString('\n')
		if err != nil {
			ch <- inputEvent{err: err}
			close(ch)
			return
		}
		ch <- inputEvent{line: strings.TrimSpace(line)}
	}
}

func waitForLine(inputCh <-chan inputEvent, errCh <-chan error, sigCh <-chan os.Signal) (string, error) {
	select {
	case ev, ok := <-inputCh:
		if !ok {
			return "", fmt.Errorf("stdin reader closed unexpectedly")
		}
		if ev.err != nil {
			return "", ev.err
		}
		return ev.line, nil
	case err := <-errCh:
		if err != nil {
			return "", fmt.Errorf("serial read error: %w", err)
		}
		return "", fmt.Errorf("serial reader stopped")
	case <-sigCh:
		return "", fmt.Errorf("interrupted")
	}
}

func receiveLoop(ctx context.Context, dev *mt.Device, channel uint32, rxCh chan<- mt.RxText, errCh chan<- error) {
	defer close(rxCh)
	for {
		rx, err := dev.RecvText(ctx)
		if err != nil {
			logx.Debugf("client recv error: %v", err)
			select {
			case errCh <- err:
			default:
			}
			return
		}
		if rx.Channel != channel {
			logx.Debugf("client ignoring channel=%d (want %d)", rx.Channel, channel)
			continue
		}
		rxCh <- rx
	}
}
