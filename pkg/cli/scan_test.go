// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"strings"
	"testing"
	"time"
)

// Told nothing, a scan names its report after the tuner it came off and the
// time it finished, and writes it where the command was run.
func TestAnUnnamedReportIsNamedAfterTheTunerAndTheTime(t *testing.T) {
	at := time.Date(2026, 8, 29, 14, 30, 12, 0, time.UTC)
	got := reportName("chardev", "dvb:adapter2.frontend0:0", at)
	want := "chardev_dvb-adapter2.frontend0-0_20260829_143012.json"
	if got != want {
		t.Errorf("report name is %q, want %q", got, want)
	}
	// A tuner named after a device path must not scatter the report across
	// directories.
	if name := reportName("chardev", "/dev/dvb/adapter0/frontend0", at); name != "chardev_dev-dvb-adapter0-frontend0_20260829_143012.json" {
		t.Errorf("a device path was not made safe to write: %q", name)
	}
	// Nothing tuned, so nothing to name it after, and it still has a name.
	if name := reportName("", "", at); name != "coax_coax_20260829_143012.json" {
		t.Errorf("an unnamed tuner gave %q", name)
	}
}

// The progress file sits beside the report only where a report was named: the
// name a scan makes up for itself is not known until the scan is over, and the
// progress is written from the first channel onwards.
func TestProgressSitsWhereTheCommandWasRunUnlessTheReportWasNamed(t *testing.T) {
	for _, one := range []struct {
		name    string
		options scanOptions
		want    string
	}{
		{"asked for", scanOptions{progress: "elsewhere.json"}, "elsewhere.json"},
		{"beside a named report", scanOptions{output: "scan-sat.json"}, "scan-sat.json.progress.json"},
		{"printing", scanOptions{output: "-"}, ".coax.progress.json"},
		{"named by the scan", scanOptions{output: ""}, ".coax.progress.json"},
	} {
		if got := one.options.progressPath(); got != one.want {
			t.Errorf("%s: progress path is %q, want %q", one.name, got, one.want)
		}
	}
}

// A listing never opens a tuner, so there is nothing to name a file after and
// it prints instead.
func TestAListingPrintsWhenNoOutputWasAskedFor(t *testing.T) {
	if got := (scanOptions{}).outputOrStdout(); got != "-" {
		t.Errorf("a listing wrote to %q rather than standard output", got)
	}
	if got := (scanOptions{output: "candidates.json"}).outputOrStdout(); got != "candidates.json" {
		t.Errorf("a listing wrote to %q", got)
	}
}

// TestOneChannelIsScannedWithoutAFile. A scan report is what a channel list is
// built from, so asking about one channel should not need a file with one
// section in it. The channel is described the way `coax tune` takes it, and
// comes out as the entry the rest of a scan carries.
func TestOneChannelIsScannedWithoutAFile(t *testing.T) {
	for _, one := range []struct {
		name       string
		request    channelRequest
		route      string
		wantName   string
		wantValues map[string]string
	}{
		{
			name:     "a television channel names its own system",
			request:  channelRequest{channel: "27"},
			wantName: "27",
			wantValues: map[string]string{
				"DELIVERY_SYSTEM": "ISDBT",
				"FREQUENCY":       "557142857",
			},
		},
		{
			// The name is kept rather than replaced by the frequency: a report
			// a person reads is easier to follow when it says what they asked
			// for.
			name:     "a satellite channel keeps the name it was asked for",
			request:  channelRequest{channel: "BS15_1"},
			route:    "sat",
			wantName: "BS15_1",
			wantValues: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1318000",
				"STREAM_ID":       "1",
			},
		},
		{
			// Nothing named it, so the frequency is all there is to call it.
			name:     "a frequency names itself",
			request:  channelRequest{system: "ISDBT", frequency: "473142857"},
			wantName: "473142857",
			wantValues: map[string]string{
				"DELIVERY_SYSTEM": "ISDBT",
				"FREQUENCY":       "473142857",
			},
		},
		{
			// Two carriers of one transponder are two channels, and two names.
			name:     "a stream on a frequency says which",
			request:  channelRequest{system: "ISDBS", frequency: "1318000", streamID: "3"},
			wantName: "1318000_3",
			wantValues: map[string]string{
				"DELIVERY_SYSTEM": "ISDBS",
				"FREQUENCY":       "1318000",
				"STREAM_ID":       "3",
			},
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			entry, err := namedChannel(one.request, one.route)
			if err != nil {
				t.Fatalf("build the channel: %v", err)
			}
			if entry.Name != one.wantName {
				t.Errorf("the channel is called %q, want %q", entry.Name, one.wantName)
			}
			if entry.Source != "command line" {
				t.Errorf("the channel says it came from %q", entry.Source)
			}
			want := strings.ToUpper(one.route)
			if entry.Route != want {
				t.Errorf("the route is %q, want %q", entry.Route, want)
			}
			for name, value := range one.wantValues {
				if entry.Properties[name] != value {
					t.Errorf("%s = %q, want %q", name, entry.Properties[name], value)
				}
			}
		})
	}
}

// TestWhatCountsAsNamingAChannel: the flags that say which channel, told apart
// from the ones that say how to receive it. A command line carrying only the
// second has named nothing and is asking for a channel file.
func TestWhatCountsAsNamingAChannel(t *testing.T) {
	for _, one := range []struct {
		name    string
		request channelRequest
		want    bool
	}{
		{"nothing at all", channelRequest{}, false},
		{"a channel", channelRequest{channel: "27"}, true},
		{"a frequency", channelRequest{frequency: "473142857"}, true},
		{"a delivery system", channelRequest{system: "ISDBT"}, true},
		{"a stream", channelRequest{streamID: "1"}, true},
		{"a property", channelRequest{extra: map[string]string{"SYMBOL_RATE": "28860000"}}, true},
		// How to receive it, not which one it is. On their own these describe
		// no channel, and a scan told only these is a scan of a file.
		{"a gain", channelRequest{gain: "auto"}, false},
		{"an LNB supply", channelRequest{lnbPower: "high"}, false},
		{"a low-noise block", channelRequest{lnbf: "JCSAT3A"}, false},
		{"a switch position", channelRequest{satNumber: "1"}, false},
	} {
		t.Run(one.name, func(t *testing.T) {
			if got := one.request.given(); got != one.want {
				t.Errorf("given() = %v, want %v", got, one.want)
			}
		})
	}
}
