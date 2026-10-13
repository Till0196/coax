// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"bytes"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

// readEDCB decodes a written file the way EDCB reads one: UTF-8 behind the
// mark that says so, and the machine's own code page otherwise. A file this
// program wrote always carries the mark.
func readEDCB(t *testing.T, written []byte) string {
	t.Helper()
	if !bytes.HasPrefix(written, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal("the file does not open with the byte-order mark, so EDCB will read it as code page 932")
	}
	return string(bytes.TrimPrefix(written, []byte{0xef, 0xbb, 0xbf}))
}

// edcbLines is the lines of a written file, which has no heading.
func edcbLines(text string) [][]string {
	var lines [][]string
	for _, line := range strings.Split(text, "\r\n") {
		if line != "" {
			lines = append(lines, strings.Split(line, "\t"))
		}
	}
	return lines
}

// TestWriteChSet4WritesTheColumnsEDCBReads: twelve of them, in EDCB's order,
// tab-separated and with no heading. Getting one wrong shifts every column after
// it, and the file still loads.
func TestWriteChSet4WritesTheColumnsEDCBReads(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteChSet4(&out, SystemSatellite, []report.Result{
		bsResult("1318000", 16625,
			isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}),
	})
	if err != nil {
		t.Fatalf("WriteChSet4: %v", err)
	}
	lines := edcbLines(readEDCB(t, out.Bytes()))
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), lines)
	}
	// チャンネル名 サービス名 ネットワーク名 チューナ空間 物理チャンネル
	// ONID TSID SID サービスタイプ 部分受信か 一覧表示に使用するか リモコンID
	want := []string{
		"BS15 テスト　衛星", "テスト　衛星", "", "0", "0",
		"4", "16625", "101", "1", "0", "1", "0",
	}
	if !slicesEqual(lines[0], want) {
		t.Errorf("line = %q\nwant   %q", lines[0], want)
	}
}

// TestChSet4AgreesWithTheChannelDefinitionFile: EDCB's 「チューナ空間」 and
// 「物理チャンネル」 are what it hands to SetChannel, which is to say indexes
// into what the BonDriver enumerates -- the channel definition file it loaded,
// not a physical channel number despite the column's name.
func TestChSet4AgreesWithTheChannelDefinitionFile(t *testing.T) {
	results := []report.Result{
		terrestrialResult("27", isdb.ServiceInfo{ServiceID: 1990, Type: 0x01, Name: "サンプルテレビ"}),
	}
	var definitions, tuner bytes.Buffer
	if _, err := WriteChSet(&definitions, SystemTerrestrial, results); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	if _, err := WriteChSet4(&tuner, SystemTerrestrial, results); err != nil {
		t.Fatalf("WriteChSet4: %v", err)
	}
	line := edcbLines(readEDCB(t, tuner.Bytes()))[0]
	// The terrestrial file holds the whole plan, so 27 is the seventy-eighth
	// channel of space 0 rather than the first.
	if line[3] != "0" || line[4] != "77" {
		t.Errorf("space and channel = %s, %s; want 0, 77", line[3], line[4])
	}
	want := line[0] + "\t" + line[3] + "\t" + line[4] + "\t"
	if !strings.Contains(readChSet(t, definitions.Bytes()), want) {
		t.Errorf("%q does not name a channel the definition file enumerates", want)
	}
}

// TestChSet4WritesOnlyARealRemoteControlKey: the column holds one byte and means
// the identifier ARIB defines, so only what a broadcast actually said goes in
// it. A TVTest channel file is happy to put a three-digit service identifier in
// its own 「リモコン番号」 column -- right there, and here would truncate: 310
// becomes 54.
func TestChSet4WritesOnlyARealRemoteControlKey(t *testing.T) {
	for _, test := range []struct {
		what      string
		stream    carriedStream
		serviceID uint16
		want      byte
	}{
		{"a terrestrial network says so in its own table", carriedStream{networkID: 31760, remoteKeyID: 5}, 1990, 5},
		{"the advanced services say so service by service",
			carriedStream{networkID: 11, remoteKeys: map[uint16]byte{141: 4}}, 141, 4},
		{"the communications satellites say nothing, and their channel numbers are not keys",
			carriedStream{networkID: 6}, 310, 0},
		{"nor does BS, whose keys TVTest works out for itself", carriedStream{networkID: 4}, 101, 0},
	} {
		if got := remoteControlKeyID(test.stream, test.serviceID); got != test.want {
			t.Errorf("%s: got %d, want %d", test.what, got, test.want)
		}
	}
}

// TestWriteChSet5IsOneListForTheWholeScan: EDCB keeps one of these for the
// installation rather than one per tuner, and keys a service by its network, its
// stream and its identifier, so a service reached two ways -- off a dish and
// again off a cable that remodulates it -- is written once.
func TestWriteChSet5IsOneListForTheWholeScan(t *testing.T) {
	var out bytes.Buffer
	err := WriteChSet5(&out, report.Report{Results: []report.Result{
		bsResult("1318000", 16625,
			isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}),
		// The same service, remodulated onto a cable and scanned again.
		cableResult("C23", "225000000", nil, &isdb.TransportInfo{
			TransportStreamID: 16625, OriginalNetworkID: 4,
			Services: []isdb.ServiceInfo{{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}},
		}),
		terrestrialResult("27", isdb.ServiceInfo{ServiceID: 1990, Type: 0x01, Name: "サンプルテレビ"}),
	}})
	if err != nil {
		t.Fatalf("WriteChSet5: %v", err)
	}
	lines := edcbLines(readEDCB(t, out.Bytes()))
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want the two services: %q", len(lines), lines)
	}
	// サービス名 ネットワーク名 ONID TSID SID サービスタイプ 部分受信か
	// EPGデータ取得対象か デフォルト検索対象か
	for _, want := range [][]string{
		{"テスト　衛星", "", "4", "16625", "101", "1", "0", "1", "1"},
		{"サンプルテレビ", "", "31760", "31760", "1990", "1", "0", "1", "1"},
	} {
		found := false
		for _, line := range lines {
			if slicesEqual(line, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is not in the list; got %q", want, lines)
		}
	}
}

// TestEDCBFilesCarryTheNetworkItsOwnName, out of the network name descriptor of
// the stream's own table. The converter EDCB ships has to make one up from the
// channel name because a TVTest channel file has nowhere to carry it; a scan
// read the table and does not.
func TestEDCBFilesCarryTheNetworkItsOwnName(t *testing.T) {
	result := report.Result{
		Entry: channelconfig.Entry{
			Name:       "16625",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000"},
		},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			TransportStreamID: 16625, OriginalNetworkID: 4,
			Networks: []isdb.NetworkInfo{
				// A network table describes its neighbours too, and their
				// names are theirs.
				{NetworkID: 6, Name: "ＣＳデジタル"},
				{NetworkID: 4, Actual: true, Name: "ＢＳデジタル"},
			},
			Services: []isdb.ServiceInfo{{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}},
		}},
	}
	var list bytes.Buffer
	if err := WriteChSet5(&list, report.Report{Results: []report.Result{result}}); err != nil {
		t.Fatalf("WriteChSet5: %v", err)
	}
	if got := edcbLines(readEDCB(t, list.Bytes()))[0][1]; got != "ＢＳデジタル" {
		t.Errorf("network name = %q, want the one the stream's own network declared", got)
	}
}
