// Copyright (c) 2017 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5520se.c) by Davin.

// Package tbs5520se drives the TurboSight TBS 5520SE: an Si2183 combined
// demodulator with a terrestrial Si2157 tuner and a satellite AV201x tuner,
// each wired to its own antenna input, behind a TBS vendor-protocol USB
// bridge that needs its microcontroller firmware uploaded on every open.
//
// The Si2183 has one register set covering both tuning paths, with no second
// demodulator core to switch between, so this is one tuner whose delivery system
// picks the RF input and which of the two tuner chips is programmed.
package tbs5520se

import (
	"context"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/si2183"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuner/av201x"
	"github.com/Till0196/coax/internal/coaxd/tuner/si2157"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs5520se"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "tbs5520se",
	Description: "TurboSight TBS 5520SE (Si2183 + Si2157/AV201x)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{
		Index:      0,
		Name:       "Si2183",
		Deliveries: deliveries,
		// 13V or 18V; the supply itself cannot be switched off on this board.
		Controls: device.Controls{LNB: &device.LNBControl{Low: "13V", High: "18V"}},
	}}
}

// deliveries is what the board's tuner can reach across both RF inputs.
var deliveries = []string{
	"DVB-T", "DVB-T2", "DVB-C", "DVB-C/ANNEX_B", "ISDB-T",
	"DVB-S", "DVB-S2",
}

// firmwareFile is the bridge microcontroller firmware. It is not the
// demodulator's own optional firmware.
const firmwareFile = "dvb-usb-id5520se.fw"

var supportedIDs = []usbio.ID{{Vendor: 0x734c, Product: 0x5521}}

// satelliteSystems are the delivery systems reached through the AV201x on the
// satellite antenna input; every other supported system goes through the
// Si2157 on the terrestrial input.
var satelliteSystems = map[string]bool{
	"DVBS": true, "DVBS2": true,
}

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	switch tuning.DemodDeliverySystem(deliverySystem) {
	case "DVBT", "DVBT2", "ISDBT", "DVBC", "DVBC/ANNEXA", "DVBC/ANNEXB", "DVBS", "DVBS2":
		return true
	default:
		return false
	}
}

// open claims a TBS 5520SE and brings its demodulator and both tuners up.
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

	bridge := tbs5520se.New(transport)
	// The bridge's microcontroller has no persistent storage: every open has to
	// reload it. The board does not re-enumerate afterwards, so its USB ID never
	// signals a warm device the way the ASV52xx boards' does.
	firmware, err := device.ReadFirmware(firmwareDir, firmwareFile)
	if err != nil {
		return fail("read TBS5520SE firmware", err)
	}
	if err := bridge.LoadFirmware(firmware); err != nil {
		return fail("load TBS5520SE firmware", err)
	}

	demod := si2183.New(bridge, firmwareDir, si2183.Config{
		TSMode:        si2183.TSModeParallel,
		TSClockGapped: true,
		AGCMode:       0x05,
	})
	if err := demod.Initialize(); err != nil {
		return fail("initialize Si2183", err)
	}
	terrestrial := si2157.New(bridge, firmwareDir, si2157.Config{Variant: si2157.Si2157, IFPort: 1})
	if err := terrestrial.Initialize(); err != nil {
		return fail("initialize Si2157", err)
	}
	satellite := av201x.New(bridge, av201x.Config{Address: 0x62, Model: av201x.AV2012, XtalKHz: 27000})
	if err := satellite.Initialize(); err != nil {
		return fail("initialize AV201x", err)
	}
	if err := bridge.Initialize(); err != nil {
		return fail("initialize TBS5520SE GPIO", err)
	}

	front := &frontend{bridge: bridge, demod: demod, terrestrial: terrestrial, satellite: satellite}
	board, err := boardkit.New(boardkit.Config{
		Model:    "TurboSight TBS 5520SE",
		Location: transport.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: "Si2183", Deliveries: deliveries}},
		Release:  transport.Close,
		Supports: Supports,
		Open:     func(int) (boardkit.Tuner, error) { return front, nil },
	})
	if err != nil {
		return fail("open", err)
	}
	return board, nil
}

// frontend is the board's single tuner: the Si2183 fed by whichever of the
// Si2157 or AV201x the delivery system selects.
type frontend struct {
	bridge      *tbs5520se.Bridge
	demod       *si2183.Demod
	terrestrial *si2157.Tuner
	satellite   *av201x.Tuner
	delivery    string
}

