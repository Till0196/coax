// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
)

func TestPlexStreamLooksSlotsUpAndPassesTSIDsOn(t *testing.T) {
	slots := map[int]uint16{0: 0x4630, 1: 0x4631}
	streamAt := func(slot int) (uint16, error) { return slots[slot], nil }
	for _, c := range []struct {
		delivery tuning.Delivery
		stream   int32
		at       func(int) (uint16, error)
		want     int32
	}{
		{tuning.DeliverySatellite, 1, streamAt, 0x4631},
		// Not named is slot zero.
		{tuning.DeliverySatellite, -1, streamAt, 0x4630},
		{tuning.DeliverySatellite, 0x4011, streamAt, 0x4011},
		// Nothing to look a slot up in: passed on as it is.
		{tuning.DeliverySatellite, 1, nil, 1},
		{tuning.DeliveryTerrestrial, -1, streamAt, -1},
	} {
		options := Options{Profile: tuning.TuneProfile{Delivery: c.delivery, StreamID: c.stream}, StreamAt: c.at}
		got, err := plexStream(options)
		if err != nil || got != c.want {
			t.Errorf("%v stream %d: got %#x, %v; want %#x", c.delivery, c.stream, got, err, c.want)
		}
	}
}
