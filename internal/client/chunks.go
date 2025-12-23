package client

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"os"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"
)

func randID() string {
	var x [2]byte
	_, _ = rand.Read(x[:])
	return hex.EncodeToString(x[:])
}

func sendChunkAck(ctx context.Context, dev *mt.Device, channel uint32, reqID string, seq int) {
	msg := protofmt.MakeChunkAck(reqID, seq)
	if err := dev.SendText(ctx, channel, mt.BroadcastDest, msg); err != nil {
		logx.Debugf("client failed to send chunk ack: id=%s seq=%d err=%v", reqID, seq, err)
		return
	}
	logx.Debugf("client sent chunk ack: id=%s seq=%d", reqID, seq)
}

func sendCancel(ctx context.Context, dev *mt.Device, channel uint32, reqID string) {
	msg := protofmt.MakeCancel(reqID)
	if err := dev.SendText(ctx, channel, mt.BroadcastDest, msg); err != nil {
		logx.Debugf("client failed to send cancel: id=%s err=%v", reqID, err)
		return
	}
	logx.Debugf("client sent cancel: id=%s", reqID)
}

func looksLikeGzip(b []byte) bool {
	return len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b
}

func flushGzipBuffer(buf *bytes.Buffer) error {
	if buf.Len() == 0 {
		return nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return err
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}
