package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/lru"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

var (
	flagCmdTimeout      time.Duration
	flagIOBytes         int
	flagChunkDelay      time.Duration
	flagChunkAckTimeout time.Duration
	flagChunkAckRetries int
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run commands received over Meshtastic and reply with output (and interactive PTY)",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logx.Debugf("server command starting: port=%s channel=%d shell=%s chunkBytes=%d ioBytes=%d", flagPort, flagChannel, flagShell, flagChunkBytes, flagIOBytes)
		dev, err := mt.Open(ctx, flagPort, flagAckTimeout)
		if err != nil {
			return err
		}
		defer dev.Close()
		logx.Debugf("server command connected to device: port=%s", flagPort)

		dedup := lru.New(flagDedupCap, flagDedupTTL)
		sessDedup := lru.New(flagDedupCap, flagDedupTTL)
		ackMgr := newChunkAckManager()

		fmt.Printf("server: port=%s channel=%d dm-only=%v\n", flagPort, flagChannel, flagDMOnly)
		nodeInfo := dev.Info()
		fmt.Printf("node: %s\n", nodeInfo)
		logx.Debugf("server connected node: %s", nodeInfo)
		logx.Debugf("server dedup config: ttl=%s cap=%d", flagDedupTTL, flagDedupCap)

		var (
			sessMu   sync.Mutex
			sessions = map[string]*session{}
		)

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

			// --- interactive control ---
			if op, ok := protofmt.ParseOpen(rx.Text); ok {
				// дедуп "open" по sid+from
				key := fmt.Sprintf("open:%s:%d", op.SID, rx.FromNode)
				if sessDedup.Seen(key) {
					logx.Debugf("server duplicate session open ignored: sid=%s from=%d", op.SID, rx.FromNode)
					continue
				}

				sessMu.Lock()
				_, exists := sessions[op.SID]
				sessMu.Unlock()
				if exists {
					// already running; ignore
					logx.Debugf("server session already running, ignoring new open: sid=%s", op.SID)
					continue
				}

				// start pty session
				s := startSession(ctx, dev, op.SID, rx, op.Cmd, &sessMu, sessions)
				if s == nil {
					logx.Debugf("server failed to start session: sid=%s", op.SID)
					continue
				}
				logx.Debugf("server session started: sid=%s from=%d", op.SID, rx.FromNode)
				continue
			}
			if cl, ok := protofmt.ParseClose(rx.Text); ok {
				sessMu.Lock()
				if s := sessions[cl.SID]; s != nil {
					s.close()
					delete(sessions, cl.SID)
					logx.Debugf("server session closed via request: sid=%s", cl.SID)
				}
				sessMu.Unlock()
				continue
			}
			if ioc, err := protofmt.ParseIOStrict(rx.Text); err == nil {
				// input to pty
				sessMu.Lock()
				s := sessions[ioc.SID]
				sessMu.Unlock()
				if s == nil {
					logx.Debugf("server got IO for unknown session: sid=%s", ioc.SID)
					continue
				}
				s.writeInput(ioc.Data)
				logx.Debugf("server wrote %d bytes to session sid=%s", len(ioc.Data), ioc.SID)
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
	serverCmd.Flags().IntVar(&flagIOBytes, "io-bytes", 140, "Max io bytes per interactive frame")
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

func handleRequest(ctx context.Context, dev *mt.Device, req protofmt.Request, ackMgr *chunkAckManager) {
	out := runShell(flagShell, req.Command, flagCmdTimeout)
	chunks := chunkBytes(out, flagChunkBytes)

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
	logx.Debugf("server sent %d chunks for request id=%s replyDest=%d ack=%v retries=%d", len(chunks), req.ID, dest, ackEnabled, retryLimit)
}

// --- interactive session ---

type session struct {
	sid    string
	dev    *mt.Device
	rx     mt.RxText
	ctx    context.Context
	cancel context.CancelFunc

	ptmx   *os.File
	cmd    *exec.Cmd
	outSeq int
	mu     sync.Mutex
}

func startSession(parent context.Context, dev *mt.Device, sid string, rx mt.RxText, openCmd string, sessMu *sync.Mutex, sessions map[string]*session) *session {
	ctx, cancel := context.WithCancel(parent)

	// command to run in PTY
	shell := flagShell
	args := []string{"-l"}
	if openCmd != "" {
		// run one command but keep shell interactive by -lc? — лучше: shell -l, а команду клиент сам введёт
		// поэтому openCmd игнорируем как “startup banner”:
		_ = openCmd
	}
	c := exec.Command(shell, args...)

	ptmx, err := pty.Start(c)
	if err != nil {
		cancel()
		logx.Debugf("server start session failed to start PTY: sid=%s err=%v", sid, err)
		return nil
	}

	s := &session{
		sid:    sid,
		dev:    dev,
		rx:     rx,
		ctx:    ctx,
		cancel: cancel,
		ptmx:   ptmx,
		cmd:    c,
	}

	sessMu.Lock()
	sessions[sid] = s
	sessMu.Unlock()

	// read PTY output and send to client
	go s.pumpOutput()

	// when process exits
	go func() {
		_ = c.Wait()
		s.close()
		sessMu.Lock()
		delete(sessions, sid)
		sessMu.Unlock()
	}()

	return s
}

func (s *session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	logx.Debugf("server closing session sid=%s", s.sid)
	if s.cancel != nil {
		s.cancel()
	}
	if s.ptmx != nil {
		_ = s.ptmx.Close()
		s.ptmx = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

func (s *session) writeInput(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptmx == nil {
		return
	}
	_, _ = s.ptmx.Write(b)
}

func (s *session) pumpOutput() {
	buf := make([]byte, 4096)
	dest := uint32(0xFFFFFFFF)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		n, err := s.ptmx.Read(buf)
		if n > 0 {
			s.sendIO(dest, buf[:n])
		}
		if err != nil {
			logx.Debugf("server session output pump stopping sid=%s err=%v", s.sid, err)
			return
		}
	}
}

func (s *session) sendIO(dest uint32, data []byte) {
	max := flagIOBytes
	if max <= 0 {
		max = 140
	}
	seq := 0
	for len(data) > 0 {
		n := max
		if len(data) < n {
			n = len(data)
		}
		ch := data[:n]
		data = data[n:]
		last := len(data) == 0

		msg := protofmt.MakeIO(s.sid, seq, last, ch)
		_ = s.dev.SendText(s.ctx, flagChannel, dest, msg)
		seq++
		time.Sleep(100 * time.Millisecond)
	}
	logx.Debugf("server session sent %d IO chunks sid=%s dest=%d", seq, s.sid, dest)
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
