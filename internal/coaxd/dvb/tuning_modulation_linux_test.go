// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import "testing"

// TestModulationTakesBothVocabularies. The kernel's enumeration writes the
// count first and libdvbv5 writes it last, and a channel file may carry
// either: one written by dvbv5-scan says PSK/8 where the kernel says 8PSK.
// The 124/128 degree east plan built into this program writes PSK/8 as well,
// so this is not only about files somebody else made.
func TestModulationTakesBothVocabularies(t *testing.T) {
	for _, one := range []struct {
		written string
		want    uint32
	}{
		{"QPSK", 0},
		{"QAM/256", 5},
		// The separator is not part of it, which is what the reference says
		// and what the drivers and datasheets write.
		{"QAM256", 5},
		{"qam-256", 5},
		{"QAM/AUTO", 6},
		// The pair that was refused before: the plan's own spelling.
		{"PSK/8", 9},
		{"8PSK", 9},
		{"APSK/16", 10},
		{"16APSK", 10},
		{"APSK/32", 11},
		{"32APSK", 11},
		{"VSB/8", 7},
		{"8VSB", 7},
	} {
		t.Run(one.written, func(t *testing.T) {
			property, err := parseTuneProperty("MODULATION", one.written)
			if err != nil {
				t.Fatalf("MODULATION %q: %v", one.written, err)
			}
			if property.Value != one.want {
				t.Errorf("MODULATION %q = %d, want %d", one.written, property.Value, one.want)
			}
		})
	}

	// A spelling that is no modulation at all is still refused rather than
	// passed to the driver.
	if _, err := parseTuneProperty("MODULATION", "QAM/17"); err == nil {
		t.Error("a modulation that does not exist was accepted")
	}
}

// TestISDBS3IsTunedAsISDBS. The Linux DVB API numbers ISDB-S and nothing beyond
// it, so a channel that says ISDB-S3 has to reach the frontend as ISDB-S. One
// demodulator receives both; what separates them is the stream identifier,
// which goes through untouched.
//
// Without this a scan of the ISDB-S3 services is refused channel by channel
// on the very hardware that receives them. See
// config.DeliverySystemSubstitutes.
func TestISDBS3IsTunedAsISDBS(t *testing.T) {
	const isdbs = uint32(9)
	for _, written := range []string{"ISDBS", "ISDB-S", "ISDBS3", "ISDB-S3"} {
		properties, _, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": written,
			"FREQUENCY":       "2471820",
			"STREAM_ID":       "45162",
		})
		if err != nil {
			t.Fatalf("%s: %v", written, err)
		}
		var system, stream uint32
		var carriedStream bool
		for _, property := range properties {
			switch property.Command {
			case dtvDeliverySystem:
				system = property.Value
			case dtvStreamID:
				stream, carriedStream = property.Value, true
			}
		}
		if system != isdbs {
			t.Errorf("%s reached the frontend as %d, want %d", written, system, isdbs)
		}
		if !carriedStream || stream != 45162 {
			t.Errorf("%s lost its stream identifier (%d, given=%t)", written, stream, carriedStream)
		}
	}
}
