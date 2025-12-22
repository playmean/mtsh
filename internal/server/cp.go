package server

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"mtsh/internal/logx"
	"mtsh/internal/protofmt"
)

type compressedUploadState struct {
	path string
	buf  bytes.Buffer
}

var compressedUploads = struct {
	mu     sync.Mutex
	states map[string]*compressedUploadState
}{
	states: make(map[string]*compressedUploadState),
}

func processFileChunk(chunk protofmt.FileChunk) error {
	path := filepath.Clean(chunk.Path)
	if path == "" || path == "." {
		return fmt.Errorf("invalid path")
	}
	if chunk.Compressed {
		return handleCompressedUpload(path, chunk)
	}
	if err := writeCopyChunk(path, chunk.Seq, chunk.Data); err != nil {
		logx.Debugf("server cp chunk error: path=%s seq=%d err=%v", path, chunk.Seq, err)
		return err
	}
	logx.Debugf("server cp wrote chunk: path=%s seq=%d total=%d bytes=%d last=%v compressed=%v",
		path, chunk.Seq, chunk.Total, len(chunk.Data), chunk.Last, chunk.Compressed)
	return nil
}

func handleCompressedUpload(path string, chunk protofmt.FileChunk) error {
	compressedUploads.mu.Lock()
	state := compressedUploads.states[chunk.ID]
	if state == nil {
		state = &compressedUploadState{path: path}
		compressedUploads.states[chunk.ID] = state
	}
	if state.path != path {
		compressedUploads.mu.Unlock()
		return fmt.Errorf("path mismatch")
	}
	if _, err := state.buf.Write(chunk.Data); err != nil {
		compressedUploads.mu.Unlock()
		return err
	}
	if !chunk.Last {
		compressedUploads.mu.Unlock()
		logx.Debugf("server cp buffered compressed chunk: id=%s seq=%d bytes=%d", chunk.ID, chunk.Seq, len(chunk.Data))
		return nil
	}
	data := state.buf.Bytes()
	delete(compressedUploads.states, chunk.ID)
	compressedUploads.mu.Unlock()

	decoded, err := decompressChunk(data)
	if err != nil {
		return fmt.Errorf("decompress: %w", err)
	}
	if err := writeFullFile(path, decoded); err != nil {
		return err
	}
	logx.Debugf("server cp wrote decompressed file: path=%s bytes=%d", path, len(decoded))
	return nil
}

func writeCopyChunk(path string, seq int, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create parent %s: %w", dir, err)
		}
	}
	var flags int
	if seq == 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	} else {
		flags = os.O_WRONLY | os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	if len(data) == 0 {
		return nil
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeFullFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func decompressChunk(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out bytes.Buffer
	if _, err := out.ReadFrom(zr); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
