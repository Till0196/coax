// Copyright (c) 2022 Davin; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-only
//
// Ported from TBS linux_media (tbs5530.c) by Davin.

// Package tbs5530 drives the TurboSight TBS 5530: a CXD2878ER covering
// terrestrial and cable with its own embedded tuner, and an M88RS6060 covering
// satellite with its own, behind one TBS vendor-protocol USB bridge that needs
// its microcontroller firmware uploaded on every open.
//
// The board is one tuner, not two: both demodulators feed a single bulk
// endpoint through a selector on the bridge — a switch, not a multiplexer, so
// the unselected demodulator's stream never reaches the host at all. The
// channel's delivery system decides which demodulator is programmed and which
// way the selector points.
//
// The terrestrial half covers DVB-T/T2/C, ISDB-T, ATSC and J.83B. DVB-C2 and
// ISDB-C are left out: neither has a wake-up path.
package tbs5530

import (
	"context"
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/demod/cxd2878"
	"github.com/Till0196/coax/internal/coaxd/demod/m88rs6060"
	"github.com/Till0196/coax/internal/coaxd/demod/sony"
	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/tbs5530"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/pkg/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "tbs5530",
	Description: "TurboSight TBS 5530 (CXD2878ER + M88RS6060)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement, so a listing need not open (and
// power up) the board.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{
		Index:      0,
		Name:       "CXD2878ER / M88RS6060",
		Deliveries: deliveries,
		// The M88RS6060's regulator: 13V, 18V or nothing.
		Controls: device.Controls{LNB: &device.LNBControl{Off: true, Low: "13V", High: "18V"}},
	}}
}

// terrestrialDeliveries is what the CXD2878ER reaches.
var terrestrialDeliveries = []string{
	"DVB-T", "DVB-T2", "DVB-C", "DVB-C/ANNEX_B", "ISDB-T", "ATSC",
}

// satelliteDeliveries is what the M88RS6060 reaches. DVB-S2X shares the
// DVB-S2 register path, so only the two are declared.
var satelliteDeliveries = []string{"DVB-S", "DVB-S2"}

// deliveries is what the board's one tuner can reach across both antenna
// inputs, which is everything either demodulator does.
var deliveries = append(append([]string(nil), terrestrialDeliveries...), satelliteDeliveries...)

// firmwareFile is the bridge microcontroller firmware. The M88RS6060 has its
// own separate demodulator firmware, loaded by its own package.
const firmwareFile = "dvb-usb-id5530.fw"

var supportedIDs = []usbio.ID{{Vendor: 0x734c, Product: 0x5530}}

// Supports reports whether either of the board's demodulators can tune a
// delivery system.
func Supports(deliverySystem string) bool {
	return supportsTerrestrial(deliverySystem) || supportsSatellite(deliverySystem)
}

func supportsTerrestrial(deliverySystem string) bool {
	switch tuning.DemodDeliverySystem(deliverySystem) {
	case "DVBT", "DVBT2", "DVBC", "DVBC/ANNEXA", "DVBC/ANNEXB", "ISDBT", "ATSC":
		return true
	default:
		return false
	}
}

func supportsSatellite(deliverySystem string) bool {
	switch tuning.Normalize(deliverySystem) {
	case "DVBS", "DVBS2", "DVBS2X":
		return true
	default:
		return false
	}
}

