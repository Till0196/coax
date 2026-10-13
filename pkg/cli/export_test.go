// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"bytes"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/exporter"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

func TestExportMirakurunFromScanJSON(t *testing.T) {
	reportValue := report.Report{Results: []report.Result{{
		Entry:  channelconfig.Entry{Name: "45168", Route: "SAT"},
		Status: "found",
		Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{OriginalNetworkID: 0x000b, Services: []isdb.ServiceInfo{{
			ServiceID: 181,
			Type:      0xa5,
			Name:      "４Ｋ放送",
		}}}},
	}}}
	var input bytes.Buffer
	if err := exporter.WriteJSON(&input, reportValue); err != nil {
		t.Fatal(err)
	}
	decoded, err := readJSONReport(bytes.NewReader(input.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 || decoded.Results[0].Analysis.TLV.OriginalNetworkID != 0x000b {
		t.Fatalf("network identity was not preserved: %#v", decoded.Results)
	}
	var output, diagnostics bytes.Buffer
	if err := Run([]string{"export", "--input", "-", "--route", "SAT", "--service-id"}, &input, &output, &diagnostics); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"type: BS4K", "route: SAT", `channel: "45168"`, "serviceId: 181"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}
