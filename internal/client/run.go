package client

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"
)

// Run starts the interactive client loop.
func Run(ctx context.Context, dev *mt.Device, cfg Config) error {
	logx.Debugf("client config: channel=%d wait-timeout=%s chunkAck=%v", cfg.Channel, cfg.WaitTimeout, cfg.ChunkAck)

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	var ctrlCUsed bool

	dest := mt.BroadcastDest

	rxCh := make(chan mt.RxText, 128)
	errCh := make(chan error, 1)
	go receiveLoop(ctx, dev, cfg.Channel, rxCh, errCh)

	fmt.Println("Enter commands. Ctrl+C to exit.")

	inputCh := make(chan inputEvent)
	go readInput(inputCh)

	for {
		fmt.Print("> ")
		line, err := waitForLine(inputCh, errCh, sigCh)
		if err != nil {
			return err
		}
		if line == "" {
			continue
		}

		reqID := randID()
		opts := protofmt.RequestOptions{RequireChunkAck: cfg.ChunkAck}
		reqMsg := protofmt.MakeRequest(reqID, line, opts)
		if err := dev.SendText(ctx, cfg.Channel, dest, reqMsg); err != nil {
			fmt.Fprintln(os.Stderr, "send error:", err)
			logx.Debugf("client send request failed: id=%s err=%v", reqID, err)
			continue
		}
		logx.Debugf("client sent request: id=%s cmd=%q", reqID, line)

		waitAck := cfg.ChunkAck
		ctrlCUsed = false

		timer := time.NewTimer(cfg.WaitTimeout)

		gotAny := false
		expectSeq := 0
		buffer := make(map[int][]byte)
		encodedMap := make(map[int]bool)
		lastSeq := -1
		gzipResponse := false
		var gzipBuf bytes.Buffer
		var outputBuf bytes.Buffer
		totalChunks := -1
		progressEnabled := cfg.ShowProgress && !cfg.StreamChunks

	Processing:
		for {
			select {
			case err := <-errCh:
				if err != nil {
					return fmt.Errorf("serial read error: %w", err)
				}
				return fmt.Errorf("serial reader stopped")
			case <-timer.C:
				if gotAny {
					fmt.Fprintln(os.Stderr, "[mtsh] timeout waiting for remaining chunks")
				} else {
					fmt.Fprintln(os.Stderr, "[mtsh] timeout waiting for response")
				}
				logx.Debugf("client timed out waiting for response: id=%s gotAny=%v", reqID, gotAny)
				break Processing
			case rx, ok := <-rxCh:
				if !ok {
					return fmt.Errorf("serial reader stopped")
				}
				logx.Debugf("client received packet candidate: req=%s from=%d hops=%d hopStart=%d hopLimit=%d", reqID, rx.FromNode, rx.Hops, rx.HopStart, rx.HopLimit)
				ch, err := protofmt.ParseResponseChunkStrict(rx.Text)
				if err != nil {
					logx.Debugf("client failed to parse response chunk: from=%d err=%v text=%q", rx.FromNode, err, rx.Text)

					continue
				}
				if ch.ID != reqID {
					continue
				}

				if ch.Total >= 0 && totalChunks < 0 {
					totalChunks = ch.Total
				}
				if !gotAny {
					gotAny = true
				}

				if ch.Last {
					lastSeq = ch.Seq
				}

				if ch.Seq < expectSeq {
					if waitAck {
						sendChunkAck(ctx, dev, cfg.Channel, reqID, ch.Seq)
					}
					continue
				}

				buffer[ch.Seq] = append([]byte(nil), ch.Data...)
				encodedMap[ch.Seq] = ch.Encoded

				for {
					data, ok := buffer[expectSeq]
					if !ok {
						break
					}
					if expectSeq == 0 && encodedMap[expectSeq] && looksLikeGzip(data) {
						gzipResponse = true
					}
					if gzipResponse {
						gzipBuf.Write(data)
					} else if cfg.StreamChunks {
						_, _ = os.Stdout.Write(data)
					} else {
						outputBuf.Write(data)
					}
					delete(buffer, expectSeq)
					delete(encodedMap, expectSeq)
					expectSeq++
				}
				completed := ch.Last && lastSeq >= 0 && expectSeq > lastSeq

				if progressEnabled && totalChunks > 1 && !completed {
					renderChunkProgress(expectSeq, totalChunks, rx.Hops)
				}

				if waitAck {
					sendChunkAck(ctx, dev, cfg.Channel, reqID, ch.Seq)
				}

				if completed {
					if gzipResponse {
						if err := flushGzipBuffer(&gzipBuf); err != nil {
							fmt.Fprintf(os.Stderr, "[mtsh] failed to decompress response: %v\n", err)
							logx.Debugf("client failed to decompress gzip response id=%s: %v", reqID, err)
						}
						gzipBuf.Reset()
						gzipResponse = false
					}
					logx.Debugf("client finished receiving response: id=%s chunks=%d", reqID, expectSeq)
					break Processing
				}

				resetTimer(timer, cfg.WaitTimeout)
			case <-sigCh:
				if !ctrlCUsed {
					ctrlCUsed = true
					fmt.Fprintln(os.Stderr, "\n[mtsh] response canceled by Ctrl+C (press Ctrl+C again to exit)")
					break Processing
				}
				return fmt.Errorf("interrupted")
			}
		}

		if !cfg.StreamChunks && !gzipResponse {
			_, _ = os.Stdout.Write(outputBuf.Bytes())
		}
		stopTimer(timer)
		logx.Debugf("client ready for next command")
	}
}
