// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package exporter

import (
	"bytes"
	"strings"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/report"
)

// dvbv5Result is one channel a scan found on a lead, with the properties it
// was tuned with.
func dvbv5Result(name, route string, properties map[string]string) report.Result {
	return report.Result{
		Entry:  channelconfig.Entry{Name: name, Route: route, Properties: properties},
		Status: "found",
	}
}

func writeDVBv5(t *testing.T, route string, results []report.Result, options DVBv5Options) (file string, notes, skipped []string) {
	t.Helper()
	var out bytes.Buffer
	notes, skipped, err := WriteDVBv5(&out, route, results, options)
	if err != nil {
		t.Fatalf("WriteDVBv5(%q): %v", route, err)
	}
	return out.String(), notes, skipped
}

// skyResult is one transponder of the 124/128 degree east plan, whose channel
// files are written as downlink frequencies.
func skyResult(name string, properties map[string]string) report.Result {
	properties["DELIVERY_SYSTEM"] = "DVBS2"
	return dvbv5Result(name, "SAT", properties)
}

// sectionsOf is the section names of a written file, in the order they appear.
func sectionsOf(file string) []string {
	var names []string
	for _, line := range strings.Split(file, "\n") {
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			names = append(names, line[1:len(line)-1])
		}
	}
	return names
}

// TestTheFileIsTheOneItWasGiven: a DVBv5 channel file is the same shape a scan
// reads, and the properties a channel locked with are written back as they were
// given rather than tidied.
func TestTheFileIsTheOneItWasGiven(t *testing.T) {
	file, _, skipped := writeDVBv5(t, "SAT", []report.Result{
		dvbv5Result("16625", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
	}, DVBv5Options{})
	if len(skipped) != 0 {
		t.Errorf("skipped %v", skipped)
	}
	want := "[16625]\n\tDELIVERY_SYSTEM = ISDBS\n\tFREQUENCY = 1318000\n\tSTREAM_ID = 16625\n"
	if !strings.Contains(file, want) {
		t.Errorf("the file does not contain\n%s\ngot\n%s", want, file)
	}
}

// TestOnlyWhatAnsweredIsWritten. The scan's own JSON keeps every candidate;
// a channel file is a list of channels that are there.
func TestOnlyWhatAnsweredIsWritten(t *testing.T) {
	results := []report.Result{
		dvbv5Result("16625", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
		{
			Entry: channelconfig.Entry{Name: "16626", Route: "SAT", Properties: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16626",
			}},
			Status: "no_lock",
		},
	}
	file, _, _ := writeDVBv5(t, "SAT", results, DVBv5Options{})
	if got := sectionsOf(file); len(got) != 1 || got[0] != "16625" {
		t.Errorf("sections are %v, want [16625]", got)
	}
}

// TestSectionsAreInTuningOrder. Settled by the channels rather than by the
// order they were scanned in, so two scans of one feed write one file.
func TestSectionsAreInTuningOrder(t *testing.T) {
	results := []report.Result{
		dvbv5Result("17969", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1087840", "STREAM_ID": "17969",
		}),
		dvbv5Result("16400", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16400",
		}),
		dvbv5Result("16432", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1087840", "STREAM_ID": "16432",
		}),
	}
	file, _, _ := writeDVBv5(t, "SAT", results, DVBv5Options{})
	got := sectionsOf(file)
	want := []string{"16400", "16432", "17969"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections are %v, want %v", got, want)
	}
}

// TestASatelliteChannelCanBeNamedAsItsTransponder: the section name is a label
// -- the stream is selected by STREAM_ID either way -- but it is the label a
// Mirakurun channel list has to match, so it is the same choice the Mirakurun
// writer offers.
func TestASatelliteChannelCanBeNamedAsItsTransponder(t *testing.T) {
	results := []report.Result{
		dvbv5Result("16625", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
		dvbv5Result("16626", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16626",
		}),
	}
	file, notes, skipped := writeDVBv5(t, "SAT", results, DVBv5Options{TransponderNames: true})
	if len(skipped) != 0 || len(notes) != 0 {
		t.Errorf("skipped %v, noted %v", skipped, notes)
	}
	got := sectionsOf(file)
	want := []string{"BS15_1", "BS15_2"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sections are %v, want %v", got, want)
	}
	// The name is only a name: what selects the stream is still the identifier.
	if !strings.Contains(file, "[BS15_1]\n\tDELIVERY_SYSTEM = ISDBS\n\tFREQUENCY = 1318000\n\tSTREAM_ID = 16625\n") {
		t.Errorf("the renamed section lost its stream identifier:\n%s", file)
	}
}

