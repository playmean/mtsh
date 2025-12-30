package server

import (
	"context"
	"errors"
	"fmt"

	"mtsh/internal/logx"
	"mtsh/internal/lru"
	"mtsh/internal/mt"
	"mtsh/internal/protofmt"
)

// Run starts the server loop and blocks until the context is canceled or the device errors.
func Run(ctx context.Context, dev *mt.Device, cfg Config) error {
	logx.Debugf("server config: channel=%d shell=%s chunkBytes=%d chunkDelay=%s chunkAckTimeout=%s chunkAckRetries=%d",
		cfg.Channel, cfg.Shell, cfg.ChunkBytes, cfg.ChunkDelay, cfg.ChunkAckTimeout, cfg.ChunkAckRetries)

	dedup := lru.New(cfg.DedupCap, cfg.DedupTTL)
	logx.Debugf("server dedup config: ttl=%s cap=%d", cfg.DedupTTL, cfg.DedupCap)
	ackMgr := newChunkAckManager()
	reqCancels := newRequestCanceler()

	for {
		rx, err := dev.RecvText(ctx)
		if err != nil {
			logx.Debugf("server recv error: %v", err)
			return fmt.Errorf("serial read error: %w", err)
		}

		isDM := rx.ToNode == dev.Info().Number
		if cfg.DmOnly {
			if !isDM {
				logx.Debugf("server dropping non-DM packet: from=%d to=%d channel=%d", rx.FromNode, rx.ToNode, rx.Channel)
				continue
			}
		} else if rx.Channel != cfg.Channel {
			if rx.Channel == 0 && isDM {
				logx.Debugf("server accepting direct message on channel 0 for node=%d", rx.ToNode)
			} else {
				logx.Debugf("server dropping packet from channel %d (want %d)", rx.Channel, cfg.Channel)
				continue
			}
		}

		if ack, ok := protofmt.ParseChunkAck(rx.Text); ok {
			evt := chunkAckEvent{
				ack:      ack,
				hops:     rx.Hops,
				hopStart: rx.HopStart,
				hopLimit: rx.HopLimit,
			}
			if ackMgr.deliver(evt) {
				logx.Debugf("server received chunk ack: id=%s seq=%d from=%d hops=%d hopStart=%d hopLimit=%d",
					ack.ID, ack.Seq, rx.FromNode, rx.Hops, rx.HopStart, rx.HopLimit)
			} else {
				logx.Debugf("server got unexpected chunk ack: id=%s seq=%d from=%d", ack.ID, ack.Seq, rx.FromNode)
			}
			continue
		}

		if cancelID, ok := protofmt.ParseCancel(rx.Text); ok {
			if reqCancels.cancel(cancelID) {
				logx.Debugf("server canceled request: id=%s from=%d", cancelID, rx.FromNode)
			} else {
				logx.Debugf("server ignoring cancel for unknown request: id=%s from=%d", cancelID, rx.FromNode)
			}
			continue
		}

		if chunk, err := protofmt.ParseFileChunkUpload(rx.Text); err == nil {
			handleFileChunk(ctx, dev, cfg, chunk, rx.FromNode)
			continue
		} else if !errors.Is(err, protofmt.ErrNotFileChunk) {
			logx.Debugf("server invalid file chunk: %v", err)
			continue
		}

		req, ok := protofmt.ParseRequest(rx.Text)
		if !ok {
			logx.Debugf("server ignoring non-request packet")
			continue
		}

		if dedup.Seen(req.ID) {
			logx.Debugf("server duplicate request ignored: id=%s", req.ID)
			continue
		}

		reqCancels.cancelAll()
		reqCtx, cancel := context.WithCancel(ctx)
		cleanup := reqCancels.register(req.ID, cancel)

		logx.Debugf("server received request: id=%s from=%d hops=%d hopStart=%d hopLimit=%d", req.ID, rx.FromNode, rx.Hops, rx.HopStart, rx.HopLimit)
		respDest := mt.BroadcastDest
		if rx.ToNode != 0 && rx.ToNode != mt.BroadcastDest {
			respDest = rx.FromNode
		}

		go func() {
			defer cleanup()
			defer cancel()
			handleRequest(reqCtx, dev, cfg, req, ackMgr, respDest)
		}()
	}
}
