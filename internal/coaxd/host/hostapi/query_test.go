// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import (
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

func parse(t *testing.T, raw string) (Request, error) {
	t.Helper()
	query, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatalf("the test's own query is malformed: %v", err)
	}
	return ParseRequest(query, Request{})
}

// TestShorthandsAreForPeople checks the short URL a viewer types at a player.
func TestShorthandsAreForPeople(t *testing.T) {
	request, err := parse(t, "system=ISDBT&freq=557142857")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := request.Properties["FREQUENCY"]; got != "557142857" {
		t.Errorf("freq became %q, want 557142857", got)
	}
	if got := request.Properties["DELIVERY_SYSTEM"]; got != "ISDBT" {
		t.Errorf("the delivery system came out as %q", got)
	}
}

// TestPropertiesPassThroughByteExact lets a client hand over a channel-file
// section without re-encoding it.
func TestPropertiesPassThroughByteExact(t *testing.T) {
	request, err := parse(t, "DELIVERY_SYSTEM=ISDB-S&FREQUENCY=1049480&STREAM_ID=16625&ROLLOFF=35")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"DELIVERY_SYSTEM": "ISDB-S",
		"FREQUENCY":       "1049480",
		"STREAM_ID":       "16625",
		"ROLLOFF":         "35",
	}
	for name, value := range want {
		if request.Properties[name] != value {
			t.Errorf("%s came through as %q, want %q", name, request.Properties[name], value)
		}
	}
	if len(request.Properties) != len(want) {
		t.Errorf("produced %v, want exactly %v", request.Properties, want)
	}
}

// TestServiceKeysAreSetAsideNotRefused: a zap file mixes tuning parameters with
// the identifiers of one programme inside the multiplex, and a backend handed
// the latter refuses the whole channel.
func TestServiceKeysAreSetAsideNotRefused(t *testing.T) {
	request, err := parse(t, "DELIVERY_SYSTEM=ISDBT&FREQUENCY=557142857&SERVICE_ID=1024&VIDEO_PID=273")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, present := request.Properties["SERVICE_ID"]; present {
		t.Error("a service identifier was passed to the tuner")
	}
	if !slices.Equal(request.Ignored, []string{"SERVICE_ID", "VIDEO_PID"}) {
		t.Errorf("reported %v as set aside", request.Ignored)
	}
	if request.Properties["FREQUENCY"] != "557142857" {
		t.Error("the tuning properties did not survive")
	}
}

func TestControlsDoNotReachTheTuner(t *testing.T) {
	request, err := parse(t, "system=ISDBT&freq=557142857&tuner=px4:ABC:2&lock_timeout=7s&settle=1500ms&begin=lock")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.Tuner != "px4:ABC:2" {
		t.Errorf("named tuner %q", request.Tuner)
	}
	if request.LockTimeout != 7*time.Second {
		t.Errorf("lock timeout is %v, want 7s", request.LockTimeout)
	}
	if request.Settle != 1500*time.Millisecond {
		t.Errorf("settle is %v, want 1.5s", request.Settle)
	}
	if !request.WaitForLock {
		t.Error("begin=lock did not ask for a lock")
	}
	for _, name := range []string{"tuner", "lock_timeout", "settle", "begin", "TUNER"} {
		if _, leaked := request.Properties[name]; leaked {
			t.Errorf("%s reached the tuning properties", name)
		}
	}
}

// TestBareNumbersAreSeconds keeps a URL a person types free of units.
func TestBareNumbersAreSeconds(t *testing.T) {
	request, err := parse(t, "system=ISDBT&freq=557142857&lock_timeout=5")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.LockTimeout != 5*time.Second {
		t.Errorf("lock_timeout=5 became %v, want 5s", request.LockTimeout)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct {
		name  string
		query string
	}{
		{"an unknown parameter is a mistake, not something to ignore", "system=ISDBT&freq=557142857&channel=27"},
		{"a shorthand and its property are the same thing", "system=ISDBT&DELIVERY_SYSTEM=ISDBT&freq=557142857"},
		{"no delivery system", "freq=557142857"},
		{"no frequency", "system=ISDBT"},
		{"a negative timeout", "system=ISDBT&freq=557142857&lock_timeout=-1"},
		{"a NaN timeout", "system=ISDBT&freq=557142857&lock_timeout=NaN"},
		{"an infinite timeout", "system=ISDBT&freq=557142857&lock_timeout=Inf"},
		{"an overflowing timeout", "system=ISDBT&freq=557142857&lock_timeout=1e100"},
		{"a timeout that is not a length of time", "system=ISDBT&freq=557142857&lock_timeout=soon"},
		{"begin with a value it does not take", "system=ISDBT&freq=557142857&begin=whenever"},
		{"a parameter given twice", "system=ISDBT&freq=1&freq=2"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if _, err := parse(t, one.query); err == nil {
				t.Errorf("%s was accepted", one.query)
			}
		})
	}
}

