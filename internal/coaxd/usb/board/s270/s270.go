// Copyright (c) 2005-2009 Siano Mobile Silicon; 2008 Michael Krufky; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from the Linux siano driver (sms-cards.c, smsdvb-main.c).

// Package s270 drives the Siano "Rio" ISDB-T sticks: the MyGica S270 and the
// PLEX PX-S1UD it is the OEM original of.
//
// The whole receiver is one chip running downloadable firmware, so this package
// carries no demodulator or tuner driver: it names the firmware image, puts the
// chip into ISDB-T mode and builds the tuning request. The message protocol
// itself lives in the siano bridge package.
package s270

import (
	"context"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/tuning"
	"github.com/Till0196/coax/internal/coaxd/usb/boardkit"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/siano"
	"github.com/Till0196/coax/internal/coaxd/usb/usbdev"
	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

// Backend registers the board with the parent registry.
var Backend = usbdev.Backend{
	Name:        "s270",
	Aliases:     []string{"pxs1ud", "s1ud", "rio"},
	Description: "MyGica S270, PLEX PX-S1UD (Siano Rio)",
	Deliveries:  deliveries,
	IDs:         supportedIDs,
	Supports:    Supports,
	Layout:      Layout,
	Open:        open,
}

// Layout is the board's tuner arrangement, the same on every product this
// backend claims.
func Layout(usbio.ID) []device.TunerInfo {
	return []device.TunerInfo{{Index: 0, Name: "Siano Rio", Deliveries: deliveries}}
}

// deliveries is what the board's single tuner can reach. The Rio serves one at
// a time: each has its own firmware image, and tuning the other one reflashes
// the chip.
var deliveries = []string{"ISDB-T", "DVB-T"}

// The images the Rio needs, one per delivery system. Both ship with the vendor
// driver packages and with linux-firmware, and neither is included here.
const (
	isdbtFirmware = "isdbt_rio.inp"
	dvbtFirmware  = "dvb_rio.inp"
)

// mode describes how one delivery system is driven: a different image, a
// different tuning message and payload, and statistics in a different shape.
type mode struct {
	device       int
	firmware     string
	tuneRequest  uint16
	tuneResponse uint16
	// statisticsRequest and demodLockedOffset locate the demodulator lock in
	// the statistics answer. The ISDB-T reply carries a type and a size ahead
	// of the lock flags that the DVB-T one does not.
	statisticsRequest  uint16
	statisticsResponse uint16
	demodLockedOffset  int
}

var modes = map[string]mode{
	"ISDBT": {
		device: siano.ModeISDBTBDA, firmware: isdbtFirmware,
		tuneRequest: siano.MsgISDBTTuneReq, tuneResponse: siano.MsgISDBTTuneRes,
		statisticsRequest: siano.MsgGetStatisticsExReq, statisticsResponse: siano.MsgGetStatisticsExRes,
		demodLockedOffset: 16,
	},
	"DVBT": {
		device: siano.ModeDVBTBDA, firmware: dvbtFirmware,
		tuneRequest: siano.MsgRFTuneReq, tuneResponse: siano.MsgRFTuneRes,
		statisticsRequest: siano.MsgGetStatisticsReq, statisticsResponse: siano.MsgGetStatisticsRes,
		demodLockedOffset: 12,
	},
}

// dvbtBandwidths maps a channel width in MHz onto the firmware's code.
var dvbtBandwidths = map[uint64]uint32{
	8: siano.Bandwidth8MHz,
	7: siano.Bandwidth7MHz,
	6: siano.Bandwidth6MHz,
}

// products maps each supported USB product ID to its board name. All of them
// are the same Rio silicon; only the branding differs.
var products = map[usbio.ID]string{
	{Vendor: 0x187f, Product: 0x0600}: "MyGica S270",
	{Vendor: 0x187f, Product: 0x0601}: "MyGica S270",
	{Vendor: 0x3275, Product: 0x0080}: "PLEX PX-S1UD",
}

var supportedIDs = func() []usbio.ID {
	ids := make([]usbio.ID, 0, len(products))
	for id := range products {
		ids = append(ids, id)
	}
	return ids
}()

// Supports reports whether the board can tune a delivery system.
func Supports(deliverySystem string) bool {
	_, ok := modes[tuning.Normalize(deliverySystem)]
	return ok
}

// open claims a Rio stick. The firmware is not downloaded here: which image is
// wanted follows from the channel being tuned, and loading one at open would
// flash the chip twice for anyone scanning the other delivery system.
func open(options device.Options) (device.Board, error) {
	transport, err := usbio.Open(options.Selector, supportedIDs)
	if err != nil {
		return nil, err
	}
	bridge, err := siano.New(transport)
	if err != nil {
		_ = transport.Close()
		return nil, err
	}
	// The stick keeps its USB ID across the firmware download, so the name is
	// settled once here.
	model, known := products[bridge.ID()]
	if !known {
		model = "Siano Rio"
	}
	board, err := boardkit.New(boardkit.Config{
		Model:    model,
		Location: bridge.Name(),
		Tuners:   []device.TunerInfo{{Index: 0, Name: "Siano Rio", Deliveries: deliveries}},
		Release:  bridge.Close,
		Supports: Supports,
		Open: func(int) (boardkit.Tuner, error) {
			return &frontend{siano: bridge, model: model, options: options}, nil
		},
	})
	if err != nil {
		_ = bridge.Close()
		return nil, fmt.Errorf("open: %w", err)
	}
	return board, nil
}

// referenceHz is the reference clock the tuning requests name; both delivery
// systems take the same value.
const referenceHz = 12_000_000

// frontend is the board's single tuner. There is no separate demodulator or
// tuner IC: everything is a firmware message.
type frontend struct {
	siano   *siano.Device
	model   string
	options device.Options
	// mode is the delivery system the running firmware serves, held so that
	// the statistics request matches what was tuned.
	mode  mode
	tuned bool
	// leftover holds what did not fit in the last Read's buffer.
	leftover []byte
}

// Tune locks onto the channel described by the given channel-file properties.
// The running image has to serve the delivery system, so the firmware is chosen
// here; SetMode returns straight away when the running image already serves the
// mode, which is every tune but the first.
func (front *frontend) Tune(properties map[string]string) error {
	delivery := properties["DELIVERY_SYSTEM"]
	selected, ok := modes[tuning.Normalize(delivery)]
	if !ok {
		return fmt.Errorf("%s does not support %q", front.model, delivery)
	}
	frequency, err := tuning.Number(properties, "FREQUENCY", true)
	if err != nil {
		return err
	}
	// The firmware wants Hz. Channel files usually carry Hz already, but the
	// satellite-style kHz spelling is small enough to tell apart.
	if frequency < 1_000_000 {
		frequency *= 1000
	}
	payload, err := front.tuneWords(selected, uint32(frequency), properties)
	if err != nil {
		return err
	}
	if err := front.siano.SetMode(selected.device, front.options, selected.firmware); err != nil {
		return fmt.Errorf("bring %s up for %s: %w", front.model, delivery, err)
	}
	front.mode = selected
	// Lock is announced asynchronously, so the previous channel's verdict has
	// to go before the new request does.
	front.siano.ClearLock()
	front.siano.DrainData()
	front.leftover = nil
	if _, err := front.siano.Exchange(selected.tuneRequest, selected.tuneResponse, siano.ControlMsgID,
		payload, 5*time.Second); err != nil {
		return fmt.Errorf("tune %s: %w", front.model, err)
	}
	front.tuned = true
	return nil
}

// tuneWords builds the payload of the tuning request. Both open with the
// frequency, a bandwidth code and a 12 MHz reference. ISDB-T adds the segment to
// start at -- always the first, since reception is full-segment -- and its
// bandwidth field is really a segment count. DVB-T instead takes the channel
// width, which the firmware refuses to guess, so BANDWIDTH_HZ is required.
func (front *frontend) tuneWords(selected mode, frequency uint32, properties map[string]string) ([]byte, error) {
	if selected.device != siano.ModeDVBTBDA {
		return siano.Words(frequency, siano.BandwidthISDBT13Seg, referenceHz, 0), nil
	}
	bandwidth, err := tuning.BandwidthHz(properties)
	if err != nil {
		return nil, err
	}
	code, ok := dvbtBandwidths[bandwidth/1_000_000]
	if !ok {
		return nil, fmt.Errorf("%s takes a 6, 7 or 8 MHz DVB-T channel; BANDWIDTH_HZ says %d", front.model, bandwidth)
	}
	return siano.Words(frequency, code, referenceHz), nil
}

// Status reports the lock state for the last tuned channel.
func (front *frontend) Status() (device.Status, error) {
	if !front.tuned {
		return device.Status{}, nil
	}
	locked, err := front.locked()
	if err != nil || !locked {
		return device.Status{}, err
	}
	return device.Status{
		Locked: true,
		Names:  device.LockNames,
	}, nil
}

// locked reads the demodulator lock out of the firmware's statistics, falling
// back on the indication the firmware sends unprompted. Which request to send
// and where the lock sits follow the running mode: the extended statistics are
// ISDB-T only, and the plain reply has no type and size ahead of the lock flags.
func (front *frontend) locked() (bool, error) {
	body, err := front.siano.Exchange(front.mode.statisticsRequest, front.mode.statisticsResponse,
		siano.ControlMsgID, nil, 5*time.Second)
	if err != nil {
		// The statistics request is only a prompt: the firmware announces lock
		// on its own too, so a missing answer is not yet a failure.
		return front.siano.Locked(), nil
	}
	// The body opens with a request result, then the statistics themselves.
	offset := front.mode.demodLockedOffset
	if len(body) < offset+4 {
		return front.siano.Locked(), nil
	}
	if binary.LittleEndian.Uint32(body[offset:]) != 0 {
		return true, nil
	}
	return front.siano.Locked(), nil
}

// StartStream asks the firmware for the whole transport stream. It sends
// nothing until a filter asks for it, and the full-stream pseudo-PID takes
// every packet rather than a single elementary stream.
//
// The request is sent and not waited on, as smsdvb_start_feed does: the Rio
// firmware never answers it. Measured on a MyGica S270 under Windows, it
// replies to the tune and to every statistics request but sends no
// MSG_SMS_ADD_PID_FILTER_RES, while the stream starts the moment the request
// lands. Waiting for the answer turned every locked channel into "no data".
func (front *frontend) StartStream() error {
	// The drain in Tune is not enough: the firmware sends a block or two of the
	// previous stream just after it answers the tune, after that drain, and the
	// lock takes a second or two after that. Left queued, they were the first
	// bytes of the new stream -- a continuity break right at the start of every
	// channel, measured on a MyGica S270.
	front.siano.DrainData()
	front.leftover = nil
	if err := front.siano.Send(siano.MsgAddPIDFilterReq, siano.ControlMsgID, siano.Words(siano.PIDAll)); err != nil {
		return fmt.Errorf("request the full transport stream: %w", err)
	}
	return nil
}

// StopStream withdraws the full-stream filter, again without waiting for an
// answer the firmware does not send (smsdvb_stop_feed).
func (front *frontend) StopStream() error {
	return front.siano.Send(siano.MsgRemovePIDFilterReq, siano.ControlMsgID, siano.Words(siano.PIDAll))
}

// Read hands back the next block the firmware sent, a buffer at a time.
func (front *frontend) Read(ctx context.Context, buffer []byte) (int, error) {
	if len(front.leftover) == 0 {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case block := <-front.siano.Data():
			front.leftover = block
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-timer.C:
			return 0, device.ErrWouldBlock
		}
	}
	count := copy(buffer, front.leftover)
	front.leftover = front.leftover[count:]
	return count, nil
}
