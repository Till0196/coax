// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package native presents the tuners the operating system already drives as
// boards the pool can hand out.
//
// A Linux DVB adapter and a Windows BDA tuner are not USB devices here:
// something else opened the hardware and what is left is a device node or a
// filter graph. They are claimed exclusively just the same, so they are leased
// the same way.
//
// Each is a board of exactly one tuner, which is no simplification: a DVB
// adapter is one frontend, and a card with two presents two adapters that tune
// independently.
package native

import (
	"context"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/receiver"
)

// controlsFor is what a channel may set on a native tuner beside the channel,
// by backend and by what the tuner receives. See device.Controls.
//
//   - dvb: the kernel's SEC voltage, 13V or 18V, or off. Its amplifier setting
//     (DTV_LNA) is not offered: a frontend without one ignores it, and nothing
//     says which have one.
//   - chardev: px4_drv, the pt3 driver and pt1_drv put 11V or 15V on the lead, or
//     nothing, and the one supply is the card's.
//   - pt3, pt1 (Windows): the same, shared by the card's satellite receivers.
//   - dd: the card's register per tuner, 13V or 18V or off.
//   - bda: nothing. The driver decides the supply and has no amplifier setting.
//
// A tuner that receives nothing by satellite has no supply to set; one that
// says nothing of what it receives (a plain BDA tuner) is not offered one.
func controlsFor(backend string, deliveries []string) device.Controls {
	if !bySatellite(deliveries) {
		return device.Controls{}
	}
	switch backend {
	case "dvb", "dd":
		return device.Controls{LNB: &device.LNBControl{Off: true, Low: "13V", High: "18V"}}
	case "chardev", "pt3", "pt1":
		return device.Controls{LNB: &device.LNBControl{Off: true, Low: "11V", High: "15V", Shared: "card"}}
	}
	return device.Controls{}
}

// bySatellite reports whether any of the delivery systems is a satellite one.
func bySatellite(deliveries []string) bool {
	for _, system := range deliveries {
		upper := strings.ToUpper(system)
		if strings.HasPrefix(upper, "ISDBS") || strings.HasPrefix(upper, "DVBS") ||
			strings.HasPrefix(upper, "DSS") || strings.HasPrefix(upper, "TURBO") {
			return true
		}
	}
	return false
}

// board is one native device, presented as a board carrying a single tuner.
type board struct {
	name   string
	tuner  device.TunerInfo
	device *tuner
}

func (b *board) Name() string               { return b.name }
func (b *board) Tuners() []device.TunerInfo { return []device.TunerInfo{b.tuner} }

// OpenTuner hands out the one tuner this board has, opened when the board was:
// there is one frontend, and claiming it is what "open" means.
func (b *board) OpenTuner(index int) (device.Tuner, error) {
	if index != 0 {
		return nil, errNoSuchTuner(b.name, index)
	}
	return b.device, nil
}

func (b *board) Close() error { return b.device.receiver.Close() }

// tuner adapts a receiver device to the board-driver contract. The two differ
// only in Supports returning an error on one side, and in the two sentinels for
// "no data yet".
type tuner struct {
	receiver receiver.Device
	// deliveries is what the device said it could tune when opened, also what
	// the listing showed before that.
	deliveries []string
}

func (t *tuner) Name() string { return t.receiver.Name() }

func (t *tuner) Supports(deliverySystem string) bool {
	supported, err := t.receiver.Supports(deliverySystem)
	// An error means an unknown name rather than the hardware refusing, but
	// either way this tuner cannot serve and the caller is choosing between
	// tuners, not diagnosing one.
	return err == nil && supported
}

func (t *tuner) Tune(properties map[string]string) error { return t.receiver.Tune(properties) }

func (t *tuner) Status() (device.Status, error) {
	status, err := t.receiver.Status()
	if err != nil {
		return device.Status{}, err
	}
	return device.Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      device.Measurement(status.CNR),
		Strength: device.Measurement(status.Strength),
		Quality:  device.Measurement(status.Quality),
	}, nil
}

func (t *tuner) OpenStream() (device.Stream, error) {
	stream, err := t.receiver.OpenStream()
	if err != nil {
		return nil, err
	}
	return &nativeStream{stream: stream}, nil
}

// Close does nothing: the tuner and the board are the same device, and the pool
// closes the board.
func (t *tuner) Close() error { return nil }

type nativeStream struct{ stream receiver.Stream }

func (s *nativeStream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := s.stream.Read(ctx, buffer)
	if err == receiver.ErrWouldBlock {
		// One vocabulary above this line, so no reader knows both sentinels.
		return read, device.ErrWouldBlock
	}
	return read, err
}

func (s *nativeStream) Close() error { return s.stream.Close() }
