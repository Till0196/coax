// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package progress is what a scan leaves behind and what it shows while it runs.
// A scan of a whole band takes long enough to be interrupted, so what it has
// already done is written out as it goes and read back on the next run; the
// display is the line saying which channel is being tried and how it went.
package progress

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/report"
)

type State struct {
	Version   int                      `json:"version"`
	UpdatedAt time.Time                `json:"updated_at"`
	Results   map[string]report.Result `json:"results"`
	path      string
}

func New(path string) *State {
	return &State{Version: 1, Results: make(map[string]report.Result), path: path}
}

func Load(path string) (*State, error) {
	progress := New(path)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return progress, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, progress); err != nil {
		return nil, fmt.Errorf("parse progress %s: %w", path, err)
	}
	if progress.Version != 1 {
		return nil, fmt.Errorf("unsupported progress version %d", progress.Version)
	}
	if progress.Results == nil {
		progress.Results = make(map[string]report.Result)
	}
	progress.path = path
	return progress, nil
}

func (progress *State) Record(result report.Result) error {
	progress.Results[Key(result.Entry)] = result
	progress.UpdatedAt = time.Now()
	return progress.save()
}

func (progress *State) save() error {
	directory := filepath.Dir(progress.path)
	temporary, err := os.CreateTemp(directory, ".coax-progress-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	encoder := json.NewEncoder(temporary)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(progress); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, progress.path)
}

func Key(entry channelconfig.Entry) string {
	hash := sha256.New()
	// Include the analysis schema so results saved before native TLV/MMT
	// parsing was added are captured again instead of being resumed stale.
	hash.Write([]byte("coax-analysis-v1"))
	hash.Write([]byte{0})
	source, err := filepath.Abs(entry.Source)
	if err != nil {
		source = filepath.Clean(entry.Source)
	}
	hash.Write([]byte(filepath.Clean(source)))
	hash.Write([]byte{0})
	hash.Write([]byte(entry.Route))
	hash.Write([]byte{0})
	hash.Write([]byte(entry.Name))
	for _, key := range channelconfig.SortedPropertyKeys(entry.Properties) {
		hash.Write([]byte{0})
		hash.Write([]byte(strings.ToUpper(key)))
		hash.Write([]byte{'='})
		hash.Write([]byte(entry.Properties[key]))
	}
	return hex.EncodeToString(hash.Sum(nil))
}
