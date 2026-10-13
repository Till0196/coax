// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package scanner

import (
	"context"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/Till0196/coax/pkg/tuner"
)

// recordingDevice notes every channel it was asked for and never locks, so a
// walk over the transponders runs to its end quickly.
type recordingDevice struct {
	tuned []map[string]string
	// relativeStreams is what this tuner claims about how a satellite stream
	// is named on it, as the daemon reports it with a tune.
	relativeStreams int
}

func (device *recordingDevice) Backend() string               { return "test" }
func (device *recordingDevice) RelativeStreams() int          { return device.relativeStreams }
func (device *recordingDevice) Name() string                  { return "test0" }
func (device *recordingDevice) Supports(string) (bool, error) { return true, nil }
func (device *recordingDevice) Close() error                  { return nil }
func (device *recordingDevice) Status() (tuner.Status, error) {
	return tuner.Status{}, nil
}
func (device *recordingDevice) OpenStream(tuner.Bounds) (tuner.Stream, error) {
	return nil, io.EOF
}
func (device *recordingDevice) Tune(properties map[string]string) error {
	device.tuned = append(device.tuned, properties)
	return nil
}

// Nothing can tell from a tuner's own account of itself whether it receives
// ISDB-S3: the Linux DVB API has no delivery system number for it and Windows
// BDA cannot be asked. Guessing from the backend name is worse -- a board added
// without the name in its list would have its ISDB-S3 services quietly passed
// over.
// So a scan makes the walk only when ISDB-S3 is among the systems it was told
// to cover.
func TestTheTLVWalkIsMadeOnlyWhenISDBS3IsCovered(t *testing.T) {
	// The identifiers the TLV walk probes with, which nothing else asks for.
	probes := map[string]bool{}
	for _, on := range append(broadcastTransponders, communicationsTransponders...) {
		probes[strconv.Itoa(int(on.tlvProbe))] = true
	}

	for _, test := range []struct {
		what     string
		covering func(string) bool
		want     bool
	}{
		{"both covered, as a scan that was told nothing covers everything",
			func(string) bool { return true }, true},
		{"only the older satellite", func(s string) bool { return s == "ISDBS" }, false},
		{"only the advanced one", func(s string) bool { return s == "ISDBS3" }, true},
	} {
		device := &recordingDevice{}
		options := Options{Device: device, LockTimeout: 0, PollInterval: time.Millisecond}
		_, _ = discoverSatellite(context.Background(), options, newDiscoveryDisplay(io.Discard), test.covering)

		probed := false
		for _, properties := range device.tuned {
			if probes[properties["STREAM_ID"]] {
				probed = true
			}
		}
		if probed != test.want {
			t.Errorf("%s: the TLV walk was made = %v, want %v", test.what, probed, test.want)
		}
	}
}

// Covering everything the tuner carries cannot cover ISDB-S3, because every
// satellite front end answers yes when asked. Believing that buys a walk over
// two dozen transponders -- three quarters of a bootstrap's time -- for a
// network most installations do not have. This exercises resolveSystems against
// the same rule Discover's carries applies.
func TestABareDiscoverDoesNotWalkForTLV(t *testing.T) {
	for _, test := range []struct {
		what    string
		named   []string
		wantTLV bool
	}{
		{"nothing named, so everything the tuner carries bar the advanced one", nil, false},
		{"the older satellite by name", []string{"ISDBS"}, false},
		{"the advanced one by name", []string{"ISDBS3"}, true},
		{"both by name", []string{"ISDBS", "ISDBS3"}, true},
	} {
		chosen, err := resolveSystems(test.named)
		if err != nil {
			t.Fatalf("%s: resolveSystems: %v", test.what, err)
		}
		// The same rule Discover applies: what was named, or everything the
		// tuner carries bar the one nothing can be told about.
		covers := func(system string) bool {
			if chosen != nil {
				return chosen[system]
			}
			return system != "ISDBS3"
		}
		if got := covers("ISDBS3"); got != test.wantTLV {
			t.Errorf("%s: ISDBS3 covered = %v, want %v", test.what, got, test.wantTLV)
		}
	}
}