// open claims a TBS 5530 and brings both demodulators up.
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

	bridge := tbs5530.New(transport)
	// The bridge's microcontroller has no persistent storage: every open has
	// to reload it.
	firmware, err := device.ReadFirmware(firmwareDir, firmwareFile)
	if err != nil {
		return fail("read TBS5530 firmware", err)
	}
	if err := bridge.LoadFirmware(firmware); err != nil {
		return fail("load TBS5530 firmware", err)
	}

	// CXD2878ER: demodulator at 0x64 on a 24 MHz crystal, embedded tuner at
	// 0x60 on its own 24 MHz crystal.
	terrestrialDemod := cxd2878.New(bridge, sony.Config{
		DemodAddress: 0x64,
		TunerAddress: 0x60,
		XtalCode:     1, // SONY_DEMOD_XTAL_24000KHz
		TunerXtal:    sony.TunerXtal24000KHz,
		TSClockMask:  1,
	})
	if err := terrestrialDemod.Initialize(); err != nil {
		return fail("initialize CXD2878ER", err)
	}

	// M88RS6060: demodulator at 0x69, embedded tuner at 0x2c, 27 MHz clock,
	// common (CI-style) TS output.
	satelliteDemod := m88rs6060.New(bridge, firmwareDir, m88rs6060.Config{
		DemodAddress: 0x69,
		TunerAddress: 0x2c,
		ClockHz:      27_000_000,
		TSMode:       m88rs6060.TSModeCommon,
	})
	if err := bridge.InitializeSatellite(); err != nil {
		return fail("initialize TBS5530 satellite GPIO", err)
	}
	if err := satelliteDemod.Initialize(); err != nil {
		return fail("initialize M88RS6060", err)
	}

	front := &frontend{bridge: bridge, terrestrial: terrestrialDemod, satellite: satelliteDemod}

	handle, err := boardkit.New(boardkit.Config{
		Model:    "TurboSight TBS 5530",
		Location: transport.Name(),
		Tuners:   Layout(usbio.ID{}),
		Release:  transport.Close,
		Supports: Supports,
		Open:     func(int) (boardkit.Tuner, error) { return front, nil },
	})
	if err != nil {
		return fail("open", err)
	}
	return handle, nil
}

// frontend is the board's single tuner: whichever of the two demodulators the
// channel's delivery system calls for. Nothing here locks — boardkit serialises
// one tuner's Tune, Status and stream handling, and only Read runs alongside
// them, touching the bulk endpoint rather than either demodulator.
type frontend struct {
	bridge      *tbs5530.Bridge
	terrestrial *cxd2878.Demod
	satellite   *m88rs6060.Demod
	// onSatellite is which demodulator the last tune programmed, and so which
	// way the bridge's selector has to point when the stream starts.
	onSatellite bool
	delivery    string
	frequency   uint32 // kHz, for the signal strength reading
}

// Tune locks onto the channel described by the given channel-file properties,
// on whichever demodulator reaches its delivery system.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := tuning.Normalize(properties["DELIVERY_SYSTEM"])
	switch {
	case supportsSatellite(delivery):
		return front.tuneSatellite(delivery, properties)
	case supportsTerrestrial(delivery):
		return front.tuneTerrestrial(delivery, properties)
	default:
		return fmt.Errorf("TBS 5530 does not support %q", properties["DELIVERY_SYSTEM"])
	}
}

// system maps a channel file's delivery system to the demodulator's own.
func system(delivery string) (sony.System, error) {
	switch tuning.DemodDeliverySystem(delivery) {
	case "DVBT":
		return sony.SystemDVBT, nil
	case "DVBT2":
		return sony.SystemDVBT2, nil
	case "DVBC", "DVBC/ANNEXA":
		return sony.SystemDVBC, nil
	case "DVBC/ANNEXB":
		return sony.SystemJ83B, nil
	case "ISDBT":
		return sony.SystemISDBT, nil
	case "ATSC":
		return sony.SystemATSC, nil
	default:
		return sony.SystemUnspecified, fmt.Errorf("TBS 5530 terrestrial demodulator does not support %q", delivery)
	}
}

func (front *frontend) tuneTerrestrial(delivery string, properties map[string]string) error {
	target, err := system(delivery)
	if err != nil {
		return err
	}
	frequencyHz, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	// ATSC and J.83B have fixed channel widths; the rest need one.
	var bandwidthMHz uint32
	if target != sony.SystemATSC && target != sony.SystemJ83B {
		bandwidthHz, err := tuning.BandwidthHz(properties)
		if err != nil {
			return err
		}
		bandwidthMHz = sony.BandwidthMHz(uint32(bandwidthHz))
	}
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", target == sony.SystemJ83B)
	if err != nil {
		return err
	}
	plpID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}

	if err := front.terrestrial.Wakeup(target, sony.WakeupParams{
		BandwidthMHz:  bandwidthMHz,
		PLPID:         plpID,
		SymbolRateSPS: uint32(symbolRate),
	}); err != nil {
		return fmt.Errorf("wake the CXD2878ER for %s: %w", target, err)
	}
	// The demodulator's Tune drives its embedded tuner, and takes kHz.
	frequencyKHz := uint32(frequencyHz / 1000)
	if err := front.terrestrial.Tune(frequencyKHz); err != nil {
		return fmt.Errorf("tune the CXD2878ER: %w", err)
	}
	front.onSatellite = false
	front.delivery = delivery
	front.frequency = frequencyKHz
	return nil
}

