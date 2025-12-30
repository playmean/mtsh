package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"mtsh/internal/chunks"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"

	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <source> <destination>",
	Short: "Copy a local file to the remote server",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		src := args[0]
		dest := args[1]
		ctx := cmd.Context()
		nodeDest, err := parseNodeNumber(flagTo)
		if err != nil {
			return err
		}

		dev, port, err := openDevice(ctx)
		if err != nil {
			return err
		}
		defer dev.Close()

		fmt.Printf("cp: port=%s channel=%d dest=%d wait-timeout=%s\n", port, flagChannel, nodeDest, flagWaitTimeout)
		fmt.Printf("node: %s\n", dev.Info())

		return runCopy(ctx, dev, src, dest, nodeDest)
	},
}

func init() {
	rootCmd.AddCommand(cpCmd)
}

func runCopy(ctx context.Context, dev *mt.Device, src, dest string, nodeDest uint32) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("source %s is a directory", src)
	}

	chunkSize := flagChunkBytes
	if chunkSize <= 0 {
		chunkSize = 64
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	fileBytes, err := io.ReadAll(srcFile)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}

	originalSize := len(fileBytes)
	payload := fileBytes
	fileCompressed := false
	if compressed, ok := chunks.MaybeCompress(fileBytes); ok {
		payload = compressed
		fileCompressed = true
	}

	totalChunks := 1
	payloadLen := len(payload)
	if payloadLen > 0 {
		totalChunks = (payloadLen + chunkSize - 1) / chunkSize
	}

	fmt.Printf("[mtsh] cp sending %d bytes in %d chunks to %s\n", originalSize, totalChunks, dest)

	fileID := randomID()
	useChunkAck := !flagNoChunkAck

	builder := func(id string, seq int, last bool, total int, data []byte) string {
		return protofmt.MakeFileChunkUpload(id, seq, last, total, dest, data, fileCompressed, useChunkAck)
	}

	sender := chunks.Sender{
		Device:     dev,
		Channel:    flagChannel,
		Dest:       nodeDest,
		Builder:    builder,
		RequireAck: useChunkAck,
		AckRetries: flagChunkAckRetries,
		ChunkDelay: flagChunkDelay,
	}
	if useChunkAck {
		sender.WaitAck = makeFileReplyWaiter(dev, flagChannel, fileID, flagChunkAckTimeout)
	}

	for seq := 0; seq < totalChunks; seq++ {
		start := seq * chunkSize
		end := start + chunkSize
		if end > payloadLen {
			end = payloadLen
		}
		chunk := payload[start:end]
		chunkLen := len(chunk)
		last := seq == totalChunks-1

		fmt.Printf("[mtsh] cp chunk %d/%d (%d bytes)\n", seq+1, totalChunks, chunkLen)
		if err := sender.SendChunk(ctx, fileID, seq, last, totalChunks, chunk); err != nil {
			return fmt.Errorf("chunk %d failed: %w", seq+1, err)
		}
	}

	return nil
}

func makeFileReplyWaiter(dev *mt.Device, channel uint32, fileID string, timeout time.Duration) chunks.Waiter {
	return func(ctx context.Context, seq int, last bool) error {
		resp, err := waitForFileChunkReply(ctx, dev, channel, fileID, timeout)
		if err != nil {
			return err
		}
		if bytes.HasPrefix(resp, []byte("[mtsh] cp error")) {
			return fmt.Errorf("remote copy error: %s", strings.TrimSpace(string(resp)))
		}
		if len(resp) == 0 {
			return fmt.Errorf("empty ack response")
		}
		acked := strings.TrimSpace(string(resp))
		if acked != strconv.Itoa(seq) {
			return fmt.Errorf("ack mismatch: sent %d got %s", seq, acked)
		}
		return nil
	}
}

func waitForFileChunkReply(ctx context.Context, dev *mt.Device, channel uint32, fileID string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = time.Minute
	}
	buffer := make(map[int][]byte)
	expectSeq := 0
	lastSeq := -1
	var out bytes.Buffer

	for {
		ctxWait, cancel := context.WithTimeout(ctx, timeout)
		rx, err := dev.RecvText(ctxWait)
		cancel()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, chunks.ErrAckTimeout
			}
			return nil, err
		}
		if rx.Channel != channel {
			continue
		}
		chunk, err := protofmt.ParseFileChunkReply(rx.Text)
		if err != nil {
			if errors.Is(err, protofmt.ErrNotFileChunkReply) {
				continue
			}
			return nil, err
		}
		if chunk.ID != fileID {
			continue
		}
		buffer[chunk.Seq] = append([]byte(nil), chunk.Data...)
		if chunk.Last {
			lastSeq = chunk.Seq
		}
		for {
			data, ok := buffer[expectSeq]
			if !ok {
				break
			}
			out.Write(data)
			delete(buffer, expectSeq)
			expectSeq++
		}
		if lastSeq >= 0 && expectSeq > lastSeq {
			return out.Bytes(), nil
		}
	}
}

func randomID() string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
