// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/isdb"
	"github.com/Till0196/coax/pkg/report"
)

func TestSatellitePTXChannelNamesTheTransponder(t *testing.T) {
	for frequency, want := range map[string]struct {
		ptxCh int
		name  string
	}{
		"1049480": {0, "BS01"},
		"1087840": {1, "BS03"},
		"1471440": {11, "BS23"},
		"1613000": {12, "ND02"},
		"1653000": {13, "ND04"},
		"2053000": {23, "ND24"},
		// Left-hand polarisation: the even BS transponders, interleaved half a
		// spacing between the odd ones. BS 14 is the one with anything on it,
		// and 2471820 kHz is where the channel file puts a TLV stream.
		"2241660": {24, "BS02"},
		"2471820": {30, "BS14"},
		"2663620": {35, "BS24"},
		// Left-hand CS: the odd ND numbers, half a spacing below the even
		// ones. Nothing is on them since 2024-03-31, but a scan of a feed that
		// still carries something must not be told there is no such channel.
		"2766000": {36, "ND01"},
		"3206000": {47, "ND23"},
	} {
		ptxCh, name, ok := satellitePTXChannel(SystemSatellite, frequency)
		if !ok || ptxCh != want.ptxCh || name != want.name {
			t.Errorf("satellitePTXChannel(S, %s) = %d %s, %t; want %d %s",
				frequency, ptxCh, name, ok, want.ptxCh, want.name)
		}
	}
	// ND 25 and ND 26 are in the same table and land outside the bands this
	// arithmetic covers, so they are reported rather than guessed at.
	for _, frequency := range []string{"", "1049481", "1049000", "473142857", "3246000", "2093000"} {
		if _, _, ok := satellitePTXChannel(SystemSatellite, frequency); ok {
			t.Errorf("satellitePTXChannel(S, %q) answered for a frequency that is no transponder", frequency)
		}
	}
	// The 124/128 degree service numbers its transponders its own way, and the two
	// satellites there number theirs the same -- 250 kHz apart, which is why the
	// satellite is part of the name. These are downlink frequencies, not
	// intermediate ones: the block has to be named there in any case, and its
	// oscillator is 11200 MHz rather than 10678 MHz.
	for frequency, want := range map[string]struct {
		ptxCh int
		name  string
	}{
		"12508000": {0, "JCSAT3A/JD1"},
		"12733000": {15, "JCSAT3A/JD16"},
		"12268000": {16, "JCSAT3A/JD17"},
		"12488000": {27, "JCSAT3A/JD28"},
		"12508250": {28, "JCSAT4B/JD1"},
		"12733250": {43, "JCSAT4B/JD16"},
	} {
		ptxCh, name, ok := satellitePTXChannel(SystemDVBS2, frequency)
		if !ok || ptxCh != want.ptxCh || name != want.name {
			t.Errorf("satellitePTXChannel(S2, %s) = %d %s, %t; want %d %s",
				frequency, ptxCh, name, ok, want.ptxCh, want.name)
		}
	}
	// The two plans are kept apart by system, not by frequency: a 110 degree
	// transponder must not be answered for out of the 124/128 degree table. The K
	// series is in the plan and is deliberately not numbered here: adding a run to
	// the middle would move every number after it.
	for _, test := range []struct{ system, frequency string }{
		{SystemSatellite, "12508000"},
		{SystemDVBS2, "1049480"},
		{SystemDVBS2, "2471820"},
		{SystemDVBS2, "1830000"},
		{SystemDVBS2, "12268250"},
	} {
		if _, name, ok := satellitePTXChannel(test.system, test.frequency); ok {
			t.Errorf("satellitePTXChannel(%s, %s) = %s; want no answer from the other plan",
				test.system, test.frequency, name)
		}
	}
}

func cableResult(name, frequency string, multiplex *isdb.TSMFInfo, transport *isdb.TransportInfo) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       name,
			Route:      "CATV",
			Properties: map[string]string{"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": frequency},
		},
		Status:   "found",
		Analysis: &isdb.Analysis{TSMF: multiplex, Transport: transport},
	}
}