// TestATLVStreamKeepsItsIdentifier: a place on a transponder names the older
// service sharing it, so the TLV streams are left as the scan named them
// and the reason is reported.
func TestATLVStreamKeepsItsIdentifier(t *testing.T) {
	file, notes, skipped := writeDVBv5(t, "SAT", []report.Result{
		dvbv5Result("45168", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "45168",
		}),
	}, DVBv5Options{TransponderNames: true})
	// It is in the file, under the name the scan gave it -- so it is reported
	// as a channel that was not renamed rather than as one left out.
	if got := sectionsOf(file); len(got) != 1 || got[0] != "45168" {
		t.Errorf("sections are %v, want [45168]", got)
	}
	if len(skipped) != 0 {
		t.Errorf("it was written, so nothing should say it was not: %v", skipped)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "45168") {
		t.Errorf("nothing said why it was left alone: %v", notes)
	}
}

// TestTheTerrestrialFileHoldsTheWholePlanToo, for the same reason the channel
// definition file is written as a plan: the file distributed for Japan lists all
// 113 channels of the plan.
func TestTheTerrestrialFileHoldsTheWholePlanToo(t *testing.T) {
	file, _, _ := writeDVBv5(t, "TER", []report.Result{
		dvbv5Result("27", "TER", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857", "BANDWIDTH_HZ": "6000000",
		}),
	}, DVBv5Options{})
	got := sectionsOf(file)
	if len(got) != 113 {
		t.Errorf("the file has %d sections, want the 113 of the plan", len(got))
	}
	for _, want := range []string{
		"[1]\n\tDELIVERY_SYSTEM = ISDBT\n\tFREQUENCY = 93142857\n\tBANDWIDTH_HZ = 6000000\n",
		"[27]\n\tDELIVERY_SYSTEM = ISDBT\n\tFREQUENCY = 557142857\n\tBANDWIDTH_HZ = 6000000\n",
		"[C63]\n\tDELIVERY_SYSTEM = ISDBT\n\tFREQUENCY = 465142857\n\tBANDWIDTH_HZ = 6000000\n",
	} {
		if !strings.Contains(file, want) {
			t.Errorf("the file does not contain\n%s\ngot\n%s", want, file)
		}
	}
	// The scanned channel is written once, not once as itself and once as an
	// empty section of the plan.
	if strings.Count(file, "[27]\n") != 1 {
		t.Errorf("channel 27 is in the file %d times:\n%s", strings.Count(file, "[27]\n"), file)
	}
}

// TestTheEmptySectionsAreSpeltAsTheScannedOnesAre: a channel file's
// DELIVERY_SYSTEM is passed to a backend as it was written, so a file naming
// ISDB-T must not gain a hundred sections naming ISDBT.
func TestTheEmptySectionsAreSpeltAsTheScannedOnesAre(t *testing.T) {
	file, _, _ := writeDVBv5(t, "TER", []report.Result{
		dvbv5Result("27", "TER", map[string]string{
			"DELIVERY_SYSTEM": "ISDB-T", "FREQUENCY": "557142857",
		}),
	}, DVBv5Options{})
	if strings.Contains(file, "= ISDBT\n") {
		t.Errorf("an empty section is spelt differently from the scanned one:\n%s", file)
	}
	// The scanned channel said nothing about bandwidth, so the empty ones fall
	// back to the width of a channel of the plan.
	if !strings.Contains(file, "[1]\n\tDELIVERY_SYSTEM = ISDB-T\n\tFREQUENCY = 93142857\n\tBANDWIDTH_HZ = 6000000\n") {
		t.Errorf("the empty sections are not written as the plan:\n%s", file)
	}
}

