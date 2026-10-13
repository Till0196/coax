// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package receiver

import (
	"context"
	"errors"

	"github.com/Till0196/coax/internal/coaxd/pcie/pt1"
	"github.com/Till0196/coax/internal/coaxd/pcie/pt3"
)

// openEarthsoft opens one of the cards this program drives itself.
//
// It is here rather than beside the other backends because these two are not
// like them: no vendor driver knows what the card is, so everything from the
// bring-up to the transfer is this program's. See internal/coaxd/pcie/windriver.
func openEarthsoft(backend string, options Options) (Device, bool, error) {
	switch backend {
	case pt3.Backend:
		device, err := pt3.Open(options.Device)
		if err != nil {
			return nil, true, err
		}
		return &windowsPT3Device{device: device}, true, nil
	case pt1.Backend:
		device, err := pt1.Open(options.Device)
		if err != nil {
			return nil, true, err
		}
		return &windowsPT1Device{device: device}, true, nil
	}
	return nil, false, nil
}

// windowsPT3Device is an Earthsoft PT3, which has no vendor driver of its own
// worth the name: what Windows has for it is a generic PCI driver that maps
// the card's registers, and everything else -- the bring-up, the tuners, the
// transfer engine -- is this program's. So unlike the other two backends here
// there is no BDA graph anywhere in it, and the transport stream comes off the
// card's own ring rather than out of a file a graph is writing.
type windowsPT3Device struct {
	device *pt3.Device
}

func (device *windowsPT3Device) Backend() string { return pt3.Backend }
func (device *windowsPT3Device) Name() string    { return device.device.Name() }
func (device *windowsPT3Device) Close() error    { return device.device.Close() }

func (device *windowsPT3Device) Supports(deliverySystem string) (bool, error) {
	return device.device.Supports(deliverySystem), nil
}

func (device *windowsPT3Device) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *windowsPT3Device) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      Measurement(status.CNR),
		Strength: Measurement(status.Strength),
	}, err
}

func (device *windowsPT3Device) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &windowsPT3Stream{stream: stream}, nil
}

type windowsPT3Stream struct {
	stream *pt3.Stream
}

func (stream *windowsPT3Stream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := stream.stream.Read(ctx, buffer)
	if errors.Is(err, pt3.ErrWouldBlock) {
		return read, ErrWouldBlock
	}
	return read, err
}

func (stream *windowsPT3Stream) Close() error { return stream.stream.Close() }

// windowsPT1Device is an Earthsoft PT1 or PT2, driven the same way as the PT3:
// there is no vendor driver worth the name, only a generic PCI one that maps
// the card's registers, so everything above that is this program's.
//
// What the two cards do not share is anything below that. See
// internal/coaxd/pcie/pt1.
type windowsPT1Device struct {
	device *pt1.Device
}

func (device *windowsPT1Device) Backend() string { return pt1.Backend }
func (device *windowsPT1Device) Name() string    { return device.device.Name() }
func (device *windowsPT1Device) Close() error    { return device.device.Close() }

func (device *windowsPT1Device) Supports(deliverySystem string) (bool, error) {
	return device.device.Supports(deliverySystem), nil
}

func (device *windowsPT1Device) Tune(properties map[string]string) error {
	return device.device.Tune(properties)
}

func (device *windowsPT1Device) Status() (Status, error) {
	status, err := device.device.Status()
	return Status{
		Locked:   status.Locked,
		Names:    status.Names,
		CNR:      Measurement(status.CNR),
		Strength: Measurement(status.Strength),
	}, err
}

func (device *windowsPT1Device) OpenStream() (Stream, error) {
	stream, err := device.device.OpenStream()
	if err != nil {
		return nil, err
	}
	return &windowsPT1Stream{stream: stream}, nil
}

type windowsPT1Stream struct {
	stream *pt1.Stream
}

func (stream *windowsPT1Stream) Read(ctx context.Context, buffer []byte) (int, error) {
	read, err := stream.stream.Read(ctx, buffer)
	if errors.Is(err, pt1.ErrWouldBlock) {
		return read, ErrWouldBlock
	}
	return read, err
}

func (stream *windowsPT1Stream) Close() error { return stream.stream.Close() }
