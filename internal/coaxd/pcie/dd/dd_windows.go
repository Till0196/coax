// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package dd drives a Digital Devices PCIe card's tuners itself, using the
// vendor's Windows driver only to carry the transport stream.
//
// These cards put a command interface in the bridge's register window and a
// firmware behind it: a tune is one message with a frequency in it. There is no
// demodulator on a bus this side can reach, so unlike the TBS backend there is
// no chip driver to carry. The vendor's base driver hands the register window
// to user mode as it stands, so reaching the interface costs one CreateFile.
package dd

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/coaxd/bda"
	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Backend is the name these tuners are listed under.
const Backend = "dd"

// model is one card this package knows how to drive.
type model struct {
	// name is what to call it.
	name string
	// board is the card's BOARD_ID register -- the PCI subsystem -- which is what
	// tells one model from another. The card is asked rather than guessed at.
	board uint32
	// deliveries is what its firmware will search for.
	deliveries []string
	// inputConfig is the value for the card-wide command that routes the shared
	// aerial socket, or inputConfigNone. See ensureInputConfig.
	inputConfig int
}

// The card's tuners appear to BDA once per standard rather than once each: a
// four-tuner MAX M4 offers twelve filters. They are faces of the same hardware,
// and a graph only connects to the network type the face it was opened through
// advertises, so the channel a graph is built against has to match the face. It
// is a formality either way; what the card receives is settled by the command
// after.
//
// faceCarriers is that formality for a face whose own standard the BDA layer
// can express, preferred over the family default.
var faceCarriers = map[string]map[string]string{
	"ISDB-T":  {"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857", "BANDWIDTH_HZ": "6000000"},
	"ISDB-C":  {"DELIVERY_SYSTEM": "ISDBC", "FREQUENCY": "111000000", "SYMBOL_RATE": "5274"},
	"ISDB-S":  {"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480", "STREAM_ID": "16400"},
	"DVB-T":   {"DELIVERY_SYSTEM": "DVBT", "FREQUENCY": "474000000", "BANDWIDTH_HZ": "8000000"},
	"DVB-T2":  {"DELIVERY_SYSTEM": "DVBT2", "FREQUENCY": "474000000", "BANDWIDTH_HZ": "8000000"},
	"DVB-C":   {"DELIVERY_SYSTEM": "DVBC", "FREQUENCY": "474000000", "SYMBOL_RATE": "6900"},
	"J83-B":   {"DELIVERY_SYSTEM": "DVBC/ANNEX_B", "FREQUENCY": "111000000", "SYMBOL_RATE": "5274"},
	"DVB-S2":  {"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "SYMBOL_RATE": "28860"},
	"DVB-S2X": {"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "SYMBOL_RATE": "28860"},
}

// carriers is the fallback, for a face whose own standard the BDA layer has no
// delivery system for -- DVB-C2 is the one.
var carriers = map[family]map[string]string{
	familyTerrestrial: {
		"DELIVERY_SYSTEM": "DVBT", "FREQUENCY": "474000000", "BANDWIDTH_HZ": "8000000",
	},
	familyCable: {
		"DELIVERY_SYSTEM": "DVBC", "FREQUENCY": "474000000", "SYMBOL_RATE": "6900",
	},
	familySatellite: {
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "SYMBOL_RATE": "28860",
	},
}

// family is which of the three faces a BDA tuner is.
type family int

const (
	familyUnknown family = iota
	familyTerrestrial
	familyCable
	familySatellite
)

// tunerFaces says which node the vendor driver registers each of its filters on.
// Two do not follow their own name, which is why this is a table and not a prefix
// test: DVB-C2 is OFDM and sits on the terrestrial node, and J.83 Annex B is on
// the cable node while sharing no text with it.
var tunerFaces = map[string]family{
	"DVB-T":             familyTerrestrial,
	"DVB-T2":            familyTerrestrial,
	"ISDB-T":            familyTerrestrial,
	"DVB-C2":            familyTerrestrial,
	"DVB-C":             familyCable,
	"DVB-C MEDIACENTER": familyCable,
	"ISDB-C":            familyCable,
	"J83-B":             familyCable,
	"DVB-S2":            familySatellite,
	"DVB-S2X":           familySatellite,
	"ISDB-S":            familySatellite,
}

// carrierFor is the channel a tuner's graph is built against.
func carrierFor(name string) (map[string]string, bool) {
	label := tunerLabel(name)
	if carrier, ok := faceCarriers[label]; ok {
		return carrier, true
	}
	carrier, ok := carriers[tunerFaces[label]]
	return carrier, ok
}

// familyOf reads the face out of a tuner's name, which the vendor driver builds
// as "Digital Devices <standard> Tuner <n>". The standard is taken out whole:
// matching on pieces would answer cable for "DVB-C2", and a graph that will not
// connect.
func familyOf(name string) family {
	return tunerFaces[tunerLabel(name)]
}

// tunerLabel is the standard out of a tuner's name, upper-cased, or empty if the
// name is not one this driver built.
func tunerLabel(name string) string {
	const prefix, suffix = "digital devices ", " tuner "
	lower := strings.ToLower(name)
	start := strings.Index(lower, prefix)
	if start < 0 {
		return ""
	}
	rest := lower[start+len(prefix):]
	end := strings.LastIndex(rest, suffix)
	if end < 0 {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(rest[:end]))
}

// models lists the cards this drives: the Digital Devices cards whose tuners
// are behind the command interface known as MCI. A MAX S8 or A8 carries
// ordinary demodulator chips on an I2C bus and would need a chip driver of its
// own.
//
// The board numbers are the PCI subsystem the card reports: the subdevice in
// the high half and the vendor 0xdd01 in the low.
//
// What each firmware searches for is what the card was built for. The M8 ATSC
// and the M8E ask their own firmware rather than being told, so their real
// lists differ from these; ATSC is left out of both regardless, this backend
// having no search command for it. Nothing here does ISDB-S3.
var models = []model{
	{
		name:  "Digital Devices MAX SX8",
		board: 0x0025dd01,
		deliveries: []string{
			"DVB-S", "DVB-S2",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices MAX SX8 Basic",
		board: 0x0026dd01,
		deliveries: []string{
			"DVB-S", "DVB-S2",
		},
		inputConfig: inputConfigNone,
	},
	{
		// The vendor's Windows driver of this writing does not bind this one.
		name:  "Digital Devices MAX SX8 V2",
		board: 0x0027dd01,
		deliveries: []string{
			"DVB-S", "DVB-S2",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices Octopus CI M2",
		board: 0x0045dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices MAX M4",
		board: 0x0050dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C", "DVB-C2",
		},
		inputConfig: inputConfigShared,
	},
	{
		name:  "Digital Devices MAX M4 V2",
		board: 0x0051dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C", "DVB-C2",
		},
		inputConfig: inputConfigShared,
	},
	{
		name:  "Digital Devices MAX M8",
		board: 0x0052dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigShared,
	},
	{
		name:  "Digital Devices MAX M8 ATSC",
		board: 0x0053dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices MAX M2",
		board: 0x0054dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices MAX M8E",
		board: 0x0055dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-S", "ISDB-C",
			"DVB-S", "DVB-S2", "DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigNone,
	},
	{
		name:  "Digital Devices MAX A8 V2",
		board: 0x0056dd01,
		deliveries: []string{
			"ISDB-T", "ISDB-C",
			"DVB-T", "DVB-T2",
			"DVB-C", "DVB-C/ANNEX_B", "DVB-C/ANNEX_C",
		},
		inputConfig: inputConfigNone,
	},
}

func modelFor(board uint32) (model, bool) {
	for _, candidate := range models {
		if candidate.board == board {
			return candidate, true
		}
	}
	return model{}, false
}

// Supports reports whether any card here can receive a delivery system.
func Supports(deliverySystem string) bool {
	for _, candidate := range models {
		for _, delivery := range candidate.deliveries {
			if tuning.Normalize(delivery) == tuning.Normalize(deliverySystem) {
				return true
			}
		}
	}
	return false
}

// Found is one tuner this package can drive: one demodulator of a card.
//
// The vendor's driver registers one BDA filter per standard per demodulator, so
// a four-tuner MAX M4 publishes up to thirty-six. Passing those on would lie in
// both directions -- the card cannot receive thirty-six channels at once, and a
// pool would hand out one demodulator twice -- so they are folded back into the
// tuners the hardware has, and the filter is chosen when the channel is.
type Found struct {
	// Name is what to show for it.
	Name string
	// Selector is the BDA moniker of one of the tuner's filters, which Open takes.
	Selector string
	// Deliveries is everything the filters of this demodulator offer between them.
	Deliveries []string
	// Faces is the moniker of every filter this demodulator has, by the standard
	// each offers: Open changes filter when the standard changes.
	Faces map[string]string
}

// Devices lists the tuners of every Digital Devices card present. Each card is
// opened briefly, the identity being in a register rather than in a name a driver
// update could reword; which BDA tuner belongs to which card comes from asking
// Windows for the tuner's parent.
func Devices() ([]Found, error) {
	paths, err := bridgePaths()
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	cards := make(map[string]model, len(paths))
	for _, path := range paths {
		card, err := identifyBridge(path)
		if err != nil {
			continue
		}
		cards[pciInstanceOf(path)] = card
	}
	if len(cards) == 0 {
		return nil, nil
	}
	tuners, err := bda.Devices()
	if err != nil {
		return nil, err
	}
	type key struct {
		instance string
		demod    byte
	}
	grouped := make(map[key]*Found)
	var order []key
	for _, tuner := range tuners {
		instance, err := parentInstanceOf(tuner.Selector)
		if err != nil {
			continue
		}
		card, ok := cards[instance]
		if !ok {
			continue
		}
		label := tunerLabel(tuner.Name)
		offered := faceDeliveries(tuner.Name, card.deliveries)
		if label == "" || len(offered) == 0 {
			continue
		}
		at := key{instance: instance, demod: demodOf(tuner.Name)}
		one := grouped[at]
		if one == nil {
			one = &Found{
				Name:     fmt.Sprintf("%s Tuner %d", card.name, at.demod+1),
				Selector: tuner.Selector,
				Faces:    map[string]string{},
			}
			grouped[at] = one
			order = append(order, at)
		}
		one.Faces[label] = tuner.Selector
		for _, delivery := range offered {
			if !contains(one.Deliveries, delivery) {
				one.Deliveries = append(one.Deliveries, delivery)
			}
		}
	}
	found := make([]Found, 0, len(order))
	for _, at := range order {
		found = append(found, *grouped[at])
	}
	return found, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// identifyBridge opens a card only long enough to read what it is.
func identifyBridge(path string) (model, error) {
	card, err := openBridge(path)
	if err != nil {
		return model{}, err
	}
	defer card.close()
	return identify(card)
}

// identify asks an open card which model it is. The board register decides, not
// the firmware banner, which can be updated; the banner is read so that an
// unknown card says something useful when it is refused.
func identify(card *bridge) (model, error) {
	who, err := card.identity()
	if err != nil {
		return model{}, err
	}
	known, ok := modelFor(who.board)
	if !ok {
		if banner := card.firmware(); banner != "" {
			return model{}, fmt.Errorf(
				"board %#08x (%s) is not a card this backend drives", who.board, banner)
		}
		return model{}, fmt.Errorf("board %#08x is not a card this backend drives", who.board)
	}
	return known, nil
}

// Device is one tuner of a card, driven from here.
type Device struct {
	transport *bda.Device
	bridge    *bridge
	card      string // the PCI instance, which is what the mailbox is locked per
	model     model
	// demod is which of the card's demodulators this tuner is.
	demod byte
	// face is which standard the BDA filter this was opened through offers, and so
	// which formality the graph has to be built against.
	face family
	// firmware is what the card's microcode calls itself, for messages.
	firmware string
	// lnbReady records that this tuner's supply block has been brought up, and
	// lnbTone what the lead is carrying. The card holds both until told otherwise.
	lnbReady bool
	lnbTone  bool
	// tuned is the system the card was last asked for, for Status.
	tuned     string
	frequency uint32
	// recent is the last reading and when it was taken, so a caller asking faster
	// than statusInterval is answered from here rather than from the card.
	recentMutex sync.Mutex
	recent      Status
	recentAt    time.Time
	recentValid bool
	// commanded is whether this tuner was pointed at its channel through the
	// command interface rather than by the vendor's own filter, and so whether
	// stopping it on the way out is this side's business.
	commanded bool
	// faces is the moniker of every filter this demodulator has, by the standard
	// each offers; label is the one the transport is open through.
	faces map[string]string
	label string
	name  string
}

// statusInterval is the shortest gap between two looks at the card.
//
// The mailbox these readings come through is shared with the vendor's driver, and
// asking too often stops the transport stream: on a MAX M4 one tuner polled every
// 50ms delivers no bytes at all, while four polled once a second stream cleanly.
// The daemon polls for a lock every 50ms, so the limit is enforced here.
const statusInterval = time.Second

func Open(selector string) (*Device, error) {
	instance, err := parentInstanceOf(selector)
	if err != nil {
		return nil, err
	}
	var path string
	paths, err := bridgePaths()
	if err != nil {
		return nil, err
	}
	for _, candidate := range paths {
		if pciInstanceOf(candidate) == instance {
			path = candidate
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("no Digital Devices card is the parent of %q", selector)
	}
	card, err := openBridge(path)
	if err != nil {
		return nil, err
	}
	known, err := identify(card)
	if err != nil {
		card.close()
		return nil, err
	}
	firmware := card.firmware()

	// Which other filters belong to this demodulator has to be known before the
	// first tune, the standard asked for deciding which one the transport is
	// opened through.
	faces, name := facesOf(selector)
	transport, err := bda.Open(selector)
	if err != nil {
		card.close()
		return nil, err
	}
	return &Device{
		transport: transport,
		bridge:    card,
		card:      instance,
		model:     known,
		firmware:  firmware,
		demod:     demodOf(transport.Name()),
		face:      familyOf(transport.Name()),
		faces:     faces,
		label:     tunerLabel(transport.Name()),
		name:      name,
	}, nil
}

// facesOf is every filter of the demodulator a moniker names, and what to call
// the tuner they belong to. An unknown moniker gets neither.
func facesOf(selector string) (map[string]string, string) {
	found, err := Devices()
	if err != nil {
		return nil, ""
	}
	for _, one := range found {
		for _, moniker := range one.Faces {
			if moniker == selector {
				return one.Faces, one.Name
			}
		}
	}
	return nil, ""
}

// demodOf is which demodulator a tuner is. The vendor driver numbers them from
// one and the command interface from zero, so getting it wrong tunes the tuner
// next door.
func demodOf(name string) byte {
	lower := strings.ToLower(name)
	for index := 1; index <= 16; index++ {
		if strings.Contains(lower, fmt.Sprintf("tuner %d", index)) {
			return byte(index - 1)
		}
	}
	return 0
}

// Name is the demodulator's name rather than that of whichever filter happens
// to be open for it, which changes with the standard.
func (device *Device) Name() string {
	if device.name != "" {
		return device.name
	}
	return device.transport.Name()
}

// Firmware is what the card's microcode calls itself, or empty if it left no
// banner. Which commands the card answers depends on it.
func (device *Device) Firmware() string { return device.firmware }

// Supports reports whether this tuner can hand over a delivery system, which is
// narrower than what the card can receive: see faceDeliveries.
func (device *Device) Supports(deliverySystem string) bool {
	if len(device.faces) == 0 {
		return faceOffers(device.label, deliverySystem)
	}
	for label := range device.faces {
		if faceOffers(label, deliverySystem) {
			return true
		}
	}
	return false
}

// Tune points the card at a channel.
//
// The graph comes up first and is built against a formality, as in the TBS
// backend: the vendor driver connects a graph only for the network type it
// advertises, and what the card receives is settled by the command that
// follows.
func (device *Device) Tune(properties map[string]string) error {
	// The graph carries a stream only once the vendor's tuner filter is happy, so a
	// filter that offers the standard asked for is left to do the tune. The
	// measurements still come from the command interface, which is the point of this
	// backend: BDA has no carrier-to-noise ratio at all.
	if err := device.faceFor(properties["DELIVERY_SYSTEM"]); err != nil {
		return err
	}
	// Neither path below sets the supply on the aerial lead: the command interface
	// has nothing to say about it, and the BDA tune request carries a polarization
	// but no tone. So it is driven here for both.
	if tuning.IsSatellite(properties["DELIVERY_SYSTEM"]) {
		supply, err := tuning.SatelliteSupply(properties)
		if err != nil {
			return err
		}
		tone := tuning.ToneWanted(properties)
		if err := tuning.ApplySupply(supply, func(high, off bool) error {
			return device.setLNB(high, off, tone)
		}); err != nil {
			return err
		}
	}
	if device.faceOffers(properties) {
		// The supply was set above; the BDA filter is not asked for it (a BDA
		// driver's supply is its own, and it refuses one switched off).
		if err := device.transport.Tune(bda.WithoutSupply(properties)); err != nil {
			return err
		}
		device.tuned = tuning.Normalize(properties["DELIVERY_SYSTEM"])
		device.forgetReading()
		return nil
	}

	// Otherwise the standard has no filter of its own on this card: the graph is a
	// formality and the card is pointed at the real channel through the command.
	command, err := searchFor(properties)
	if err != nil {
		return err
	}
	command.demod = device.demod
	command.output = device.demod

	if err := device.bridge.ensureInputConfig(device.card, device.model.inputConfig); err != nil {
		return err
	}
	if err := device.startTransport(); err != nil {
		return err
	}
	result, err := device.bridge.execute(device.card, command)
	if err != nil {
		return err
	}
	if err := result.err("tune"); err != nil {
		return err
	}
	device.tuned = tuning.Normalize(properties["DELIVERY_SYSTEM"])
	device.commanded = true
	device.forgetReading()
	return nil
}

// forgetReading drops the last measurement, because it was of another channel.
func (device *Device) forgetReading() {
	device.recentMutex.Lock()
	device.recentValid = false
	device.recentMutex.Unlock()
}

// faceFor puts the transport on the filter that offers a standard, which is what
// makes one demodulator answer to everything its filters offer between them.
func (device *Device) faceFor(deliverySystem string) error {
	if len(device.faces) == 0 || faceOffers(device.label, deliverySystem) {
		return nil
	}
	label, selector := "", ""
	for candidate, moniker := range device.faces {
		if faceOffers(candidate, deliverySystem) {
			label, selector = candidate, moniker
			break
		}
	}
	if selector == "" {
		// No filter of this tuner offers it. The command interface may still reach it.
		return nil
	}
	opened, err := bda.Open(selector)
	if err != nil {
		return fmt.Errorf("open this tuner's %s filter: %w", label, err)
	}
	_ = device.transport.Close()
	device.transport = opened
	device.label = label
	device.face = familyOf(opened.Name())
	device.forgetReading()
	return nil
}

// faceOffers reports whether the filter this tuner was opened through is for the
// standard being asked for.
func (device *Device) faceOffers(properties map[string]string) bool {
	return faceOffers(device.label, properties["DELIVERY_SYSTEM"])
}

// faceOffers reports whether a filter labelled label is the one for a delivery
// system. Beyond the exact match are the vendor driver's own network types,
// where a second-generation filter carries the first generation's channels, its
// cable filter is Annex A under a shorter name, and J83-B is Annex B.
func faceOffers(label, deliverySystem string) bool {
	if label == "" {
		return false
	}
	wanted := tuning.Normalize(deliverySystem)
	if tuning.Normalize(label) == wanted {
		return true
	}
	switch label {
	case "DVB-T2":
		return wanted == "DVBT"
	case "DVB-S2", "DVB-S2X":
		return wanted == "DVBS"
	case "DVB-C", "DVB-C MEDIACENTER":
		return wanted == "DVBC/ANNEXA"
	case "J83-B":
		return wanted == "DVBC/ANNEXB"
	case "ISDB-C":
		// The card's own driver runs Annex C and ISDB-C through one search, and the
		// vendor's filter is labelled for the Japanese name alone.
		return wanted == "DVBC/ANNEXC"
	}
	return false
}

// faceDeliveries is the part of what a card can receive that one of its tuners
// will actually hand over.
//
// The firmware searches for far more standards than the filter in front of a
// tuner admits to, and the command interface does tune such a channel -- but no
// stream follows: the vendor's capture filter releases data only once its own
// tuner reports lock, and that tuner is looking at the formality the graph was
// built on.
func faceDeliveries(name string, cardDeliveries []string) []string {
	label := tunerLabel(name)
	var offered []string
	for _, delivery := range cardDeliveries {
		if faceOffers(label, delivery) {
			offered = append(offered, delivery)
		}
	}
	return offered
}

func (device *Device) startTransport() error {
	carrier, ok := carrierFor(device.transport.Name())
	if !ok {
		return fmt.Errorf("%q does not say which standard its filter offers", device.transport.Name())
	}
	profile, err := bda.BuildTuneProfile(carrier)
	if err != nil {
		return err
	}
	if err := device.transport.StartTransport(profile); err != nil {
		return fmt.Errorf("start the card's transport: %w", err)
	}
	return nil
}

// Settings is what the driver says about how the signal is being demodulated. It
// comes from the BDA demodulator node rather than the card's command interface,
// which is what makes it safe to ask mid-stream. See driverStatus below.
func (device *Device) Settings() [][2]string { return device.transport.Settings() }

// Status asks the firmware how acquisition is going.
func (device *Device) Status() (Status, error) {
	device.recentMutex.Lock()
	defer device.recentMutex.Unlock()
	// A tuner the vendor's own filter is driving has a driver that will say what
	// it is measuring, free of the hazard the mailbox carries.
	if !device.commanded {
		if status, ok := device.driverStatus(); ok {
			device.recent, device.recentAt, device.recentValid = status, time.Now(), true
			return status, nil
		}
	}
	if device.transport.Streaming() {
		return device.streamingStatus(), nil
	}
	if device.recentValid && time.Since(device.recentAt) < statusInterval {
		return device.recent, nil
	}

	status, err := device.readStatus()
	if err != nil {
		return status, err
	}
	device.recent, device.recentAt, device.recentValid = status, time.Now(), true
	return status, nil
}

// driverStatus is what the vendor's driver says about the channel, and is the
// reading to prefer wherever it is available: the same figures come from the
// card's command mailbox, but reaching that while the driver is carrying a
// stream stops the stream. Asked this way the request goes through the driver's
// own lock.
func (device *Device) driverStatus() (Status, bool) {
	measured, ok := device.transport.Statistics()
	if !ok || !measured.KnownCNR {
		return Status{}, false
	}
	status := Status{}
	if measured.KnownLevel {
		status.Strength = Measurement{Value: measured.Level, Known: true}
	}
	// Zero is what a demodulator with nothing to measure answers, so it is the
	// difference between locked and not rather than a ratio of zero.
	if measured.CNR > 0 {
		status.Locked = true
		status.CNR = Measurement{Value: measured.CNR, Known: true}
		status.Names = []string{"locked", "LOCK"}
	} else {
		status.Names = []string{"searching"}
	}
	return status, true
}

// streamingStatus is what to say about a tuner whose stream the vendor's driver
// is carrying. The mailbox is out of reach while it streams, so the driver is
// asked through the property set it answers for exactly this. A card that does
// not answer falls back to the last reading taken before the stream started.
func (device *Device) streamingStatus() Status {
	status := device.recent
	if !device.recentValid {
		status = Status{}
	}
	if live, err := device.transport.Status(); err == nil {
		status.Locked = live.Locked
		status.Names = live.Names
	}
	if measured, ok := device.transport.Statistics(); ok {
		if measured.KnownCNR {
			status.CNR = Measurement{Value: measured.CNR, Known: true}
			// A demodulator with a ratio to report has something to measure, which is
			// the lock this graph cannot otherwise confirm. Zero is what it answers
			// when it has not.
			if measured.CNR > 0 {
				status.Locked = true
				if len(status.Names) == 0 {
					status.Names = []string{"locked", "LOCK"}
				}
			}
		}
		if measured.KnownLevel {
			status.Strength = Measurement{Value: measured.Level, Known: true}
		}
	}
	return status
}

// readStatus asks the card, which Status does no more often than statusInterval.
func (device *Device) readStatus() (Status, error) {
	state, err := device.bridge.execute(device.card, &mciCommand{
		command: cmdGetStatus, demod: device.demod,
	})
	if err != nil {
		return Status{}, err
	}
	status := Status{Names: []string{demodStateName(state.status)}}
	switch state.status {
	case demodLocked:
		status.Locked = true
		status.Names = append(status.Names, "LOCK")
	case demodWaitTS:
		status.Names = append(status.Names, "SYNC")
	}

	info, err := device.bridge.execute(device.card, &mciCommand{
		command: cmdGetSignalInfo, demod: device.demod,
	})
	if err != nil {
		return status, nil
	}
	if signal, ok := parseSignalInfo(info.payload); ok {
		status.Strength = Measurement{Value: signal.powerDBm, Known: true}
		if status.Locked {
			status.CNR = Measurement{Value: signal.cnrDB, Known: true}
		}
	}
	return status, nil
}

// Measurement is a reading the hardware may not offer. Zero is a real reading, so
// whether one was taken is carried separately.
type Measurement struct {
	Value float64
	Known bool
}

type Status struct {
	Locked   bool
	Names    []string
	CNR      Measurement
	Strength Measurement
}

// OpenStream hands over the transport stream the card's DMA is filling.
func (device *Device) OpenStream() (*bda.Stream, error) {
	return device.transport.OpenStream()
}

// Close stops the demodulator and lets go. Unlike the TBS cards there is
// nothing to hand back: the vendor driver keeps no state this backend took
// away.
func (device *Device) Close() error {
	device.recentMutex.Lock()
	commanded := device.commanded
	device.recentMutex.Unlock()
	if device.bridge != nil {
		if commanded {
			_, _ = device.bridge.execute(device.card, &mciCommand{
				command: cmdStop, demod: device.demod,
			})
		}
		// Before the register window goes, that being what the supply is switched
		// through.
		device.releaseLNB()
		_ = device.bridge.close()
		device.bridge = nil
	}
	return device.transport.Close()
}
