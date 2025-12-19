package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/lru"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"

	"github.com/spf13/cobra"
)

var (
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
		logx.Debugf("server command starting: port=%s channel=%d shell=%s chunkBytes=%d", flagPort, flagChannel, flagShell, flagChunkBytes)
		dev, err := mt.Open(ctx, flagPort, flagAckTimeout)
		if err != nil {
			return err
		}
		defer dev.Close()
		logx.Debugf("server command connected to device: port=%s", flagPort)

		dedup := lru.New(flagDedupCap, flagDedupTTL)
		ackMgr := newChunkAckManager()

		fmt.Printf("server: port=%s channel=%d dm-only=%v\n", flagPort, flagChannel, flagDMOnly)
		nodeInfo := dev.Info()
		fmt.Printf("node: %s\n", nodeInfo)
		logx.Debugf("server connected node: %s", nodeInfo)
		logx.Debugf("server dedup config: ttl=%s cap=%d", flagDedupTTL, flagDedupCap)

		for {
			rx, err := dev.RecvText(ctx)
			if err != nil {
				logx.Debugf("server recv error: %v", err)
				return fmt.Errorf("serial read error: %w", err)
			}

			// channel filter: для DM обычно всё равно идёт на channel index; оставим как есть
			if rx.Channel != flagChannel {
				logx.Debugf("server dropping packet from channel %d (want %d)", rx.Channel, flagChannel)
				continue
			}

			// dm-only: broadcast игнорируем
			if flagDMOnly {
				// BroadcastNodenum в meshtastic-go — константа; но ToNode может быть 0.
				// Практически: DM имеет To != 0 && To != broadcast.
				if rx.ToNode == 0 {
					logx.Debugf("server ignoring broadcast packet while dm-only mode active")
					continue
				}
			}

			// --- chunk ACK handling ---
			if ack, ok := protofmt.ParseChunkAck(rx.Text); ok {
				if ackMgr.deliver(ack) {
					logx.Debugf("server received chunk ack: id=%s seq=%d from=%d", ack.ID, ack.Seq, rx.FromNode)
				} else {
					logx.Debugf("server got unexpected chunk ack: id=%s seq=%d from=%d", ack.ID, ack.Seq, rx.FromNode)
				}
				continue
			}

			// --- non-interactive request ---
			req, ok := protofmt.ParseRequest(rx.Text)
			if !ok {
				logx.Debugf("server ignoring non-request packet")
				continue
			}

			if dedup.Seen(req.ID) {
				// уже исполняли — игнорируем
				logx.Debugf("server duplicate request ignored: id=%s", req.ID)
				continue
			}

			logx.Debugf("server received request: id=%s from=%d hops=%d hopStart=%d hopLimit=%d", req.ID, rx.FromNode, rx.Hops, rx.HopStart, rx.HopLimit)

			go handleRequest(ctx, dev, req, ackMgr)
		}
	},
}

func init() {
	serverCmd.Flags().DurationVar(&flagCmdTimeout, "cmd-timeout", 20*time.Second, "Per-command execution timeout")
	serverCmd.Flags().DurationVar(&flagChunkDelay, "chunk-delay", 5*time.Second, "Delay between response chunks when client does not request ACK")
	serverCmd.Flags().DurationVar(&flagChunkAckTimeout, "chunk-ack-timeout", time.Minute, "Timeout waiting for client chunk ACK")
	serverCmd.Flags().IntVar(&flagChunkAckRetries, "chunk-ack-retries", 3, "Retries for chunk resend if ACK not received")
}

func runShell(shell, command string, timeout time.Duration) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	c := exec.CommandContext(ctx, shell, "-lc", command)
	logx.Debugf("server executing command: shell=%s timeout=%s cmd=%q", shell, timeout, command)

	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf

	err := c.Run()
	logx.Debugf("server command complete: cmd=%q err=%v bytes=%d", command, err, buf.Len())

	if ctx.Err() == context.DeadlineExceeded {
		buf.WriteString("\n[mtsh] command timed out\n")
	}
	return buf.Bytes()
}

func chunkBytes(b []byte, max int) [][]byte {
	if max <= 0 {
		max = 180
	}
	if len(b) == 0 {
		return [][]byte{[]byte{}}
	}
	var res [][]byte
	for len(b) > 0 {
		n := max
		if len(b) < n {
			n = len(b)
		}
		res = append(res, b[:n])
		b = b[n:]
	}
	return res
}

const compressThreshold = 1024