func (front *frontend) tuneSatellite(delivery string, properties map[string]string) error {
	// A satellite channel file's FREQUENCY is in kHz.
	frequencyKHz, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	symbolRate, err := tuning.Number(properties, "SYMBOL_RATE", true)
	if err != nil {
		return err
	}
	streamID, err := tuning.PLP(properties)
	if err != nil {
		return err
	}

	// Unlike the TBS 5520SE, whose LNB supply is a bridge GPIO, this board's
	// regulator is the demodulator's own.
	// The voltage is the polarization, so the two settings land on one knob:
	// polarization wins where a channel gives one, and asking for no supply wins
	// over both. See tuning.SatelliteSupply.
	supply, err := tuning.SatelliteSupply(properties)
	if err != nil {
		return err
	}
	if err := tuning.ApplySupply(supply, front.satellite.SetVoltage); err != nil {
		return fmt.Errorf("set the LNB voltage: %w", err)
	}
	// The tone follows the voltage: a channel naming a low-noise block has had
	// one worked out for it.
	if err := front.satellite.SetTone(tuning.ToneWanted(properties)); err != nil {
		return fmt.Errorf("set the 22 kHz tone: %w", err)
	}
	if err := front.satellite.Tune(m88rs6060.Params{
		DeliverySystem: delivery,
		FrequencyKHz:   uint32(frequencyKHz),
		SymbolRate:     uint32(symbolRate),
		StreamID:       int32(streamID),
	}); err != nil {
		return fmt.Errorf("tune the M88RS6060: %w", err)
	}
	front.onSatellite = true
	front.delivery = delivery
	front.frequency = uint32(frequencyKHz)
	return nil
}

// Status reports the lock state of whichever demodulator the last tune used.
func (front *frontend) Status() (device.Status, error) {
	if front.delivery == "" {
		return device.Status{}, nil
	}
	if front.onSatellite {
		return front.satelliteStatus()
	}
	return front.terrestrialStatus()
}

func (front *frontend) terrestrialStatus() (device.Status, error) {
	lock, err := front.terrestrial.Lock()
	if err != nil {
		return device.Status{}, err
	}
	result := device.Status{Locked: lock.Locked}
	if lock.Synced {
		result.Names = append(result.Names, "SIGNAL", "CARRIER")
	}
	if lock.Locked {
		result.Names = append(result.Names, "VITERBI", "SYNC", "LOCK")
	}
	if strength, err := front.terrestrial.SignalStrength(front.frequency); err == nil {
		result.Strength = device.Measured(strength)
	}
	if lock.Locked {
		if cnr, err := front.terrestrial.CNR(); err == nil {
			result.CNR = device.Measured(cnr)
		}
	}
	return result, nil
}

func (front *frontend) satelliteStatus() (device.Status, error) {
	status, err := front.satellite.Status()
	if err != nil {
		return device.Status{}, err
	}
	result := device.Status{Locked: status.Locked}
	if status.Locked {
		result.Names = device.LockNames
	}
	if status.HasCNR {
		result.CNR = device.Measured(status.CNRdB)
	}
	if strength, err := front.satellite.SignalStrength(front.frequency); err == nil {
		result.Strength = device.Measured(strength)
	}
	return result, nil
}

// StartStream points the bulk endpoint at the demodulator the last tune used.
func (front *frontend) StartStream() error {
	if err := front.bridge.SelectStreamSource(front.onSatellite); err != nil {
		return err
	}
	return front.bridge.StartReading()
}

// StopStream only withdraws the reads still outstanding: the endpoint is
// switched, never gated, and the next tune moves the selector.
func (front *frontend) StopStream() error { return front.bridge.StopReading() }

// Read takes the next packets off the bulk endpoint.
func (front *frontend) Read(_ context.Context, buffer []byte) (int, error) {
	return boardkit.WouldBlock(front.bridge.ReadStream(buffer, 100))
}
