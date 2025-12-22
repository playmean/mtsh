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

type ResponseChunk struct {
	ID      string
	Seq     int
	Last    bool
	Encoded bool
	Data    []byte
	Total   int
}

type ChunkType byte

const (
	ChunkTypeResponse   ChunkType = '1'
	ChunkTypeFileUpload ChunkType = '2'
	ChunkTypeFileAck    ChunkType = '3'
)

var errWrongChunkType = errors.New("wrong chunk type")

func MakeResponseChunk(id string, seq int, last bool, total int, data []byte) string {
	return makeChunk(ChunkTypeResponse, id, seq, last, total, data)
}

func ParseResponseChunkStrict(s string) (ResponseChunk, error) {
	ch, err := parseChunk(ChunkTypeResponse, s)
	if errors.Is(err, errWrongChunkType) {
		return ResponseChunk{}, errors.New("not a response")
	}
	return ch, err
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

type FileChunk struct {
	ID         string
	Seq        int
	Last       bool
	Total      int
	Path       string
	Data       []byte
	RequireAck bool
	Compressed bool
}

var (
	ErrNotFileChunk      = errors.New("not a file chunk")
	ErrNotFileChunkReply = errors.New("not a file chunk reply")
)

func MakeFileChunkUpload(id string, seq int, last bool, total int, path string, data []byte, compressed bool, requireAck bool) string {
	lastStr := "0"
	if last {
		lastStr = "1"
	}
	totalStr := ""
	if total >= 0 {
		totalStr = strconv.Itoa(total)
	}
	payload := string(data)
	encoded := "0"
	if hasNonASCII(data) {
		encoded = "1"
		payload = base64.RawURLEncoding.EncodeToString(data)
	}
	ackStr := "0"
	if requireAck {
		ackStr = "1"
	}
	compStr := "0"
	if compressed {
		compStr = "1"
	}
	pathEnc := base64.RawURLEncoding.EncodeToString([]byte(path))
	return fmt.Sprintf("%s2:%s:%d:%s:%s:%s:%s:%s:%s:%s", Prefix, id, seq, lastStr, encoded, totalStr, ackStr, compStr, pathEnc, payload)
}

func ParseFileChunkUpload(s string) (FileChunk, error) {
	prefix := Prefix + "2:"
	if !strings.HasPrefix(s, prefix) {
		return FileChunk{}, ErrNotFileChunk
	}
	rest := strings.TrimPrefix(s, prefix)
	parts := strings.SplitN(rest, ":", 9)
	if len(parts) != 9 {
		return FileChunk{}, errors.New("bad file chunk format")
	}
	id := parts[0]
	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return FileChunk{}, err
	}
	last := parts[2] == "1"
	encoded := parts[3] == "1"
	total := -1
	if parts[4] != "" {
		total, err = strconv.Atoi(parts[4])
		if err != nil {
			return FileChunk{}, err
		}
	}
	requireAck := parts[5] == "1"
	compressed := parts[6] == "1"
	pathBytes, err := base64.RawURLEncoding.DecodeString(parts[7])
	if err != nil {
		return FileChunk{}, err
	}
	payload := parts[8]
	var data []byte
	if encoded {
		data, err = base64.RawURLEncoding.DecodeString(payload)
		if err != nil {
			return FileChunk{}, err
		}
	} else {
		data = []byte(payload)
	}
	return FileChunk{
		ID:         id,
		Seq:        seq,
		Last:       last,
		Total:      total,
		Path:       string(pathBytes),
		Data:       data,
		RequireAck: requireAck,
		Compressed: compressed,
	}, nil
}

func MakeFileChunkReply(id string, seq int, last bool, total int, data []byte) string {
	return makeChunk(ChunkTypeFileAck, id, seq, last, total, data)
}

func ParseFileChunkReply(s string) (ResponseChunk, error) {
	ch, err := parseChunk(ChunkTypeFileAck, s)
	if errors.Is(err, errWrongChunkType) {
		return ResponseChunk{}, ErrNotFileChunkReply
	}
	return ch, err
}

func makeChunk(kind ChunkType, id string, seq int, last bool, total int, data []byte) string {
	lastStr := "0"
	if last {
		lastStr = "1"
	}
	totalStr := ""
	if total >= 0 {
		totalStr = strconv.Itoa(total)
	}
	if hasNonASCII(data) {
		enc := base64.RawURLEncoding.EncodeToString(data)
		return fmt.Sprintf("%s%c:%s:%d:%s:1:%s:%s", Prefix, kind, id, seq, lastStr, totalStr, enc)
	}
	return fmt.Sprintf("%s%c:%s:%d:%s:0:%s:%s", Prefix, kind, id, seq, lastStr, totalStr, string(data))
}

func parseChunk(kind ChunkType, s string) (ResponseChunk, error) {
	prefix := fmt.Sprintf("%s%c:", Prefix, kind)
	if !strings.HasPrefix(s, prefix) {
		return ResponseChunk{}, errWrongChunkType
	}
	rest := strings.TrimPrefix(s, prefix)
	parts := strings.SplitN(rest, ":", 6)
	if len(parts) != 5 && len(parts) != 6 {
		return ResponseChunk{}, errors.New("bad response format")
	}
	id := parts[0]
	seq, err := strconv.Atoi(parts[1])
	if err != nil {
		return ResponseChunk{}, err
	}
	last := parts[2] == "1"
	encoded := parts[3] == "1"
	total := -1
	var payload string
	if len(parts) == 5 {
		payload = parts[4]
	} else {
		if parts[4] != "" {
			total, err = strconv.Atoi(parts[4])
			if err != nil {
				return ResponseChunk{}, err
			}
		}
		payload = parts[5]
	}
	var data []byte
	if encoded {
		data, err = base64.RawURLEncoding.DecodeString(payload)
		if err != nil {
			return ResponseChunk{}, err
		}
	} else {
		data = []byte(payload)
	}
	return ResponseChunk{ID: id, Seq: seq, Last: last, Encoded: encoded, Data: data, Total: total}, nil
}
