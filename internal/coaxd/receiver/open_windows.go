// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package receiver

import (
	"context"
	"errors"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/pcie/dd"
	"github.com/Till0196/coax/internal/coaxd/pcie/tbs"
)

func Open(options Options) (Device, error) {
	backend := options.Backend
	if backend == "" || backend == "auto" {
		backend = "bda"
	}
	if device, handled, err := openUSB(backend, options); handled {
		return device, err
	}
	if backend == dd.Backend {
		device, err := dd.Open(options.Device)
		if err != nil {
			return nil, err
		}
		return &windowsDDDevice{device: device}, nil
	}
	if device, handled, err := openEarthsoft(backend, options); handled {
		return device, err
	}
	if backend == tbs.Backend {
		device, err := tbs.Open(options.Device, options.FirmwareDir)
		if err != nil {
			return nil, err
		}
		return &windowsTBSDevice{device: device}, nil
	}
	if backend != "bda" {
		return nil, fmt.Errorf("backend %q is not available on Windows", backend)
	}
	device, err := bda.Open(options.Device)
	if err != nil {
		return nil, err
	}
	return &windowsBDADevice{device: device}, nil
}

type windowsBDADevice struct {
	device *bda.Device
}

func (device *windowsBDADevice) Backend() string { return "bda" }
func (device *windowsBDADevice) Name() string    { return device.device.Name() }
func (device *windowsBDADevice) Close() error    { return device.device.Close() }

// Settings is what the driver says about how the signal is being demodulated.
func (device *windowsBDADevice) Settings() [][2]string { return device.device.Settings() }

func (device *windowsBDADevice) Supports(deliverySystem string) (bool, error) {
	return bda.Supports(deliverySystem), nil
}

func (device *windowsBDADevice) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *windowsBDADevice) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		Strength: Measurement(status.Strength),
		Quality:  Measurement(status.Quality),
	}, err
}

func (device *windowsBDADevice) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &windowsBDAStream{stream: stream}, nil
}

type windowsBDAStream struct {
	stream *bda.Stream
}

func (stream *windowsBDAStream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := stream.stream.Read(ctx, buffer)
	if errors.Is(err, bda.ErrWouldBlock) {
		return read, ErrWouldBlock
	}
	return read, err
}

func (stream *windowsBDAStream) Close() error {
	return stream.stream.Close()
}

// windowsTBSDevice is a TBS PCIe tuner driven directly. Its status comes off
// the demodulator rather than out of BDA, which is why it reports a real
// carrier-to-noise ratio where the BDA path can only offer a quality figure.
type windowsTBSDevice struct {
	device *tbs.Device
}

func (device *windowsTBSDevice) Backend() string { return tbs.Backend }
func (device *windowsTBSDevice) Name() string    { return device.device.Name() }
func (device *windowsTBSDevice) Close() error    { return device.device.Close() }

// Mode is the card-wide demodulator mode. See tbs.Device.Mode.
func (device *windowsTBSDevice) Mode() string { return device.device.Mode() }

// Settings is what the driver says about how the signal is being demodulated.
func (device *windowsTBSDevice) Settings() [][2]string { return device.device.Settings() }

func (device *windowsTBSDevice) Supports(deliverySystem string) (bool, error) {
	return device.device.Supports(deliverySystem), nil
}

func (device *windowsTBSDevice) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *windowsTBSDevice) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      Measurement(status.CNR),
		Strength: Measurement(status.Strength),
	}, err
}

func (device *windowsTBSDevice) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &windowsBDAStream{stream: stream}, nil
}

// windowsDDDevice is a Digital Devices tuner driven through the card's own
// command interface. Everything but the transport stream goes there rather
// than through BDA, so its status is the firmware's own view of acquisition.
type windowsDDDevice struct {
	device *dd.Device
}

func (device *windowsDDDevice) Backend() string { return dd.Backend }
func (device *windowsDDDevice) Name() string    { return device.device.Name() }
func (device *windowsDDDevice) Close() error    { return device.device.Close() }

// Settings is what the driver says about how the signal is being demodulated.
//
// It comes off the BDA demodulator node rather than the card's own command
// interface, which is the only way to ask this while a stream is running: the
// command interface is shared with the driver, and reaching it mid-stream
// stops the stream. See bda.Device.Settings.
func (device *windowsDDDevice) Settings() [][2]string { return device.device.Settings() }

func (device *windowsDDDevice) Supports(deliverySystem string) (bool, error) {
	return device.device.Supports(deliverySystem), nil
}

func (device *windowsDDDevice) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *windowsDDDevice) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      Measurement(status.CNR),
		Strength: Measurement(status.Strength),
	}, err
}

func (device *windowsDDDevice) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &windowsBDAStream{stream: stream}, nil
}