// TestTheGainShorthandBecomesAProperty: the lower-case gain and lna shorthands
// land on the keys a channel file spells in capitals, and a shorthand given
// alongside its own property, or with a value that is no setting, is refused.
func TestTheGainShorthandBecomesAProperty(t *testing.T) {
	request, err := parse(t, "system=ISDBT&freq=557142857&gain=%2B13dB")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.Properties[tuning.GainProperty] != "+13dB" {
		t.Errorf("gain=+13dB became %s=%q", tuning.GainProperty, request.Properties[tuning.GainProperty])
	}
	if request, err := parse(t, "system=ISDBT&freq=557142857&lna=off"); err != nil {
		t.Errorf("lna=off: %v", err)
	} else if request.Properties[tuning.LNAProperty] != "off" {
		t.Errorf("lna=off became %s=%q", tuning.LNAProperty, request.Properties[tuning.LNAProperty])
	}
	for _, query := range []string{
		"system=ISDBT&freq=557142857&gain=off&GAIN=on",
		"system=ISDBT&freq=557142857&gain=off&lna=on",
		"system=ISDBT&freq=557142857&gain=23",
	} {
		if _, err := parse(t, query); err == nil {
			t.Errorf("%s was accepted", query)
		}
	}
}

// TestLNBPowerBecomesAProperty: the one control that does describe the channel
// is folded in rather than dropped.
func TestLNBPowerBecomesAProperty(t *testing.T) {
	request, err := parse(t, "system=ISDBS&freq=1049480&STREAM_ID=16625&lnb_power=15")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.Properties["VOLTAGE"] != "15" {
		t.Errorf("lnb_power=15 became %v", request.Properties)
	}
}

func TestDefaultsAreKept(t *testing.T) {
	query, _ := url.ParseQuery("system=ISDBT&freq=557142857")
	request, err := ParseRequest(query, Request{LockTimeout: 3 * time.Second, WaitForLock: true})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.LockTimeout != 3*time.Second {
		t.Errorf("the default lock timeout was lost: %v", request.LockTimeout)
	}
	if !request.WaitForLock {
		t.Error("the default was lost")
	}
}

// A meaningless supply setting is a mistake in the request, not a tuner that
// could not serve it, so it is caught before a tuner is chosen.
func TestTheSupplySettingIsCheckedBeforeATunerIsChosen(t *testing.T) {
	for _, raw := range []string{
		"system=ISDBS&freq=1049480&lnb_power=12",
		"system=ISDBS&freq=1049480&VOLTAGE=24",
		"system=ISDBS&freq=1049480&lnb_power=yes",
	} {
		if _, err := parse(t, raw); err == nil {
			t.Errorf("%q should have been refused", raw)
		}
	}
}

// Every spelling arrives as the same property.
func TestTheSupplySettingArrivesAsTheVoltageProperty(t *testing.T) {
	for _, one := range []struct{ raw, want string }{
		{"system=ISDBS&freq=1049480&lnb_power=off", "off"},
		{"system=ISDBS&freq=1049480&lnb_power=15", "15"},
		{"system=ISDBS&freq=1049480&VOLTAGE=high", "high"},
	} {
		request, err := parse(t, one.raw)
		if err != nil {
			t.Fatalf("%q: %v", one.raw, err)
		}
		if request.Properties["VOLTAGE"] != one.want {
			t.Errorf("%q became VOLTAGE=%q, want %q", one.raw, request.Properties["VOLTAGE"], one.want)
		}
	}
}

