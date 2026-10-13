// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package s270

import (
	"bytes"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/bridge/siano"
)

func TestSupports(t *testing.T) {
	for _, delivery := range []string{"ISDBT", "ISDB-T", "isdb_t", "DVBT", "DVB-T"} {
		if !Supports(delivery) {
			t.Errorf("%q should be supported", delivery)
		}
	}
	// The chip has firmware for these, but this program does not carry the
	// message layouts they need.
	for _, delivery := range []string{"DVBT2", "DVBC", "ISDBS", "DVBH", ""} {
		if Supports(delivery) {
			t.Errorf("%q should not be supported", delivery)
		}
	}
}

// TestModesFollowTheKernelDriver pins the per-mode numbers. Sending an ISDB-T
// tuning request in DVB-T mode, or reading the lock at the wrong offset, would
// fail quietly rather than loudly.
func TestModesFollowTheKernelDriver(t *testing.T) {
	isdbt, dvbt := modes["ISDBT"], modes["DVBT"]
	if isdbt.firmware != "isdbt_rio.inp" || dvbt.firmware != "dvb_rio.inp" {
		t.Errorf("firmware images: ISDB-T %q, DVB-T %q", isdbt.firmware, dvbt.firmware)
	}
	if isdbt.device != siano.ModeISDBTBDA || dvbt.device != siano.ModeDVBTBDA {
		t.Errorf("device modes: ISDB-T %d, DVB-T %d", isdbt.device, dvbt.device)
	}
	if isdbt.tuneRequest != 776 || dvbt.tuneRequest != 561 {
		t.Errorf("tune requests: ISDB-T %d, DVB-T %d", isdbt.tuneRequest, dvbt.tuneRequest)
	}
	// The extended statistics are ISDB-T only; the plain reply has no type and
	// size ahead of the lock flags, so the lock sits one pair of words earlier.
	if isdbt.statisticsRequest != 653 || dvbt.statisticsRequest != 615 {
		t.Errorf("statistics requests: ISDB-T %d, DVB-T %d", isdbt.statisticsRequest, dvbt.statisticsRequest)
	}
	if isdbt.demodLockedOffset != 16 || dvbt.demodLockedOffset != 12 {
		t.Errorf("lock offsets: ISDB-T %d, DVB-T %d", isdbt.demodLockedOffset, dvbt.demodLockedOffset)
	}
}

func TestTuneWordsISDBT(t *testing.T) {
	front := &frontend{model: "test"}
	got, err := front.tuneWords(modes["ISDBT"], 473_142_857, map[string]string{"DELIVERY_SYSTEM": "ISDBT"})
	if err != nil {
		t.Fatal(err)
	}
	// Frequency, full-segment reception, the reference clock, first segment.
	want := siano.Words(473_142_857, siano.BandwidthISDBT13Seg, referenceHz, 0)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x; want %x", got, want)
	}
}

func TestTuneWordsDVBT(t *testing.T) {
	front := &frontend{model: "test"}
	for width, code := range map[string]uint32{
		"8000000": siano.Bandwidth8MHz,
		"7000000": siano.Bandwidth7MHz,
		"6000000": siano.Bandwidth6MHz,
	} {
		properties := map[string]string{"DELIVERY_SYSTEM": "DVBT", "BANDWIDTH_HZ": width}
		got, err := front.tuneWords(modes["DVBT"], 474_000_000, properties)
		if err != nil {
			t.Fatalf("%s Hz: %v", width, err)
		}
		// Three words, not the four an ISDB-T request carries.
		want := siano.Words(474_000_000, code, referenceHz)
		if !bytes.Equal(got, want) {
			t.Errorf("%s Hz: got %x; want %x", width, got, want)
		}
	}
}

func TestTuneWordsDVBTNeedsAKnownWidth(t *testing.T) {
	front := &frontend{model: "test"}
	// The firmware will not guess, so neither does this: a file without the
	// key, or with a width the chip has no code for, is refused.
	for _, properties := range []map[string]string{
		{"DELIVERY_SYSTEM": "DVBT"},
		{"DELIVERY_SYSTEM": "DVBT", "BANDWIDTH_HZ": "5000000"},
	} {
		if _, err := front.tuneWords(modes["DVBT"], 474_000_000, properties); err == nil {
			t.Errorf("%v should have been refused", properties)
		}
	}
}