// Tune locks onto the channel described by the given channel-file properties.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	if !Supports(delivery) {
		return fmt.Errorf("TBS 5520SE does not support %q", properties["DELIVERY_SYSTEM"])
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	if satelliteSystems[delivery] {
		return front.tuneSatellite(delivery, uint32(frequency), properties)
	}
	return front.tuneTerrestrial(delivery, uint32(frequency), properties)
}

func (front *frontend) tuneTerrestrial(delivery string, frequencyHz uint32, properties map[string]string) error {
	bandwidth, err := tuning.BandwidthHz(properties)
	if err != nil {
		return err
	}
	scrambling, err := tuning.Scrambling(properties)
	if err != nil {
		return err
	}
	streamID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}
	// The tuner takes Hz; the Si2157's IF frequency is fixed regardless of
	// delivery system, so there is nothing else to derive here.
	if err := front.terrestrial.Tune(frequencyHz, uint32(bandwidth), delivery); err != nil {
		return fmt.Errorf("tune Si2157: %w", err)
	}
	// Annex A/C is programmed with the channel's symbol rate and cannot do
	// without one. Annex B takes one when it is given and otherwise follows
	// the modulation.
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", tuning.IsJ83AnnexAC(delivery))
	if err != nil {
		return err
	}
	if err := front.demod.Tune(si2183.Params{
		DeliverySystem:  delivery,
		BandwidthHz:     uint32(bandwidth),
		SymbolRate:      uint32(symbolRate),
		Modulation:      properties["MODULATION"],
		StreamID:        streamID,
		ScramblingIndex: scrambling,
	}); err != nil {
		return fmt.Errorf("tune Si2183: %w", err)
	}
	front.delivery = delivery
	return nil
}

func (front *frontend) tuneSatellite(delivery string, frequencyKHz uint32, properties map[string]string) error {
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", true)
	if err != nil {
		return err
	}
	scrambling, err := tuning.Scrambling(properties)
	if err != nil {
		return err
	}
	streamID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}
	// The voltage is the polarization, and the bridge chooses between the two
	// and nothing else -- the supply itself is wired on. A channel asking for
	// none is told so rather than quietly fed 13 V.
	supply, err := tuning.SatelliteSupply(properties)
	if err != nil {
		return err
	}
	// A channel that said nothing is given no supply, which this board cannot
	// do; tuning.ApplySupply turns the refusal below back into the voltage it
	// would have used, so only a channel that asked is told.
	if err := tuning.ApplySupply(supply, func(high, off bool) error {
		if off {
			return fmt.Errorf("%w: the TBS 5520SE selects between 13 V and 18 V and cannot cut the supply", tuning.ErrNoLNBSwitch)
		}
		return front.bridge.SetVoltage(high)
	}); err != nil {
		return fmt.Errorf("set LNB voltage: %w", err)
	}
	// The tone is the demodulator's rather than the bridge's here: the Si2183
	// does it through its DiSEqC command, the only way to ask for it.
	if err := front.demod.SetTone(tuning.ToneWanted(properties)); err != nil {
		return fmt.Errorf("set the 22 kHz tone: %w", err)
	}
	// AV201x.Tune wants Hz; the channel file's satellite FREQUENCY is kHz.
	if err := front.satellite.Tune(frequencyKHz*1000, uint32(symbolRate)); err != nil {
		return fmt.Errorf("tune AV201x: %w", err)
	}
	if err := front.demod.Tune(si2183.Params{
		DeliverySystem:  delivery,
		SymbolRate:      uint32(symbolRate),
		StreamID:        streamID,
		ScramblingIndex: scrambling,
	}); err != nil {
		return fmt.Errorf("tune Si2183: %w", err)
	}
	front.delivery = delivery
	return nil
}

// Status reports the demodulator lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if front.delivery == "" {
		return device.Status{}, nil
	}
	status, err := front.demod.Status()
	if err != nil {
		return device.Status{}, err
	}
	result := device.Status{Locked: status.Locked, Names: status.Names}
	if status.HasCNR {
		result.CNR = device.Measured(status.CNRdB)
	}
	return result, nil
}

// StartStream only puts the reads in flight: this bridge's bulk endpoint is
// never gated, so the stream is already flowing once the frontend is tuned.
func (front *frontend) StartStream() error { return front.bridge.StartReading() }

// StopStream only withdraws the reads still outstanding, which would otherwise
// carry the last channel's packets into the next stream.
func (front *frontend) StopStream() error { return front.bridge.StopReading() }

// Read takes the next packets off the bulk endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, 100))
}
