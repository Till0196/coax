// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"bytes"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
	"strconv"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

// bsResult is one BS transponder carrying the services named.
func bsResult(frequencyKHz string, tsid uint16, services ...isdb.ServiceInfo) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       strconv.Itoa(int(tsid)),
			Route:      "SAT",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": frequencyKHz},
		},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			TransportStreamID: tsid, OriginalNetworkID: 4, Services: services,
		}},
	}
}

// readCh2 decodes a written file the way TVTest reads one: a byte-order mark
// means UTF-16, and anything else is code page 932.
func readCh2(t *testing.T, written []byte) (text string, utf16 bool) {
	t.Helper()
	if len(written) >= 2 && written[0] == 0xff && written[1] == 0xfe {
		decoded, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(written)
		if err != nil {
			t.Fatalf("decode as UTF-16: %v", err)
		}
		return string(decoded), true
	}
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(written)
	if err != nil {
		t.Fatalf("decode as code page 932: %v", err)
	}
	return string(decoded), false
}

// readChSet decodes a channel definition file the way the driver reads one:
// code page 932, always, with no other case.
func readChSet(t *testing.T, written []byte) string {
	t.Helper()
	decoded, err := japanese.ShiftJIS.NewDecoder().Bytes(written)
	if err != nil {
		t.Fatalf("decode as code page 932: %v", err)
	}
	return string(decoded)
}

