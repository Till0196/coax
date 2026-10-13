// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package report is the shape a scan's findings are written in.
//
// It is types and nothing else, so that the scanner which fills them and the
// exporters which read them share a definition rather than a convention.
package report

import (
	"time"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
)

type Result struct {
	channelconfig.Entry
	Status         string   `json:"status"`
	ReceiverStatus []string `json:"receiver_status,omitempty"`
	StreamFormat   string   `json:"stream_format,omitempty"`
	BytesRead      int      `json:"bytes_read,omitempty"`
	// ThroughputMbps is how fast the sample arrived, which is a measurement of
	// the capture and not of the channel. The channel's own rate, where it
	// could be read from the sender's clock, is in Analysis.
	ThroughputMbps *float64       `json:"throughput_mbps,omitempty"`
	Analysis       *isdb.Analysis `json:"analysis,omitempty"`
	ElapsedMS      int64          `json:"elapsed_ms"`
	Error          string         `json:"error,omitempty"`
	// Device is the tuner this channel was tried on. A scan with no tuner
	// named runs over every one the daemon lists, so which tuner a result came
	// off is a property of the result rather than of the report.
	Device string `json:"device,omitempty"`
}

type Report struct {
	Backend string    `json:"backend"`
	Device  string    `json:"device"`
	Started time.Time `json:"started_at"`
	Results []Result  `json:"results"`
}
