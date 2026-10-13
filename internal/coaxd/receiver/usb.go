// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package receiver

import (
	"context"
	"errors"

	"github.com/Till0196/coax/internal/coaxd/usb"
)

// openUSB claims a USB board when the requested backend names one. The second
// result reports whether the backend was a USB one at all, so each platform's
// Open can fall through to its native backend.
func openUSB(name string, options Options) (Device, bool, error) {
	backend := usb.Lookup(name)
	if backend == nil {
		return nil, false, nil
	}
	device, err := usb.Open(backend.Name, usbOptions(options))
	if err != nil {
		return nil, true, err
	}
	return &usbDevice{backend: backend.Name, device: device}, true, nil
}

func usbOptions(options Options) usb.Options {
	return usb.Options{
		Selector:    options.Device,
		Tuner:       options.Tuner,
		FirmwareDir: options.FirmwareDir,
	}
}

// USBBackends names the USB boards this build supports, for help output.
func USBBackends() []string { return usb.Names() }

// OpenUSBTuners claims every tuner of a USB board that can reach the given
// delivery system, so a scan can run across them in parallel. Pass an empty
// delivery system to take them all. It reports false when the backend does not
// name a USB board.
func OpenUSBTuners(options Options, deliverySystem string) ([]Device, bool, error) {
	backend := usb.Lookup(options.Backend)
	if backend == nil {
		return nil, false, nil
	}
	opened, err := usb.OpenAll(backend.Name, usbOptions(options), deliverySystem)
	if err != nil {
		return nil, true, err
	}
	devices := make([]Device, 0, len(opened))
	for _, device := range opened {
		devices = append(devices, &usbDevice{backend: backend.Name, device: device})
	}
	return devices, true, nil
}

type usbDevice struct {
	backend string
	device  usb.Device
}

func (device *usbDevice) Backend() string { return device.backend }
func (device *usbDevice) Name() string    { return device.device.Name() }
func (device *usbDevice) Close() error    { return device.device.Close() }

func (device *usbDevice) Supports(deliverySystem string) (bool, error) {
	return device.device.Supports(deliverySystem), nil
}

func (device *usbDevice) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *usbDevice) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      Measurement(status.CNR),
		Strength: Measurement(status.Strength),
	}, err
}

func (device *usbDevice) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &usbStream{stream: stream}, nil
}

type usbStream struct{ stream usb.Stream }

func (stream *usbStream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := stream.stream.Read(ctx, buffer)
	if errors.Is(err, usb.ErrWouldBlock) {
		return read, ErrWouldBlock
	}
	return read, err
}

func (stream *usbStream) Close() error { return stream.stream.Close() }