// Saying it twice, once each way, is a mistake worth naming.
func TestLNBAndVoltageAreNotBothAccepted(t *testing.T) {
	if _, err := parse(t, "system=ISDBS&freq=1049480&lnb_power=off&VOLTAGE=high"); err == nil {
		t.Error("lnb_power together with VOLTAGE should be refused")
	}
}

// A channel naming the block on the dish is converted before it reaches a
// backend: the downlink becomes the intermediate frequency, and the tone the
// lead needs is filled in. This is the 124/128 degrees east DVB-S2 case, where
// the two satellites share an oscillator and a lead and the tone picks between
// them.
func TestNamingTheBlockConvertsTheDownlink(t *testing.T) {
	for _, one := range []struct {
		raw  string
		tone string
	}{
		{"system=DVBS2&freq=12268000&lnbf=JCSAT3A&symbol_rate=21096000", "OFF"},
		{"system=DVBS2&freq=12268000&lnbf=JCSAT4A&symbol_rate=21096000", "ON"},
	} {
		request, err := parse(t, one.raw)
		if err != nil {
			t.Fatalf("%q: %v", one.raw, err)
		}
		if request.Properties["FREQUENCY"] != "1068000" {
			t.Errorf("%q: FREQUENCY = %q, want 1068000", one.raw, request.Properties["FREQUENCY"])
		}
		if request.Properties["TONE"] != one.tone {
			t.Errorf("%q: TONE = %q, want %q", one.raw, request.Properties["TONE"], one.tone)
		}
		// The name goes through too: the Windows driver is told which block is on
		// the dish and converts itself, so it must be able to undo this.
		if request.Properties["LNB"] == "" {
			t.Errorf("%q: the block's name was dropped", one.raw)
		}
	}
}

// Channel files already in use carry the intermediate frequency and name no
// block; those go through untouched.
func TestAChannelThatNamesNoBlockIsNotConverted(t *testing.T) {
	request, err := parse(t, "system=ISDBS&freq=1049480&STREAM_ID=16400")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.Properties["FREQUENCY"] != "1049480" {
		t.Errorf("FREQUENCY = %q, want it left alone", request.Properties["FREQUENCY"])
	}
	if _, given := request.Properties["TONE"]; given {
		t.Error("a tone was invented for a channel that named no block")
	}
}

// The block may equally be named as a DVBv5 channel file names it, which is the
// point of spelling it the same way.
func TestTheBlockMayBeNamedAsAChannelFileNamesIt(t *testing.T) {
	request, err := parse(t, "system=DVBS2&freq=12268000&LNB=JCSAT4A&symbol_rate=21096000")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if request.Properties["FREQUENCY"] != "1068000" || request.Properties["TONE"] != "ON" {
		t.Errorf("LNB=JCSAT4A gave %v", request.Properties)
	}
}

// The sync check is on unless a caller says otherwise, and a value it does not
// take is refused rather than quietly replaced by the default.
func TestSyncIsAControlAndTakesOnlyOnAndOff(t *testing.T) {
	base := "system=ISDBT&freq=473142857"
	for _, one := range []struct {
		query string
		want  bool
	}{
		{base, true},
		{base + "&sync=on", true},
		{base + "&sync=off", false},
	} {
		values, err := url.ParseQuery(one.query)
		if err != nil {
			t.Fatalf("ParseQuery(%q): %v", one.query, err)
		}
		request, err := ParseRequest(values, Request{RequireSync: true})
		if err != nil {
			t.Fatalf("ParseRequest(%q): %v", one.query, err)
		}
		if request.RequireSync != one.want {
			t.Errorf("%q gave RequireSync=%v, want %v", one.query, request.RequireSync, one.want)
		}
		if _, sent := request.Properties["sync"]; sent {
			t.Errorf("%q sent sync on to the backend as a tuning property", one.query)
		}
	}

	values, _ := url.ParseQuery(base + "&sync=maybe")
	if _, err := ParseRequest(values, Request{RequireSync: true}); err == nil {
		t.Error("sync=maybe should have been refused")
	}
}