func handleRequest(ctx context.Context, dev *mt.Device, req protofmt.Request, ackMgr *chunkAckManager) {
	out := runShell(flagShell, req.Command, flagCmdTimeout)
	payload, compressed := maybeCompressResponse(out)
	chunks := chunkBytes(payload, flagChunkBytes)

	dest := uint32(0xFFFFFFFF)
	var ackCh <-chan protofmt.ChunkAck
	var ackEnabled bool
	if req.RequireChunkAck && ackMgr != nil {
		ackCh = ackMgr.register(req.ID)
		ackEnabled = true
		logx.Debugf("server chunk ACKs enabled: id=%s chunks=%d", req.ID, len(chunks))
		defer ackMgr.unregister(req.ID)
	} else if req.RequireChunkAck {
		logx.Debugf("server chunk ACKs requested but manager unavailable: id=%s", req.ID)
	}

	retryLimit := flagChunkAckRetries
	if retryLimit <= 0 {
		retryLimit = 3
	}

	for i, c := range chunks {
		last := i == len(chunks)-1
		attempt := 0
		for {
			msg := protofmt.MakeResponseChunk(req.ID, i, last, c)
			if err := dev.SendText(ctx, flagChannel, dest, msg); err != nil {
				logx.Debugf("server failed to send response chunk: id=%s err=%v", req.ID, err)
				return
			}

			var waitErr error
			if ackEnabled {
				waitErr = waitForClientAck(ctx, req.ID, ackCh, i)
			} else if !last && flagChunkDelay > 0 {
				waitErr = sleepWithContext(ctx, flagChunkDelay)
			}

			if waitErr == nil {
				break
			}

			if ackEnabled && errors.Is(waitErr, errChunkAckTimeout) && attempt < retryLimit {
				attempt++
				logx.Debugf("server chunk ack timed out, retrying: id=%s seq=%d attempt=%d/%d", req.ID, i, attempt, retryLimit)
				continue
			}

			logx.Debugf("server chunk handling failed: id=%s seq=%d err=%v", req.ID, i, waitErr)
			return
		}
	}
	logx.Debugf("server sent %d chunks for request id=%s replyDest=%d ack=%v retries=%d compressed=%v", len(chunks), req.ID, dest, ackEnabled, retryLimit, compressed)
}

func maybeCompressResponse(data []byte) ([]byte, bool) {
	if len(data) <= compressThreshold {
		return data, false
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		logx.Debugf("server gzip compression failed, sending raw: err=%v", err)
		_ = zw.Close()
		return data, false
	}
	if err := zw.Close(); err != nil {
		logx.Debugf("server gzip close failed, sending raw: err=%v", err)
		return data, false
	}
	logx.Debugf("server compressed response: in=%d out=%d", len(data), buf.Len())
	return buf.Bytes(), true
}

type chunkAckManager struct {
	mu      sync.Mutex
	waiters map[string]chan protofmt.ChunkAck
}

func newChunkAckManager() *chunkAckManager {
	return &chunkAckManager{
		waiters: make(map[string]chan protofmt.ChunkAck),
	}
}

func (m *chunkAckManager) register(id string) <-chan protofmt.ChunkAck {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.waiters[id]; ok {
		close(ch)
	}
	ch := make(chan protofmt.ChunkAck, 8)
	m.waiters[id] = ch
	return ch
}

func (m *chunkAckManager) unregister(id string) {
	m.mu.Lock()
	ch := m.waiters[id]
	if ch != nil {
		delete(m.waiters, id)
	}
	m.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (m *chunkAckManager) deliver(ack protofmt.ChunkAck) bool {
	m.mu.Lock()
	ch := m.waiters[ack.ID]
	m.mu.Unlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- ack:
	default:
	}
	return true
}

var (
	errChunkAckTimeout       = errors.New("chunk ack timeout")
	errChunkAckChannelClosed = errors.New("chunk ack channel closed")
)

func waitForClientAck(ctx context.Context, id string, ch <-chan protofmt.ChunkAck, seq int) error {
	if ch == nil {
		return nil
	}
	timer := time.NewTimer(flagChunkAckTimeout)
	defer timer.Stop()
	for {
		select {
		case ack, ok := <-ch:
			if !ok {
				return errChunkAckChannelClosed
			}
			if ack.Seq == seq {
				logx.Debugf("server received chunk ack confirmation: id=%s seq=%d", id, seq)
				return nil
			}
			logx.Debugf("server ignoring mismatched chunk ack: id=%s want=%d got=%d", id, seq, ack.Seq)
		case <-timer.C:
			return errChunkAckTimeout
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
