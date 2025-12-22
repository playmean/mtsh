package server

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"time"

	"mtsh/internal/chunks"
	"mtsh/internal/logx"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"
)

func handleRequest(ctx context.Context, dev *mt.Device, cfg Config, req protofmt.Request, ackMgr *chunkAckManager) {
	out := runShell(cfg.Shell, req.Command, cfg.CmdTimeout)
	requireAck := cfg.ChunkAck || req.RequireChunkAck
	sender, cleanup := newChunkSender(dev, cfg, req, ackMgr, protofmt.ChunkTypeResponse, requireAck)
	if cleanup != nil {
		defer cleanup()
	}
	compressed, err := sender.SendPayload(ctx, req.ID, out, cfg.ChunkBytes, true)
	if err != nil {
		logx.Debugf("server failed to send response: id=%s err=%v", req.ID, err)
		return
	}
	logx.Debugf("server sent response chunks: id=%s ack=%v compressed=%v", req.ID, sender.RequireAck, compressed)
}

func handleFileChunk(ctx context.Context, dev *mt.Device, cfg Config, chunk protofmt.FileChunk) {
	err := processFileChunk(chunk)
	if !chunk.RequireAck {
		return
	}
	respReq := protofmt.Request{ID: chunk.ID}
	sender, _ := newChunkSender(dev, cfg, respReq, nil, protofmt.ChunkTypeFileAck, false)
	payload := []byte(strconv.Itoa(chunk.Seq))
	if err != nil {
		payload = []byte("[mtsh] cp error: " + err.Error() + "\n")
	}
	if sendErr := sender.SendChunk(ctx, chunk.ID, chunk.Seq, chunk.Last, chunk.Total, payload); sendErr != nil {
		logx.Debugf("server failed to send cp ack: id=%s err=%v", chunk.ID, sendErr)
	}
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

func newChunkSender(dev *mt.Device, cfg Config, req protofmt.Request, ackMgr *chunkAckManager, chunkType protofmt.ChunkType, wantAck bool) (chunks.Sender, func()) {
	builder := func(id string, seq int, last bool, total int, data []byte) string {
		switch chunkType {
		case protofmt.ChunkTypeFileAck:
			return protofmt.MakeFileChunkReply(id, seq, last, total, data)
		default:
			return protofmt.MakeResponseChunk(id, seq, last, total, data)
		}
	}

	retries := cfg.ChunkAckRetries
	if retries <= 0 {
		retries = 3
	}

	sender := chunks.Sender{
		Device:     dev,
		Channel:    cfg.Channel,
		Builder:    builder,
		RequireAck: wantAck && ackMgr != nil,
		AckRetries: retries,
		ChunkDelay: cfg.ChunkDelay,
	}

	var cleanup func()
	if wantAck {
		if ackMgr == nil {
			logx.Debugf("server chunk ACKs requested but manager unavailable: id=%s", req.ID)
			return sender, nil
		}
		ackCh := ackMgr.register(req.ID)
		sender.WaitAck = func(ctx context.Context, seq int, last bool) error {
			return waitForClientAck(ctx, req.ID, ackCh, seq, cfg.ChunkAckTimeout)
		}
		cleanup = func() {
			ackMgr.unregister(req.ID)
		}
	}

	return sender, cleanup
}
