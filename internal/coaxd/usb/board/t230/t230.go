// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package t230 drives the MyGica T230 family: a DVBSky USB bridge carrying an
// Si2168 demodulator with a Silicon Labs tuner on its gated bus. T230C, T230C
// Lite, T230A, T230C2 and T230C2 Lite carry an Si2141 on IF port 0; the plain
// T230 carries an Si2157 on IF port 1. Both tuners are one driver, so the USB
// product ID is all that has to change between them.
package t230

import (
	"context"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/si2168"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/i2c"
	"github.com/Till0196/coax/internal/coaxd/tuner/si2157"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/dvbsky"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "t230",
	Description: "MyGica T230 / T230C / T230A / T230C2 (Si2168 + Si2141/Si2157)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's single tuner. Which Silicon Labs part is fitted
// follows from the product ID, so a listing can say before anything is
// opened.
func Layout(id usbio.ID) []device.TunerInfo {
	product, ok := products[id.Product]
	if !ok {
		return nil
	}
	return []device.TunerInfo{{Index: 0, Name: product.tuner.Variant.String(), Deliveries: deliveries}}
}

// deliveries is what the board's single tuner can reach.
var deliveries = []string{"DVB-T", "DVB-T2", "DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C", "ISDB-C"}

// products maps each supported USB product ID to its board name, the tuner it
// carries and whether the Si2168 needs the TS_CLK_MANUAL sequence. T230A and
// the T230C2 pair need it; the plain T230, T230C and T230C Lite don't.
var products = map[uint16]struct {
	name          string
	tuner         si2157.Config
	tsClockManual bool
}{
	0xc688: {"MyGica T230", si2157.Config{Variant: si2157.Si2157, IFPort: 1}, false},
	0xc689: {"MyGica T230C", si2157.Config{Variant: si2157.Si2141, IFPort: 0}, false},
	0xc699: {"MyGica T230C Lite", si2157.Config{Variant: si2157.Si2141, IFPort: 0}, false},
	0x689a: {"MyGica T230A", si2157.Config{Variant: si2157.Si2141, IFPort: 0}, true},
	0xc68a: {"MyGica T230C2", si2157.Config{Variant: si2157.Si2141, IFPort: 0}, true},
	0xc69a: {"MyGica T230C2 Lite", si2157.Config{Variant: si2157.Si2141, IFPort: 0}, true},
}

var supportedIDs = usbio.IDs(0x0572, products)

// Supports reports whether the board can tune a delivery system. The Si2168
// decides: the tuner covers everything the demodulator does.
func Supports(deliverySystem string) bool { return si2168.Supports(deliverySystem) }

// open claims a T230C-family stick and brings its demodulator and tuner up.
func open(options device.Options) (device.Board, error) {
	firmwareDir := options.FirmwareDirOrDefault()
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	fail := func(operation string, cause error) (device.Board, error) {
		_ = transport.Close()
		return nil, fmt.Errorf("%s: %w", operation, cause)
	}
	product, ok := products[transport.ProductID()]
	if !ok {
		return fail("open", fmt.Errorf("unsupported product ID %#04x", transport.ProductID()))
	}
	bridge := dvbsky.New(transport)
	if err := bridge.Reset(); err != nil {
		return fail("reset "+product.name, err)
	}
	demod := si2168.New(bridge, firmwareDir, product.tsClockManual)
	if err := demod.Initialize(); err != nil {
		return fail("initialize Si2168", err)
	}
	tuner := si2157.New(i2c.Gated(bridge, demod), firmwareDir, product.tuner)
	if err := tuner.Initialize(); err != nil {
		return fail("initialize "+product.tuner.Variant.String(), err)
	}

	front := &frontend{bridge: bridge, demod: demod, tuner: tuner, model: product.name}
	board, err := boardkit.New(boardkit.Config{
		Model:    product.name,
		Location: transport.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: product.tuner.Variant.String(), Deliveries: deliveries}},
		Release:  transport.Close,
		Supports: Supports,
		Open:     func(int) (boardkit.Tuner, error) { return front, nil },
	})
	if err != nil {
		return fail("open", err)
	}
	return board, nil
}

// frontend is the board's single tuner: the Si2141 or Si2157 in front of the
// Si2168.
type frontend struct {
	bridge   *dvbsky.Bridge
	demod    *si2168.Demod
	tuner    *si2157.Tuner
	model    string
	delivery string
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := properties["DELIVERY_SYSTEM"]
	if !Supports(delivery) {
		return fmt.Errorf("%s does not support %q", front.model, delivery)
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	// A J.83 channel file names a symbol rate rather than a bandwidth, so the
	// bandwidth is derived rather than demanded.
	bandwidth, err := tuning.BandwidthHz(properties)
	if err != nil {
		return err
	}
	// Every J.83 Annex A/C spelling needs a symbol rate: it is what the
	// Si2168's DVB-C mode is programmed with, not merely how the bandwidth is
	// derived.
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", tuning.IsJ83AnnexAC(delivery))
	if err != nil {
		return err
	}
	streamID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}
	if err := front.tuner.Tune(uint32(frequency), uint32(bandwidth), delivery); err != nil {
		return fmt.Errorf("tune %s: %w", front.tuner.Variant(), err)
	}
	parameters := si2168.Params{
		DeliverySystem: delivery,
		BandwidthHz:    uint32(bandwidth),
		SymbolRate:     uint32(symbolRate),
		// Annex B derives its symbol rate from the constellation, so the
		// modulation matters there even though the other systems ignore it.
		Modulation: properties["MODULATION"],
		StreamID:   streamID,
	}
	if err := front.demod.Tune(parameters); err != nil {
		return fmt.Errorf("tune Si2168: %w", err)
	}
	front.delivery = delivery
	return nil
}

// Status reports the demodulator lock state for the last tuned channel, along
// with the signal measurements the two chips can make.
func (front *frontend) Status() (device.Status, error) {
	if front.delivery == "" {
		return device.Status{}, nil
	}
	demodStatus, err := front.demod.Status()
	if err != nil {
		return device.Status{}, err
	}
	status := device.Status{Locked: demodStatus.Locked, Names: demodStatus.Names}
	if demodStatus.HasCNR {
		status.CNR = device.Measured(demodStatus.CNRdB)
	}
	// A failure to read the level says nothing about the lock already
	// established, so it is left unmeasured rather than made a status error.
	if strength, strengthErr := front.tuner.SignalStrength(); strengthErr == nil {
		status.Strength = device.Measured(strength)
	}
	return status, nil
}

// StartStream opens the bridge's stream endpoint.
func (front *frontend) StartStream() error { return front.bridge.Stream(true) }

// StopStream closes it again.
func (front *frontend) StopStream() error { return front.bridge.Stream(false) }

// Read takes the next packets off the endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, 100))
}
