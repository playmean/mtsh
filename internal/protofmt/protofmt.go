package protofmt

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const Prefix = "mtsh:"

type Request struct {
	ID              string
	Command         string
	RequireChunkAck bool
}

type RequestOptions struct {
	RequireChunkAck bool
}

type ResponseChunk struct {
	ID      string
	Seq     int
	Last    bool
	Encoded bool
	Data    []byte
}

func MakeRequest(id, cmd string, opts RequestOptions) string {
	optStr := encodeRequestOptions(opts)
	if optStr == "" {
		return fmt.Sprintf("%s0:%s:%s", Prefix, id, cmd)
	}
	return fmt.Sprintf("%s0:%s!%s:%s", Prefix, id, optStr, cmd)
}

func ParseRequest(s string) (Request, bool) {
	if !strings.HasPrefix(s, Prefix+"0:") {
		return Request{}, false
	}
	rest := strings.TrimPrefix(s, Prefix+"0:")
	i := strings.IndexByte(rest, ':')
	if i <= 0 {
		return Request{}, false
	}
	head := rest[:i]
	cmd := rest[i+1:]
	if head == "" {
		return Request{}, false
	}
	opts := RequestOptions{}
	id := head
	if bang := strings.IndexByte(head, '!'); bang >= 0 {
		id = head[:bang]
		optStr := head[bang+1:]
		if id == "" {
			return Request{}, false
		}
		opts = decodeRequestOptions(optStr)
	}
	return Request{
		ID:              id,
		Command:         cmd,
		RequireChunkAck: opts.RequireChunkAck,
	}, true
}

func MakeResponseChunk(id string, seq int, last bool, data []byte) string {
	lastStr := "0"
	if last {
		lastStr = "1"
	}
	if hasNonASCII(data) {
		enc := base64.RawURLEncoding.EncodeToString(data)
		return fmt.Sprintf("%s1:%s:%d:%s:1:%s", Prefix, id, seq, lastStr, enc)
	}
	return fmt.Sprintf("%s1:%s:%d:%s:0:%s", Prefix, id, seq, lastStr, string(data))
}

func ParseResponseChunkStrict(s string) (ResponseChunk, error) {
	if !strings.HasPrefix(s, Prefix+"1:") {
		return ResponseChunk{}, errors.New("not a response")
	}
	rest := strings.TrimPrefix(s, Prefix+"1:")
	parts := strings.SplitN(rest, ":", 5)
	if len(parts) != 5 {
		return ResponseChunk{}, errors.New("bad response format")
	}
	id := parts[0]
	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return ResponseChunk{}, err
	}
	last := parts[2] == "1"
	encoded := parts[3] == "1"
	var data []byte
	if encoded {
		data, err = base64.RawURLEncoding.DecodeString(parts[4])
		if err != nil {
			return ResponseChunk{}, err
		}
	} else {
		data = []byte(parts[4])
	}
	return ResponseChunk{ID: id, Seq: seq, Last: last, Encoded: encoded, Data: data}, nil
}

func hasNonASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return true
		}
	}
	return false
}

type ChunkAck struct {
	ID  string
	Seq int
}

func MakeChunkAck(id string, seq int) string {
	return fmt.Sprintf("%sack:%s:%d", Prefix, id, seq)
}

func ParseChunkAck(s string) (ChunkAck, bool) {
	if !strings.HasPrefix(s, Prefix+"ack:") {
		return ChunkAck{}, false
	}
	rest := strings.TrimPrefix(s, Prefix+"ack:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || parts[0] == "" {
		return ChunkAck{}, false
	}
	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return ChunkAck{}, false
	}
	return ChunkAck{ID: parts[0], Seq: seq}, true
}

func encodeRequestOptions(opts RequestOptions) string {
	var tokens []string
	if opts.RequireChunkAck {
		tokens = append(tokens, "ack")
	}
	return strings.Join(tokens, ",")
}

func decodeRequestOptions(s string) RequestOptions {
	var opts RequestOptions
	if s == "" {
		return opts
	}
	for _, token := range strings.Split(s, ",") {
		switch token {
		case "ack":
			opts.RequireChunkAck = true
		case "", "0":
		default:
		}
	}
	return opts
}
