package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"mtsh/internal/logx"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"

	"github.com/spf13/cobra"
)

var (
	flagWaitTimeout time.Duration
	flagChunkAck    bool
)

var clientCmd = &cobra.Command{
	Use:   "client",
	Short: "Terminal proxy: send commands over Meshtastic and print replies",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		logx.Debugf("client command starting: port=%s channel=%d dm=%d", flagPort, flagChannel, flagDM)
		dev, err := mt.Open(ctx, flagPort, flagAckTimeout)
		if err != nil {
			return err
		}
		defer dev.Close()
		logx.Debugf("client command connected to device: port=%s", flagPort)

		sigCh := make(chan os.Signal, 2)
		signal.Notify(sigCh, os.Interrupt)
		defer signal.Stop(sigCh)
		var ctrlCUsed bool

		dest := uint32(0xFFFFFFFF)

		fmt.Printf("client: port=%s channel=%d dest=%d wait-timeout=%s\n", flagPort, flagChannel, dest, flagWaitTimeout)
		nodeInfo := dev.Info()
		fmt.Printf("node: %s\n", nodeInfo)
		logx.Debugf("client connected node: %s", nodeInfo)
		logx.Debugf("client using dest=%d wait-timeout=%s", dest, flagWaitTimeout)

		// receiver goroutine
		rxCh := make(chan mt.RxText, 128)
		errCh := make(chan error, 1)
		go func() {
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
				if rx.Channel != flagChannel {
					logx.Debugf("client ignoring channel=%d (want %d)", rx.Channel, flagChannel)
					continue
				}
				rxCh <- rx
			}
		}()

		// line-mode
		fmt.Println("Enter commands. Ctrl+C to exit.")

		type inputEvent struct {
			line string
			err  error
		}
		inputCh := make(chan inputEvent)
		go func() {
			in := bufio.NewReader(os.Stdin)
			for {
				line, err := in.ReadString('\n')
				if err != nil {
					inputCh <- inputEvent{err: err}
					close(inputCh)
					return
				}
				inputCh <- inputEvent{line: strings.TrimSpace(line)}
			}
		}()

		for {
			fmt.Print("> ")
			var line string
			select {
			case ev, ok := <-inputCh:
				if !ok {
					return fmt.Errorf("stdin reader closed unexpectedly")
				}
				if ev.err != nil {
					return ev.err
				}
				line = ev.line
			case err := <-errCh:
				if err != nil {
					return fmt.Errorf("serial read error: %w", err)
				}
				return fmt.Errorf("serial reader stopped")
			case <-sigCh:
				return fmt.Errorf("interrupted")
			}
			if line == "" {
				continue
			}

			reqID := randID()
			opts := protofmt.RequestOptions{RequireChunkAck: flagChunkAck}
			reqMsg := protofmt.MakeRequest(reqID, line, opts)
			if err := dev.SendText(ctx, flagChannel, dest, reqMsg); err != nil {
				fmt.Fprintln(os.Stderr, "send error:", err)
				logx.Debugf("client send request failed: id=%s err=%v", reqID, err)
				continue
			}
			logx.Debugf("client sent request: id=%s cmd=%q", reqID, line)
			waitAck := flagChunkAck
			ctrlCUsed = false

			timer := time.NewTimer(flagWaitTimeout)
			defer timer.Stop()

			gotAny := false
			expectSeq := 0
			buffer := make(map[int][]byte)
			lastSeq := -1

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
					goto NEXT
				case rx, ok := <-rxCh:
					if !ok {
						return fmt.Errorf("serial reader stopped")
					}
					logx.Debugf("client received packet candidate: req=%s from=%d hops=%d hopStart=%d hopLimit=%d", reqID, rx.FromNode, rx.Hops, rx.HopStart, rx.HopLimit)
					ch, err := protofmt.ParseResponseChunkStrict(rx.Text)
					if err != nil || ch.ID != reqID {
						continue
					}

					if !gotAny {
						gotAny = true
					}

					if ch.Last {
						lastSeq = ch.Seq
					}

					if ch.Seq < expectSeq {
						// duplicate chunk; already processed
						if waitAck {
							sendChunkAck(ctx, dev, reqID, ch.Seq, dest)
						}
						continue
					}

					buffer[ch.Seq] = append([]byte(nil), ch.Data...)

					for {
						data, ok := buffer[expectSeq]
						if !ok {
							break
						}
						_, _ = os.Stdout.Write(data)
						delete(buffer, expectSeq)
						expectSeq++
					}

					if waitAck {
						sendChunkAck(ctx, dev, reqID, ch.Seq, dest)
					}

					if ch.Last && lastSeq >= 0 && expectSeq > lastSeq {
						logx.Debugf("client finished receiving response: id=%s chunks=%d", reqID, expectSeq)
						goto NEXT
					}

					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(flagWaitTimeout)
				case <-sigCh:
					if !ctrlCUsed {
						ctrlCUsed = true
						fmt.Fprintln(os.Stderr, "\n[mtsh] response canceled by Ctrl+C (press Ctrl+C again to exit)")
						goto NEXT
					}
					return fmt.Errorf("interrupted")
				}
			}
		NEXT:
			logx.Debugf("client ready for next command")
			continue
		}
	},
}

func init() {
	clientCmd.Flags().DurationVar(&flagWaitTimeout, "wait-timeout", 2*time.Minute, "Timeout waiting for remaining response chunks")
	clientCmd.Flags().BoolVar(&flagChunkAck, "chunk-ack", false, "Request ACK between response chunks")
}

func randID() string {
	var x [2]byte
	_, _ = rand.Read(x[:])
	return hex.EncodeToString(x[:])
}

func sendChunkAck(ctx context.Context, dev *mt.Device, reqID string, seq int, dest uint32) {
	msg := protofmt.MakeChunkAck(reqID, seq)
	if err := dev.SendText(ctx, flagChannel, dest, msg); err != nil {
		logx.Debugf("client failed to send chunk ack: id=%s seq=%d err=%v", reqID, seq, err)
		return
	}
	logx.Debugf("client sent chunk ack: id=%s seq=%d", reqID, seq)
}