// TestOnlyTheTerrestrialPlanIsFilled: an unscanned cable channel would need a
// symbol rate and a modulation, which are the operator's and in no plan.
func TestOnlyTheTerrestrialPlanIsFilled(t *testing.T) {
	file, _, _ := writeDVBv5(t, "CATV", []report.Result{
		dvbv5Result("C13", "CATV", map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "111000000",
			"SYMBOL_RATE": "5274000", "MODULATION": "QAM/AUTO",
		}),
	}, DVBv5Options{})
	if got := sectionsOf(file); len(got) != 1 || got[0] != "C13" {
		t.Errorf("sections are %v, want [C13]", got)
	}
}

// TestOneLeadCarriesBothSystemsInOneFile: a DVBv5 section names its own
// delivery system, so a cable carrying transmodulated channels as J.83 and
// passing ISDB-T through on the same numbers is one file -- the whole reason
// the split is by lead.
func TestOneLeadCarriesBothSystemsInOneFile(t *testing.T) {
	file, _, _ := writeDVBv5(t, "CATV", []report.Result{
		dvbv5Result("C13", "CATV", map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "111000000",
			"SYMBOL_RATE": "5274000", "MODULATION": "QAM/AUTO",
		}),
		dvbv5Result("T27", "CATV", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857", "BANDWIDTH_HZ": "6000000",
		}),
	}, DVBv5Options{})
	for _, want := range []string{"[C13]", "[T27]"} {
		if !strings.Contains(file, want+"\n") {
			t.Errorf("the file does not contain %s:\n%s", want, file)
		}
	}
	// The plan is filled around the terrestrial channel that was found, and
	// what covers a channel of it is the frequency rather than the name: the
	// scan called 557142857 T27, so the plan's own 27 is not written again.
	if !strings.Contains(file, "[28]\n") {
		t.Errorf("the plan was not filled beside the cable channels:\n%s", file)
	}
	if strings.Contains(file, "[27]\n") {
		t.Errorf("the plan wrote 27 again beside the T27 that covers it:\n%s", file)
	}
}

// TestAnUnroutedScanIsAFileOfItsOwn. A scan that was not told its lead is not
// filed under one.
func TestAnUnroutedScanIsAFileOfItsOwn(t *testing.T) {
	value := report.Report{Results: []report.Result{
		dvbv5Result("16625", "", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
		dvbv5Result("27", "TER", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857",
		}),
	}}
	got := DVBv5Routes(value)
	if len(got) != 2 || got[0] != "TER" || got[1] != "" {
		t.Errorf("DVBv5Routes() = %q, want [TER \"\"]", got)
	}
	if suffix := DVBv5FileSuffix(""); suffix != ".conf" {
		t.Errorf("DVBv5FileSuffix(\"\") = %q, want .conf", suffix)
	}
	if suffix := DVBv5FileSuffix("HIKARI"); suffix != "_hikari.conf" {
		t.Errorf("DVBv5FileSuffix(HIKARI) = %q, want _hikari.conf", suffix)
	}
}

// TestTwoChannelsOfOneNameAreNotBothWritten. A file this program's own parser
// refuses is not a file to write; the second one is left out and said so.
func TestTwoChannelsOfOneNameAreNotBothWritten(t *testing.T) {
	file, _, skipped := writeDVBv5(t, "CATV", []report.Result{
		dvbv5Result("27", "CATV", map[string]string{
			"DELIVERY_SYSTEM": "DVBC/ANNEX_A", "FREQUENCY": "557000000", "SYMBOL_RATE": "5274000",
		}),
		dvbv5Result("27", "CATV", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857",
		}),
	}, DVBv5Options{})
	if got := sectionsOf(file); len(got) != 1 {
		t.Errorf("sections are %v, want one of them", got)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "27") {
		t.Errorf("nothing said which channel was left out: %v", skipped)
	}
	// Parsing it back is the real check: a duplicate section is what the
	// parser refuses.
	if _, err := channelconfig.ParseReader(strings.NewReader(file), "written", "CATV"); err != nil {
		t.Errorf("the written file does not parse: %v", err)
	}
}

