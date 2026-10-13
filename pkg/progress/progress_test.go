// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package progress

import (
	"path/filepath"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/report"
)

func TestProgressRoundTripAndConfigurationFingerprint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.progress.json")
	entry := channelconfig.Entry{
		Name: "45168", Source: "sat.conf", Route: "SAT",
		Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1164560", "STREAM_ID": "45168"},
	}
	progress := New(path)
	result := report.Result{Entry: entry, Status: "found"}
	if err := progress.Record(result); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Results[Key(entry)]; got.Status != "found" {
		t.Fatalf("unexpected resumed result: %#v", got)
	}

	changed := entry
	changed.Properties = map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1164561", "STREAM_ID": "45168"}
	if Key(changed) == Key(entry) {
		t.Fatal("a changed frequency reused stale progress")
	}
}