// TestWriteChSetSeparatesWhatArrivedFromWhatItIs: BS reached over a cable
// transmodulation is BS and belongs in the BS space with the identifier that
// picks it out of the frame, while the cable operator's own carriage must not
// fall through to the satellite platform.
func TestWriteChSetSeparatesWhatArrivedFromWhatItIs(t *testing.T) {
	var out bytes.Buffer
	skipped, err := WriteChSet(&out, SystemCable, []report.Result{
		cableResult("13", "473000000", &isdb.TSMFInfo{RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 1, StreamID: 17488, OriginalNetworkID: 4, Format: "ts"},
		}}, nil),
		cableResult("C14", "117000000", &isdb.TSMFInfo{RelativeStreams: []isdb.RelativeStreamInfo{
			{RelativeStream: 10, StreamID: 45168, OriginalNetworkID: 11, Format: "tlv"},
		}}, nil),
		cableResult("C17", "135000000", nil, &isdb.TransportInfo{
			TransportStreamID: 65493, OriginalNetworkID: 65527,
		}),
	})
	if err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("nothing should have been skipped, got %v", skipped)
	}
	written := out.String()
	for _, want := range []string{
		"$BS\t0\r\n",
		"$BS4K\t1\r\n",
		"$CATV\t2\r\n",
		// The stream of a multiplex is named for the way in and carries the
		// identifier that picks it out.
		"13/TS1\t0\t0\t63\t17488\r\n",
		"C14/TS10\t1\t0\t4\t45168\r\n",
		// A carrier holding one stream has nothing to choose between.
		"C17Ch\t2\t0\t7\t0\r\n",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the file does not contain %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, "SKY") {
		t.Errorf("the cable operator's own carriage was filed under SKY:\n%s", written)
	}
}

// TestWriteChSetSaysWhatItCouldNotPlace. A file quietly missing a channel that
// was found looks exactly like a complete one.
func TestWriteChSetSaysWhatItCouldNotPlace(t *testing.T) {
	var out bytes.Buffer
	skipped, err := WriteChSet(&out, SystemCable, []report.Result{
		cableResult("13", "473000000", nil, &isdb.TransportInfo{
			TransportStreamID: 17488, OriginalNetworkID: 4,
		}),
		cableResult("C99", "999000000", nil, &isdb.TransportInfo{
			TransportStreamID: 1, OriginalNetworkID: 4,
		}),
	})
	if err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "C99") {
		t.Fatalf("the unplaceable channel was not reported, got %v", skipped)
	}
}

func satelliteResult(frequency string, tsid, networkID uint16) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       strconv.Itoa(int(tsid)),
			Route:      "SAT",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": frequency},
		},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			TransportStreamID: tsid, OriginalNetworkID: networkID,
		}},
	}
}

// TestWriteChSetNumbersTheStreamsOfATransponder: a satellite transponder is
// scanned one stream at a time and does not name its own, so the places are
// handed out here by identifier, so that a scan which lost one still names the
// rest as the driver's file does.
func TestWriteChSetNumbersTheStreamsOfATransponder(t *testing.T) {
	var out bytes.Buffer
	_, err := WriteChSet(&out, SystemSatellite, []report.Result{
		// Out of order on purpose, and BS 3's identifiers are not consecutive.
		satelliteResult("1087840", 17970, 4),
		satelliteResult("1087840", 16432, 4),
		satelliteResult("1087840", 17969, 4),
		// One stream on its own transponder: left-hand BS 14, which is where
		// the channel file puts a TLV stream.
		satelliteResult("2471820", 45280, 11),
	})
	if err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	written := out.String()
	for _, want := range []string{
		// Exactly the rows BonDriver_PX4 ships for this transponder.
		"BS03/TS0\t0\t0\t1\t16432\r\n",
		"BS03/TS1\t0\t1\t1\t17969\r\n",
		"BS03/TS2\t0\t2\t1\t17970\r\n",
		// A transponder carrying one stream is named by itself.
		"BS14\t1\t0\t30\t45280\r\n",
	} {
		if !strings.Contains(written, want) {
			t.Errorf("the file does not contain %q:\n%s", want, written)
		}
	}
	if strings.Contains(written, "BS14/TS") {
		t.Errorf("a transponder with one stream was given a place on itself:\n%s", written)
	}
}