// TestWhatIsWrittenCanBeScannedAgain. This is the one writer whose output is
// the shape its own input is, so the round trip is worth holding to.
func TestWhatIsWrittenCanBeScannedAgain(t *testing.T) {
	file, _, _ := writeDVBv5(t, "SAT", []report.Result{
		dvbv5Result("16625", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
	}, DVBv5Options{})
	entries, err := channelconfig.ParseReader(strings.NewReader(file), "written", "SAT")
	if err != nil {
		t.Fatalf("parse what was written: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "16625" ||
		entries[0].Properties["STREAM_ID"] != "16625" ||
		entries[0].Properties["FREQUENCY"] != "1318000" {
		t.Errorf("the round trip did not give the channel back: %+v", entries)
	}
}

// TestTheDishSettingsAreWrittenIntoTheSatelliteChannels. LNB and SAT_NUMBER are
// libdvbv5's own keys, so a file carrying them says the same thing to dvbv5-zap
// and to this program.
func TestTheDishSettingsAreWrittenIntoTheSatelliteChannels(t *testing.T) {
	file, notes, _ := writeDVBv5(t, "SAT", []report.Result{
		skyResult("JCSAT3A/JD1", map[string]string{
			"FREQUENCY": "12508000", "POLARIZATION": "VERTICAL", "SYMBOL_RATE": "23303000",
		}),
	}, DVBv5Options{LNBf: "JCSAT3A", SatNumber: "1"})
	if len(notes) != 0 {
		t.Errorf("noted %v", notes)
	}
	for _, want := range []string{"\tLNB = JCSAT3A\n", "\tSAT_NUMBER = 1\n"} {
		if !strings.Contains(file, want) {
			t.Errorf("the file does not contain %q:\n%s", want, file)
		}
	}
}

// TestWhatTheScanTunedWithWins. The scan recorded what actually locked, which
// is a better answer than what was asked for on the command line afterwards.
func TestWhatTheScanTunedWithWins(t *testing.T) {
	file, _, _ := writeDVBv5(t, "SAT", []report.Result{
		skyResult("JCSAT4B/JD2", map[string]string{
			"FREQUENCY": "12523250", "LNB": "JCSAT4B", "SAT_NUMBER": "2",
		}),
	}, DVBv5Options{LNBf: "JCSAT3A", SatNumber: "1"})
	for _, want := range []string{"\tLNB = JCSAT4B\n", "\tSAT_NUMBER = 2\n"} {
		if !strings.Contains(file, want) {
			t.Errorf("the file does not contain %q:\n%s", want, file)
		}
	}
}

// TestNoBlockIsNamedOnAJapaneseSatelliteChannel: naming a block says the
// channel's frequency is a downlink with an oscillator to come off it, and an
// ISDB-S channel file carries the intermediate frequency already, so writing
// one in subtracts the oscillator twice. A DiSEqC position is not like that:
// which port the dish is on is the same answer whatever the frequency means, so
// it is still written.
func TestNoBlockIsNamedOnAJapaneseSatelliteChannel(t *testing.T) {
	file, notes, _ := writeDVBv5(t, "SAT", []report.Result{
		dvbv5Result("16625", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1318000", "STREAM_ID": "16625",
		}),
	}, DVBv5Options{LNBf: "110BS", SatNumber: "1"})
	if strings.Contains(file, "LNB =") {
		t.Errorf("a block was named on an intermediate frequency:\n%s", file)
	}
	if !strings.Contains(file, "\tSAT_NUMBER = 1\n") {
		t.Errorf("the switch position was left out too:\n%s", file)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "16625") {
		t.Errorf("nothing said the block was left out: %v", notes)
	}
}

// TestTheDishSettingsLeaveTheReportAlone. A second export of one report must
// see what the first one saw.
func TestTheDishSettingsLeaveTheReportAlone(t *testing.T) {
	results := []report.Result{
		skyResult("JCSAT3A/JD1", map[string]string{"FREQUENCY": "12508000"}),
	}
	writeDVBv5(t, "SAT", results, DVBv5Options{LNBf: "JCSAT3A", SatNumber: "1"})
	if _, written := results[0].Properties["LNB"]; written {
		t.Errorf("the export wrote a block back into the report: %v", results[0].Properties)
	}
	file, _, _ := writeDVBv5(t, "SAT", results, DVBv5Options{})
	if strings.Contains(file, "LNB =") {
		t.Errorf("the second export saw the first one's settings:\n%s", file)
	}
}

// TestOnlyTheSatelliteChannelsGetThem. A block and a switch are things on a
// dish, and nothing over the air or off a cable reaches one.
func TestOnlyTheSatelliteChannelsGetThem(t *testing.T) {
	file, _, _ := writeDVBv5(t, "TER", []report.Result{
		dvbv5Result("27", "TER", map[string]string{
			"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "557142857",
		}),
	}, DVBv5Options{LNBf: "UNIVERSAL", SatNumber: "1"})
	if strings.Contains(file, "LNB =") || strings.Contains(file, "SAT_NUMBER") {
		t.Errorf("a terrestrial channel was given a dish setting:\n%s", file)
	}
}

// TestTheAdvancedSatelliteIsWrittenAsISDBS. ISDB-S3 is a standard of its own
// and a scan records it as one, but the Linux DVB API numbers ISDB-S and
// nothing beyond it: a file naming ISDB-S3 is one dvbv5-zap refuses to parse.
// It goes out as ISDB-S, with the stream identifier beside it doing what it
// always did.
func TestTheAdvancedSatelliteIsWrittenAsISDBS(t *testing.T) {
	results := []report.Result{
		dvbv5Result("16401", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16401",
		}),
		dvbv5Result("45162", "SAT", map[string]string{
			"DELIVERY_SYSTEM": "ISDBS3", "FREQUENCY": "2471820", "STREAM_ID": "45162",
		}),
	}
	file, _, skipped := writeDVBv5(t, "SAT", results, DVBv5Options{})
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	if strings.Contains(file, "ISDBS3") {
		t.Errorf("the file names a delivery system libdvbv5 has no number for:\n%s", file)
	}
	// Two channels, both as ISDB-S, and each keeping the stream identifier
	// that is what tells them apart.
	if got := strings.Count(file, "DELIVERY_SYSTEM = ISDBS"); got != 2 {
		t.Errorf("the file has %d ISDBS channels, want 2:\n%s", got, file)
	}
	for _, want := range []string{"STREAM_ID = 16401", "STREAM_ID = 45162"} {
		if !strings.Contains(file, want) {
			t.Errorf("%q is not in the file:\n%s", want, file)
		}
	}

	// The report itself is untouched: a second export must see what the first
	// one saw, and every other format can say ISDB-S3.
	if results[1].Properties["DELIVERY_SYSTEM"] != "ISDBS3" {
		t.Errorf("the export wrote back into the report: %v", results[1].Properties)
	}
}

// dvbv5 knows the Japanese cable band only as the DVB-C annex its tuners lock
// it with, so a scan that wrote down ISDBC has to go out as that.
func TestTheCableBandGoesOutUnderTheDVBName(t *testing.T) {
	properties := map[string]string{"DELIVERY_SYSTEM": "ISDBC", "FREQUENCY": "111000000"}
	written := dvbv5Systems(properties)
	if written["DELIVERY_SYSTEM"] != "DVBC/ANNEX_A" {
		t.Errorf("the band went out as %q", written["DELIVERY_SYSTEM"])
	}
	// The report's own properties are left as they were: a second export has
	// to see what the first one saw.
	if properties["DELIVERY_SYSTEM"] != "ISDBC" {
		t.Errorf("the report was rewritten to %q", properties["DELIVERY_SYSTEM"])
	}
}