// ch2Lines is the channel lines of a written file, without the comments.
func ch2Lines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\r\n") {
		if line != "" && !strings.HasPrefix(line, ";") {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestWriteCh2WritesWhatTVTestReads checks the heading, the space declaration
// and the field order against what TVTest writes: the space and the channel say
// which carrier the driver is to tune, the four identifiers which service on
// it.
func TestWriteCh2WritesWhatTVTestReads(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemSatellite, []report.Result{
		bsResult("1318000", 16625,
			isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"},
			isdb.ServiceInfo{ServiceID: 102, Type: 0x01, Name: "テスト　衛星２"}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, utf16 := readCh2(t, out.Bytes())
	if utf16 {
		t.Error("a file of names that fit in code page 932 was written as UTF-16")
	}
	if !strings.HasPrefix(text, "; TVTest チャンネル設定ファイル\r\n; 名称,チューニング空間,チャンネル,リモコン番号,サービスタイプ,サービスID,ネットワークID,TSID,状態\r\n") {
		t.Errorf("the heading is not the one TVTest writes:\n%s", text)
	}
	if !strings.Contains(text, ";#SPACE(0,BS)\r\n") {
		t.Errorf("the space was not declared:\n%s", text)
	}
	want := []string{
		"テスト　衛星,0,0,1,1,101,4,16625,1",
		"テスト　衛星２,0,0,2,1,102,4,16625,1",
	}
	if got := ch2Lines(text); !slicesEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// TestCh2AgreesWithTheChannelDefinitionFile checks that the space and channel a
// .ch2 names index the same carrier the ChSet.txt enumerates. Written from
// different orderings the numbers are all still valid and all name the wrong
// carrier.
func TestCh2AgreesWithTheChannelDefinitionFile(t *testing.T) {
	results := []report.Result{
		bsResult("1394720", 16433, isdb.ServiceInfo{ServiceID: 191, Type: 0x01, Name: "サンプル有料"}),
		bsResult("1318000", 16625, isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}),
		bsResult("1049480", 16400, isdb.ServiceInfo{ServiceID: 291, Type: 0x01, Name: "テスト　衛星８Ｋ"}),
	}
	var definitions, channels bytes.Buffer
	if _, err := WriteChSet(&definitions, SystemSatellite, results); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	if _, err := WriteCh2(&channels, SystemSatellite, results); err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}

	// What the driver would enumerate: space id and channel id, per line of
	// the definition file, in file order.
	enumerated := map[string]string{}
	for _, line := range strings.Split(readChSet(t, definitions.Bytes()), "\r\n") {
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "$") {
			continue
		}
		columns := strings.Split(line, "\t")
		if len(columns) != 5 {
			t.Fatalf("a definition line has %d columns: %q", len(columns), line)
		}
		enumerated[columns[1]+","+columns[2]] = columns[0]
	}

	text, _ := readCh2(t, channels.Bytes())
	lines := ch2Lines(text)
	if len(lines) != len(results) {
		t.Fatalf("got %d channel lines, want %d", len(lines), len(results))
	}
	// Every line must name a space and channel the definition file enumerates,
	// and that carrier must be a BS one.
	for _, line := range lines {
		fields := strings.Split(line, ",")
		carrier, known := enumerated[fields[1]+","+fields[2]]
		if !known {
			t.Errorf("%q names space %s channel %s, which the definition file does not enumerate",
				fields[0], fields[1], fields[2])
			continue
		}
		if !strings.HasPrefix(carrier, "BS") {
			t.Errorf("%q was placed on %q", fields[0], carrier)
		}
	}
	// And specifically: the lowest transponder is channel 0 in both. The
	// definition file names it after the service at the front of it as well,
	// so the carrier is the first word of it.
	if carrier := enumerated["0,0"]; !strings.HasPrefix(carrier, "BS01 ") {
		t.Errorf("channel 0 of space 0 is %q, want the one on BS01", carrier)
	}
	if !strings.HasPrefix(lines[0], "テスト　衛星８Ｋ,0,0,") {
		t.Errorf("the first channel line is %q, want the service on BS01", lines[0])
	}
}

// TestCh2RemoteControlKeyIsTheButtonTheServiceIsOn covers what a stream says
// outright against what has to be worked out. A terrestrial network says which
// button it is on and is believed; the older satellite platforms say nothing,
// so the number comes from the service identifier by the same arithmetic TVTest
// applies.
func TestCh2RemoteControlKeyIsTheButtonTheServiceIsOn(t *testing.T) {
	for _, test := range []struct {
		what      string
		stream    carriedStream
		serviceID uint16
		want      int
	}{
		{"a terrestrial network says so itself", carriedStream{networkID: 31760, remoteKeyID: 5}, 1990, 5},
		{"and is believed over any arithmetic", carriedStream{networkID: 4, remoteKeyID: 9}, 101, 9},
		{"a BS button is the identifier less a hundred", carriedStream{networkID: 4}, 101, 1},
		{"and so is BS 3", carriedStream{networkID: 4}, 103, 3},
		{"the rest of BS divides by ten as well", carriedStream{networkID: 4}, 141, 4},
		{"BS 11", carriedStream{networkID: 4}, 211, 11},
		{"BS 12", carriedStream{networkID: 4}, 229, 12},
		{"the 124/128 degrees east DVB-S2 platform counts from its own base", carriedStream{networkID: 10}, 32769, 1},
		{"a communications satellite service has no button", carriedStream{networkID: 6}, 55391, 0},
		{"nor has a terrestrial one whose network did not say", carriedStream{networkID: 31760}, 1990, 0},
	} {
		if got := remoteControlKey(test.stream, test.serviceID); got != test.want {
			t.Errorf("%s: service %d on network %d = %d, want %d",
				test.what, test.serviceID, test.stream.networkID, got, test.want)
		}
	}
}

// TestCh2FallsBackToUTF16ForNamesCodePage932HasNoRoomFor checks the encoding
// choice. Service names carry ARIB symbols code page 932 has no character for;
// UTF-8 is not an option, since TVTest reads a file with no byte-order mark as
// code page 932, and dropping the character would rename the service.
func TestCh2FallsBackToUTF16ForNamesCodePage932HasNoRoomFor(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemSatellite, []report.Result{
		bsResult("1318000", 16625,
			isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "テスト🈔"}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, utf16 := readCh2(t, out.Bytes())
	if !utf16 {
		t.Fatal("a name code page 932 cannot hold was written as code page 932")
	}
	if !strings.Contains(text, "テスト🈔,0,0,1,1,101,4,16625,1") {
		t.Errorf("the name did not survive:\n%s", text)
	}
}

// TestCh2QuotesNamesThatWouldBeReadAsSomethingElse. A comma ends a field and a
// leading semicolon starts a comment, so a service named with one has to be
// quoted or the line means something other than the service.
func TestCh2QuotesNamesThatWouldBeReadAsSomethingElse(t *testing.T) {
	for name, want := range map[string]string{
		"テスト":        "テスト",
		"A, B":       `"A, B"`,
		";comment":   `";comment"`,
		"#hash":      `"#hash"`,
		`say "this"`: `"say ""this"""`,
	} {
		if got := ch2Name(name); got != want {
			t.Errorf("ch2Name(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestCh2LeavesAServiceNobodyMeantToWatchSwitchedOff checks the state column. A
// temporary video service belongs in the file so somebody can switch it on, and
// not in the channel list by default. That is the division TVTest's own scan
// makes.
func TestCh2LeavesAServiceNobodyMeantToWatchSwitchedOff(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemSatellite, []report.Result{
		bsResult("1318000", 16625,
			isdb.ServiceInfo{ServiceID: 101, Type: 0x01, Name: "ふつう"},
			isdb.ServiceInfo{ServiceID: 102, Type: 0xa1, Name: "りんじ"}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, _ := readCh2(t, out.Bytes())
	lines := ch2Lines(text)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want both services: %q", len(lines), lines)
	}
	if !strings.HasSuffix(lines[0], ",1") {
		t.Errorf("the television service is switched off: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], ",0") {
		t.Errorf("the temporary service is switched on: %q", lines[1])
	}
}

// TestCh2SaysSoWhenTheScanNamedNoService checks that a carrier-only scan is an
// error rather than an empty file: one with no channel lines loads and leaves
// TVTest with nothing. Such a scan can still produce a channel definition file.
func TestCh2SaysSoWhenTheScanNamedNoService(t *testing.T) {
	_, err := WriteCh2(&bytes.Buffer{}, SystemSatellite, []report.Result{
		bsResult("1318000", 16625),
	})
	if err == nil {
		t.Fatal("a file with no services was written without complaint")
	}
	if !strings.Contains(err.Error(), "service") {
		t.Errorf("the error does not say what was missing: %v", err)
	}
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// tlvResult is one advanced-service stream, as a scan of a satellite
// transponder reports it.
func tlvResult(frequencyKHz string, streamID, networkID uint16,
	keys []isdb.RemoteControlKeyInfo, services ...isdb.ServiceInfo,
) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       strconv.Itoa(int(streamID)),
			Route:      "SAT",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": frequencyKHz},
		},
		Status: "found",
		Analysis: &isdb.Analysis{TLV: &isdb.TLVInfo{
			TLVStreamID: streamID, OriginalNetworkID: networkID,
			Services: services, RemoteControlKeys: keys,
		}},
	}
}

// TestTheServicesLeftSwitchedOn pins which service types are written enabled.
//
// It is a table rather than a fixture because the one type that most needs
// pinning cannot be reached through the fixtures: 0xAD, the 4K-only television
// service of ARIB STD-B10, is a transport stream's service type. It appears
// where such a service is carried in an ISDB-S multiplex, not on the advanced services -- those
// arrive as TLV, and the MH-SDT of ARIB STD-B60 has no such value.
func TestTheServicesLeftSwitchedOn(t *testing.T) {
	for _, one := range []struct {
		name        string
		serviceType byte
		want        bool
	}{
		{"television", 0x01, true},
		{"promotional video, which a viewer means to watch", 0xa5, true},
		{"the 4K-only television service of a transport stream", 0xad, true},
		// In the file and switched off, so it can be switched on rather than
		// hunted for.
		{"a temporary video service", 0xa1, false},
		{"data", 0xc0, false},
		{"a table that said nothing", 0x00, false},
	} {
		t.Run(one.name, func(t *testing.T) {
			if got := enabledServiceType(one.serviceType); got != one.want {
				t.Errorf("enabledServiceType(%#02x) = %v, want %v", one.serviceType, got, one.want)
			}
		})
	}
}

// TestCh2WritesTheAdvancedServices checks the TLV path. An MMT/TLV service
// arrives as TLV rather than as a transport stream, and is the one broadcast
// that says which button it is on service by service. Both have to reach the
// file: the services, out of a table nothing else here reads, and the buttons.
func TestCh2WritesTheAdvancedServices(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemSatellite, []report.Result{
		// BS 14, left-hand circular, a TLV stream.
		tlvResult("2471820", 45162, 0x000b,
			[]isdb.RemoteControlKeyInfo{
				{RemoteControlKeyID: 1, ServiceID: 401},
				{RemoteControlKeyID: 4, ServiceID: 141},
			},
			isdb.ServiceInfo{ServiceID: 401, Type: 0x01, Name: "テスト　衛星４Ｋ"},
			isdb.ServiceInfo{ServiceID: 141, Type: 0x01, Name: "サンプル　衛星４Ｋ"}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, _ := readCh2(t, out.Bytes())
	if !strings.Contains(text, ";#SPACE(0,BS4K)\r\n") {
		t.Errorf("the advanced services were not filed under their own space:\n%s", text)
	}
	want := []string{
		// The button is the descriptor's, and the identifier in the TSID column is
		// the TLV stream's. The service type is the plain television one: what makes
		// these the advanced services is the network they are on, not their type.
		"テスト　衛星４Ｋ,0,0,1,1,401,11,45162,1",
		"サンプル　衛星４Ｋ,0,0,4,1,141,11,45162,1",
	}
	if got := ch2Lines(text); !slicesEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// TestCh2WritesACableFeedAsWhatEachStreamIs checks that each stream of one
// multiplexing frame lands in the space its own network says: an operator's own
// carriage under an unpublished identifier beside a satellite network
// remodulated onto the cable.
//
// Remodulated rather than passed through: what is received here is ISDB-C, so
// the satellite signal was demodulated and modulated again as J.83 to get onto
// this carrier.
func TestCh2WritesACableFeedAsWhatEachStreamIs(t *testing.T) {
	results := []report.Result{
		cableResult("C23", "225000000", &isdb.TSMFInfo{
			RelativeStreams: []isdb.RelativeStreamInfo{
				{
					RelativeStream: 1, StreamID: 65001, OriginalNetworkID: 0xfffe, Format: "ts", SlotCount: 26,
					Transport: &isdb.TransportInfo{
						TransportStreamID: 65001, OriginalNetworkID: 0xfffe,
						Services: []isdb.ServiceInfo{{ServiceID: 1, Type: 0x01, Name: "コミュニティ"}},
					},
				},
				{
					RelativeStream: 2, StreamID: 16625, OriginalNetworkID: 4, Format: "ts", SlotCount: 26,
					Transport: &isdb.TransportInfo{
						TransportStreamID: 16625, OriginalNetworkID: 4,
						Services: []isdb.ServiceInfo{{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}},
					},
				},
			},
		}, nil),
	}
	var out bytes.Buffer
	if _, err := WriteCh2(&out, SystemCable, results); err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, _ := readCh2(t, out.Bytes())
	want := []string{
		// The remodulated BS is filed as BS, because that is the network it declares
		// however it got here; the operator's own carriage has no published network
		// and is filed as CATV. The TSID is each stream's own.
		"テスト　衛星,0,0,1,1,101,4,16625,1",
		"コミュニティ,1,0,1,1,1,65534,65001,1",
	}
	if got := ch2Lines(text); !slicesEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// TestCh2WritesTheDVBS2PlatformBothWaysItArrives checks that the 124/128 degree
// east platform writes the same services whether it arrives off a dish as
// DVB-S2 or off an optical distribution: different delivery systems and
// different files, same network, same identifiers, same buttons.
func TestCh2WritesTheDVBS2PlatformBothWaysItArrives(t *testing.T) {
	service := isdb.ServiceInfo{ServiceID: 32769, Type: 0x01, Name: "テストチャンネル１"}
	transport := &isdb.TransportInfo{
		TransportStreamID: 1, OriginalNetworkID: 0x000a,
		Services: []isdb.ServiceInfo{service},
	}
	for _, test := range []struct {
		what   string
		system string
		result report.Result
	}{
		{
			what: "off a dish", system: SystemDVBS2,
			result: report.Result{
				Entry: channelconfig.Entry{
					Name:  "JCSAT3A/JD1",
					Route: "SAT",
					Properties: map[string]string{
						"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "12508000", "LNB": "JCSAT3A",
					},
				},
				Status: "found", Analysis: &isdb.Analysis{Transport: transport},
			},
		},
		{
			what: "off the optical distribution", system: SystemHikari,
			result: report.Result{
				Entry: channelconfig.Entry{
					Name:  "333750",
					Route: "HIKARI",
					Properties: map[string]string{
						"DELIVERY_SYSTEM": "DVBC/ANNEX_B", "FREQUENCY": "333750000",
					},
				},
				Status: "found", Analysis: &isdb.Analysis{Transport: transport},
			},
		},
	} {
		t.Run(test.what, func(t *testing.T) {
			var out bytes.Buffer
			if _, err := WriteCh2(&out, test.system, []report.Result{test.result}); err != nil {
				t.Fatalf("WriteCh2: %v", err)
			}
			text, _ := readCh2(t, out.Bytes())
			if !strings.Contains(text, ";#SPACE(0,SKY)\r\n") {
				t.Errorf("the platform was not filed under SKY:\n%s", text)
			}
			// Button 1, which is 32769 less the base the platform numbers from.
			want := []string{"テストチャンネル１,0,0,1,1,32769,10,1,1"}
			if got := ch2Lines(text); !slicesEqual(got, want) {
				t.Errorf("lines = %q, want %q", got, want)
			}
		})
	}
}

// TestCh2LeavesASpaceWithNoServiceUndeclared checks that a placed carrier which
// named no service declares no space, and that the spaces which did keep their
// channel-definition-file numbers -- a space left out leaves a gap rather than
// shifting anything.
func TestCh2LeavesASpaceWithNoServiceUndeclared(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemCable, []report.Result{
		cableResult("C23", "225000000", nil, &isdb.TransportInfo{
			TransportStreamID: 65001, OriginalNetworkID: 0xfffe,
		}),
		cableResult("C24", "231000000", nil, &isdb.TransportInfo{
			TransportStreamID: 16625, OriginalNetworkID: 4,
			Services: []isdb.ServiceInfo{{ServiceID: 101, Type: 0x01, Name: "テスト　衛星"}},
		}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, _ := readCh2(t, out.Bytes())
	if strings.Contains(text, "CATV") {
		t.Errorf("a space with no service was declared:\n%s", text)
	}
	// BS is space 0 in the channel definition file and stays space 0 here.
	if !strings.Contains(text, ";#SPACE(0,BS)\r\n") {
		t.Errorf("the space that does have services lost its number:\n%s", text)
	}
}

// csResult is one 110 degree CS transport stream.
func csResult(frequencyKHz string, tsid, networkID uint16, services ...isdb.ServiceInfo) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       strconv.Itoa(int(tsid)),
			Route:      "SAT",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": frequencyKHz},
		},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			TransportStreamID: tsid, OriginalNetworkID: networkID, Services: services,
		}},
	}
}

// TestCh2WritesTheCommunicationsSatellites checks that the 110 degree CS
// platform's two networks -- 6 and 7, which is how its transponders were sold
// -- land in one channel space. Its services are numbered by the channel number
// people quote, so the button falls out of the identifier with no arithmetic.
func TestCh2WritesTheCommunicationsSatellites(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteCh2(&out, SystemSatellite, []report.Result{
		// ND 2, carrying two transport streams.
		csResult("1613000", 24608, 6,
			isdb.ServiceInfo{ServiceID: 237, Type: 0x01, Name: "テストチャンネル１"}),
		csResult("1653000", 28736, 7,
			isdb.ServiceInfo{ServiceID: 161, Type: 0x01, Name: "サンプル！ドラマＴＶ"}),
		// Left-hand circular, which the plan still covers although nothing has
		// been on it since 2024-03-31.
		csResult("2806000", 29056, 7,
			isdb.ServiceInfo{ServiceID: 900, Type: 0x01, Name: "左旋"}),
	})
	if err != nil {
		t.Fatalf("WriteCh2: %v", err)
	}
	text, _ := readCh2(t, out.Bytes())
	if strings.Count(text, ";#SPACE(") != 1 || !strings.Contains(text, ";#SPACE(0,CS)\r\n") {
		t.Errorf("the two networks did not land in one space:\n%s", text)
	}
	want := []string{
		"テストチャンネル１,0,0,237,1,237,6,24608,1",
		"サンプル！ドラマＴＶ,0,1,161,1,161,7,28736,1",
		"左旋,0,2,900,1,900,7,29056,1",
	}
	if got := ch2Lines(text); !slicesEqual(got, want) {
		t.Errorf("lines = %q, want %q", got, want)
	}
}

// TestStreamsOfOneTransponderAreNumberedTogetherAcrossSpaces checks that a CS
// transponder carrying the older services and a TLV one -- which go in different
// channel spaces -- still numbers their places across all of them. A place is a
// property of the transponder, not of the space.
func TestStreamsOfOneTransponderAreNumberedTogetherAcrossSpaces(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteChSet(&out, SystemSatellite, []report.Result{
		csResult("1653000", 28736, 7,
			isdb.ServiceInfo{ServiceID: 161, Type: 0x01, Name: "２Ｋ"}),
		tlvResult("1653000", 49193, 0x000c, nil,
			isdb.ServiceInfo{ServiceID: 601, Type: 0x01, Name: "４Ｋ"}),
	})
	if err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	for _, want := range []string{
		// The CS space is named by the carrier alone; see carrierOnlyNames.
		"ND04/TS0\t0\t0\t13\t28736\r\n",
		"ND04/TS1\t1\t0\t13\t49193\r\n",
	} {
		if written := readChSet(t, out.Bytes()); !strings.Contains(written, want) {
			t.Errorf("%q is not in the file:\n%s", want, written)
		}
	}
}
