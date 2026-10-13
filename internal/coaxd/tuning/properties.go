// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import "sort"

// TuningProperties are the channel-file keys that say how to acquire a channel,
// as against the ones that say what is carried on it.
//
// A dvbv5-zap file puts both kinds in one section: alongside FREQUENCY and
// MODULATION sit SERVICE_ID, VIDEO_PID and AUDIO_PID, which mean nothing to a
// tuner. The Linux DVB backend refuses a property it does not know, so a whole
// section handed over unfiltered fails the channel outright.
//
// The set is exactly what dvb.BuildTuneProperties accepts; a test asserts the
// two agree.
var TuningProperties = map[string]bool{
	"DELIVERY_SYSTEM":   true,
	"FREQUENCY":         true,
	"MODULATION":        true,
	"BANDWIDTH_HZ":      true,
	"SYMBOL_RATE":       true,
	"INVERSION":         true,
	"INNER_FEC":         true,
	"CODE_RATE_HP":      true,
	"CODE_RATE_LP":      true,
	"TRANSMISSION_MODE": true,
	"GUARD_INTERVAL":    true,
	"HIERARCHY":         true,
	"VOLTAGE":           true,
	"POLARIZATION":      true,
	"TONE":              true,
	"PILOT":             true,
	"ROLLOFF":           true,
	"STREAM_ID":         true,
	// The low-noise block on the dish. It is not a property any driver takes,
	// but it survives this filter because a backend whose driver does the
	// downconversion itself has to be told which block to do it for. See
	// LNBProperty and lnbf.go.
	"LNB": true,
	// The front end's gain, and the older spelling of the same key. It is a
	// real property on Linux, where the kernel has DTV_LNA, and a setting
	// this program applies itself on the boards whose tuner has one. See
	// GainProperty and gain.go.
	"GAIN": true,
	"LNA":  true,
	// The DiSEqC position on the lead. Like LNB it is not a property any
	// driver takes: it is a message put on the lead before the tune, and the
	// backend that can send one reads it from here. See diseqc.go.
	SatNumberProperty: true,
}

// SelectTuning splits a channel-file section into the properties a tuner is
// given and the names of the keys left behind, in the order they were named.
func SelectTuning(properties map[string]string) (map[string]string, []string) {
	selected := make(map[string]string, len(properties))
	var ignored []string
	for key, value := range properties {
		if TuningProperties[key] {
			selected[key] = value
			continue
		}
		ignored = append(ignored, key)
	}
	sort.Strings(ignored)
	return selected, ignored
}
