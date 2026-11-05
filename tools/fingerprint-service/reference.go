package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	lmfpd "github.com/Ikaleio/lm-detector/sdk/go"
)

const referenceBankSHA256 = "dc478b8eb023ba5aa14718b7633fda3695bb7c9f5485add0c4c02417a953ebc0"
const referenceDetectorSHA256 = "59dffad8128dca8bcf0cfb024f0fcebe7f0b7d7fbae3583b4b5d1418c18e601f"

// Engine holds immutable, verified local reference data. It never refreshes data.
type Engine struct {
	detector *lmfpd.Detector
	version  string
}

// LoadEngine fails closed on missing, oversized or mismatched local files. SDK
// Load gets a private snapshot of the verified bytes, not the mutable source
// directory, eliminating a check/load race. No SDK defaults or URLs are used.
func LoadEngine(ctx context.Context, dataDir string) (engine *Engine, err error) {
	defer func() {
		if recover() != nil {
			engine, err = nil, errors.New("fingerprint reference data unavailable")
		}
	}()
	if strings.TrimSpace(dataDir) == "" || strings.Contains(dataDir, "://") {
		return nil, errors.New("fingerprint data directory must be a local path")
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return nil, errors.New("fingerprint data directory unavailable")
	}
	files := []struct {
		name string
		hash string
		max  int64
		data []byte
	}{
		{name: "unified_bank.json", hash: referenceBankSHA256, max: 1 << 20},
		{name: "shared_detector.bin", hash: referenceDetectorSHA256, max: 16 << 20},
	}
	for i := range files {
		file := &files[i]
		f, readErr := os.OpenFile(filepath.Join(dataDir, file.name), os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if readErr != nil {
			return nil, errors.New("fingerprint reference data unavailable")
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > file.max {
			_ = f.Close()
			return nil, errors.New("invalid fingerprint reference data")
		}
		file.data, readErr = io.ReadAll(io.LimitReader(f, file.max+1))
		closeErr := f.Close()
		if readErr != nil || closeErr != nil || int64(len(file.data)) > file.max {
			return nil, errors.New("fingerprint reference data unavailable")
		}
		digest := sha256.Sum256(file.data)
		if hex.EncodeToString(digest[:]) != file.hash {
			return nil, errors.New("fingerprint reference data checksum mismatch")
		}
	}
	var bank struct {
		BuiltAt string `json:"built_at"`
	}
	if json.Unmarshal(files[0].data, &bank) != nil || bank.BuiltAt == "" {
		return nil, errors.New("invalid fingerprint reference metadata")
	}
	snapshot, err := os.MkdirTemp("", "new-api-fingerprint-data-")
	if err != nil {
		return nil, errors.New("fingerprint reference data unavailable")
	}
	defer os.RemoveAll(snapshot)
	for _, file := range files {
		if os.WriteFile(filepath.Join(snapshot, file.name), file.data, 0o400) != nil {
			return nil, errors.New("fingerprint reference data unavailable")
		}
	}
	// Exactly one absolute local source selects the SDK's local-only branch.
	detector, err := lmfpd.Load(ctx, lmfpd.LoadOptions{Sources: []string{snapshot}})
	if err != nil || detector == nil {
		return nil, errors.New("fingerprint reference data unavailable")
	}
	return &Engine{detector: detector, version: bank.BuiltAt}, nil
}
