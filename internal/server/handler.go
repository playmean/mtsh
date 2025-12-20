package server

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os/exec"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"
)

func handleRequest(ctx context.Context, dev *mt.Device, cfg Config, req protofmt.Request, ackMgr *chunkAckManager) {
	out := runShell(cfg.Shell, req.Command, cfg.CmdTimeout)
	payload, compressed := maybeCompressResponse(out)
	chunks := chunkBytes(payload, cfg.ChunkBytes)
	totalChunks := len(chunks)

	dest := mt.BroadcastDest
	var ackCh <-chan chunkAckEvent
	var ackEnabled bool
	if req.RequireChunkAck && ackMgr != nil {
		ackCh = ackMgr.register(req.ID)
		ackEnabled = true
		logx.Debugf("server chunk ACKs enabled: id=%s chunks=%d", req.ID, len(chunks))
		defer ackMgr.unregister(req.ID)
	} else if req.RequireChunkAck {
		logx.Debugf("server chunk ACKs requested but manager unavailable: id=%s", req.ID)
	}

	retryLimit := cfg.ChunkAckRetries
	if retryLimit <= 0 {
		retryLimit = 3
	}

	for i, c := range chunks {
		last := i == len(chunks)-1
		attempt := 0
		for {
			total := -1
			if i == 0 {
				total = totalChunks
			}
			msg := protofmt.MakeResponseChunk(req.ID, i, last, total, c)
			if err := dev.SendText(ctx, cfg.Channel, dest, msg); err != nil {
				logx.Debugf("server failed to send response chunk: id=%s err=%v", req.ID, err)
				return
			}

			var waitErr error
			if ackEnabled {
				waitErr = waitForClientAck(ctx, req.ID, ackCh, i, cfg.ChunkAckTimeout)
			} else if !last && cfg.ChunkDelay > 0 {
				waitErr = sleepWithContext(ctx, cfg.ChunkDelay)
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
	logx.Debugf("server sent %d chunks for request id=%s ack=%v retries=%d compressed=%v", len(chunks), req.ID, ackEnabled, retryLimit, compressed)
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

func maybeCompressResponse(data []byte) ([]byte, bool) {
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
	if buf.Len() >= len(data) {
		logx.Debugf("server skipping compression: in=%d out=%d (no gain)", len(data), buf.Len())
		return data, false
	}
	logx.Debugf("server compressed response: in=%d out=%d", len(data), buf.Len())
	return buf.Bytes(), true
}