func TestChSetSystemsAreTheFilesToWrite(t *testing.T) {
	value := report.Report{Results: []report.Result{
		{Entry: channelconfig.Entry{Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBT"}}, Status: "found"},
		{Entry: channelconfig.Entry{Properties: map[string]string{"DELIVERY_SYSTEM": "DVBC/ANNEX_A"}}, Status: "found"},
		// Not found, so not a file.
		{Entry: channelconfig.Entry{Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS"}}, Status: "no_lock"},
		// Not a system this writes.
		{Entry: channelconfig.Entry{Properties: map[string]string{"DELIVERY_SYSTEM": "DVBT2"}}, Status: "found"},
	}}
	got := ChSetSystems(value)
	if len(got) != 2 || got[0] != SystemTerrestrial || got[1] != SystemCable {
		t.Errorf("ChSetSystems() = %v, want [T C]", got)
	}
}

// TestTheAdvancedSatelliteIsFiledWithTheSatellites: a channel file may name
// ISDB-S3 outright rather than writing the TLV streams as ISDB-S with a TLV
// identifier, and both spellings describe one dish, one driver and one channel
// definition file.
func TestTheAdvancedSatelliteIsFiledWithTheSatellites(t *testing.T) {
	for _, name := range []string{"ISDBS3", "ISDB-S3", "isdb-s3"} {
		got, ok := chsetSystem(name)
		if !ok || got != SystemSatellite {
			t.Errorf("chsetSystem(%q) = %q, %v; want %q, true", name, got, ok, SystemSatellite)
		}
	}
}

// terrestrialResult is one channel over the air, carrying the services named.
func terrestrialResult(name string, services ...isdb.ServiceInfo) report.Result {
	return report.Result{
		Entry: channelconfig.Entry{
			Name:       name,
			Route:      "TER",
			Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBT"},
		},
		Status: "found",
		Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
			TransportStreamID: 31760, OriginalNetworkID: 31760, Services: services,
		}},
	}
}

// TestTheTerrestrialFileHoldsTheWholePlan: a channel definition file is a plan
// rather than a report of one scan, so that a channel the scan missed can still
// be tuned. BonDriver_PX4's own file lists all 113 channels with nothing said
// about any of them.
func TestTheTerrestrialFileHoldsTheWholePlan(t *testing.T) {
	var out bytes.Buffer
	if _, err := WriteChSet(&out, SystemTerrestrial, []report.Result{
		terrestrialResult("27", isdb.ServiceInfo{ServiceID: 1990, Type: 0x01, Name: "サンプルテレビ"}),
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	text := readChSet(t, out.Bytes())

	var channels []string
	for _, line := range strings.Split(text, "\r\n") {
		if line != "" && !strings.HasPrefix(line, ";") && !strings.HasPrefix(line, "$") {
			channels = append(channels, line)
		}
	}
	// VHF 1 to 12, the mid and super high bands C13 to C63, and UHF 13 to 62.
	if len(channels) != 113 {
		t.Fatalf("got %d channels, want the 113 of the plan", len(channels))
	}
	for _, want := range []string{
		// The plan is complete and in frequency order, so the channel
		// identifier and the physical channel are the same number throughout.
		"1Ch\t0\t0\t0\t0\r\n",
		"C13Ch\t0\t3\t3\t0\r\n",
		"C22Ch\t0\t12\t12\t0\r\n",
		"4Ch\t0\t13\t13\t0\r\n",
		"C63Ch\t0\t62\t62\t0\r\n",
		"13Ch\t0\t63\t63\t0\r\n",
		"62Ch\t0\t112\t112\t0\r\n",
		// And the one the scan found is named after the service on it, which
		// is what tells a channel with something on it from an empty one.
		"27Ch サンプルテレビ\t0\t77\t77\t0\r\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is not in the file", want)
		}
	}
}

// TestOnlyTheTerrestrialFileIsFilledOut: on a cable the same channels carry
// whatever the operator put there, and the space comes from the network the
// stream declares, which an empty channel does not.
func TestOnlyTheTerrestrialFileIsFilledOut(t *testing.T) {
	var out bytes.Buffer
	if _, err := WriteChSet(&out, SystemCable, []report.Result{
		cableResult("C23", "225000000", nil, &isdb.TransportInfo{
			TransportStreamID: 65001, OriginalNetworkID: 0xfffe,
			Services: []isdb.ServiceInfo{{ServiceID: 1, Type: 0x01, Name: "コミュニティ"}},
		}),
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	text := readChSet(t, out.Bytes())
	if strings.Contains(text, "C24Ch") {
		t.Errorf("the cable file was filled out with channels nothing was found on:\n%s", text)
	}
}

// TestAChannelIsNamedAfterTheServiceAtTheFrontOfIt: a transponder's own name
// says where it is and nothing about what is on it, so the service at the front
// is added -- the same rule the Mirakurun list is written under.
func TestAChannelIsNamedAfterTheServiceAtTheFrontOfIt(t *testing.T) {
	var out bytes.Buffer
	if _, err := WriteChSet(&out, SystemSatellite, []report.Result{
		{
			Entry: channelconfig.Entry{
				Name:       "16400",
				Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"},
			},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				TransportStreamID: 16400, OriginalNetworkID: 4,
				Services: []isdb.ServiceInfo{
					{ServiceID: 151, Type: 0x01, Name: "サンプル衛星"},
					{ServiceID: 152, Type: 0x01, Name: "サンプル衛星２"},
				},
			}},
		},
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	if want := "BS01 サンプル衛星\t0\t0\t0\t16400\r\n"; !strings.Contains(readChSet(t, out.Bytes()), want) {
		t.Errorf("%q is not in the file:\n%s", want, readChSet(t, out.Bytes()))
	}
}

// TestAChannelKeepsItsOwnNameWhenTheServiceWillNotFitTheCoding: this format has
// no second encoding to fall back to, so a service name carrying an ARIB symbol
// is left off rather than mangled.
func TestAChannelKeepsItsOwnNameWhenTheServiceWillNotFitTheCoding(t *testing.T) {
	var out bytes.Buffer
	if _, err := WriteChSet(&out, SystemTerrestrial, []report.Result{
		terrestrialResult("27", isdb.ServiceInfo{ServiceID: 1990, Type: 0x01, Name: "テレビ🈔"}),
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	text := readChSet(t, out.Bytes())
	if !strings.Contains(text, "27Ch\t0\t77\t77\t0\r\n") {
		t.Errorf("the channel did not keep its own name:\n%s", text)
	}
}

// TestTheSpacesSoldBySlotAreNamedByTheirCarrierAlone: on the communications
// satellites and on a cable operator's own carriage, one transponder or
// multiplexing frame holds unrelated channels bought by the slot, so whichever
// is first stands for none of the others -- nor stays first.
func TestTheSpacesSoldBySlotAreNamedByTheirCarrierAlone(t *testing.T) {
	var satellite bytes.Buffer
	if _, err := WriteChSet(&satellite, SystemSatellite, []report.Result{
		// ND 2, and BS 1 beside it to show that the rule is the space's.
		csResult("1613000", 24608, 6,
			isdb.ServiceInfo{ServiceID: 237, Type: 0x01, Name: "テストチャンネル１"}),
		{
			Entry: channelconfig.Entry{
				Name:       "16400",
				Properties: map[string]string{"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480"},
			},
			Status: "found",
			Analysis: &isdb.Analysis{Transport: &isdb.TransportInfo{
				TransportStreamID: 16400, OriginalNetworkID: 4,
				Services: []isdb.ServiceInfo{{ServiceID: 151, Type: 0x01, Name: "サンプル衛星"}},
			}},
		},
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	text := readChSet(t, satellite.Bytes())
	if !strings.Contains(text, "ND02\t1\t0\t12\t24608\r\n") {
		t.Errorf("a CS transponder was named after one of its services:\n%s", text)
	}
	if !strings.Contains(text, "BS01 サンプル衛星\t0\t0\t0\t16400\r\n") {
		t.Errorf("BS lost the service at the front of it:\n%s", text)
	}

	var cable bytes.Buffer
	if _, err := WriteChSet(&cable, SystemCable, []report.Result{
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
	}); err != nil {
		t.Fatalf("WriteChSet: %v", err)
	}
	text = readChSet(t, cable.Bytes())
	if !strings.Contains(text, "C23/TS1\t1\t0\t22\t65001\r\n") {
		t.Errorf("a cable operator's own carriage was named after one of its services:\n%s", text)
	}
	// The BS remodulated onto the same carrier is still BS, and is named as
	// BS is.
	if !strings.Contains(text, "C23/TS2 テスト　衛星\t0\t0\t22\t16625\r\n") {
		t.Errorf("the remodulated BS lost the service at the front of it:\n%s", text)
	}
}
