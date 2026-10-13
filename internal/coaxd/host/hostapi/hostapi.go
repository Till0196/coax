// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package hostapi holds the wire shapes and request-parsing rules the daemon
// and its clients agree on; nothing here touches hardware. The contract is HTTP
// plus these shapes, not a shared library, so a client in another language has
// all of it here. The shapes carry no version number: the generated document
// and both sides decoding the same recorded answers in their tests keep them
// honest.
package hostapi

// Version is what GET /version answers.
type Version struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Started lets a client tell a daemon it just launched from one already running.
	Started string `json:"started,omitempty"`
	// Arch is the processor the build is for (GOARCH: amd64, arm64), so that a
	// client can say which binary is running beside which it has on disk.
	Arch string `json:"arch,omitempty"`
}

// Board is one attached board as GET /tuners reports it.
type Board struct {
	// Name is what the board is known by; its tuners' names are built on it.
	Name string `json:"name"`
	// Naming says what the name was built from, and Stable whether it survives the
	// board being unplugged and put back into the same socket.
	Naming string `json:"naming"`
	Stable bool   `json:"stable"`

	Backend     string `json:"backend"`
	Description string `json:"description,omitempty"`
	// Location is the bus and address. Not part of the name: the kernel reassigns
	// it.
	Location string `json:"location"`
	Serial   string `json:"serial,omitempty"`
	Port     string `json:"port,omitempty"`

	// Deliveries is every delivery system some tuner of this board reaches.
	Deliveries []string `json:"deliveries,omitempty"`
	// Open is whether the daemon is holding the board open right now.
	Open bool `json:"open"`
	// Probed reports whether the tuners below were read from the hardware. False
	// means they were worked out from the board's identifiers, which is the
	// default: opening a board powers it up and loads firmware, and listing is not
	// a request to do that.
	Probed bool `json:"probed"`
	// ProbeError says why an ?open=1 listing could not confirm this board. Empty
	// when nothing was tried or nothing went wrong, so an unconfirmed answer never
	// passes for a confirmed one.
	ProbeError string  `json:"probe_error,omitempty"`
	Tuners     []Tuner `json:"tuners"`
}

// Tuner is one tuner as a listing reports it.
type Tuner struct {
	ID         string   `json:"id"`
	Index      int      `json:"index"`
	Name       string   `json:"name"`
	Deliveries []string `json:"deliveries,omitempty"`
	Leased     bool     `json:"leased"`
	// Priority is how much the work holding this tuner said it mattered, as its
	// X-Coax-Priority header gave it. Absent for a tuner nobody holds: zero is a
	// real priority, so "nobody holds it" cannot be spelled as zero.
	Priority *int `json:"priority,omitempty"`
	// Mode is a hardware setting beside the channel, where the tuner has one: a TBS
	// card's demodulator mode, shared by the whole card. Absent for most tuners.
	Mode string `json:"mode,omitempty"`
	// Settings is what a driver measured, not what it was asked for.
	Settings []Setting `json:"settings,omitempty"`
	// Controls is what a channel may set on this tuner beside the channel: the
	// supply on its lead (lnb_power) and its gain. Known from the model, so it is
	// there before the board is opened. Absent where there is nothing to set --
	// which is what tells a client not to offer it.
	Controls *Controls `json:"controls,omitempty"`
	// Tuned is the channel the tuner is set to, as the properties the tune took --
	// what was asked for. Absent for a tuner nobody holds or one not yet tuned.
	Tuned []Setting `json:"tuned,omitempty"`

	// The rest is filled in only while the tuner is in use: reading it means talking
	// to the hardware.
	Locked   *bool    `json:"locked,omitempty"`
	Status   []string `json:"status,omitempty"`
	CNR      *float64 `json:"cnr,omitempty"`
	Strength *float64 `json:"strength,omitempty"`
	Quality  *float64 `json:"quality,omitempty"`
	// Dropped is transport packets thrown away because the reader could not keep up.
	// It separates a recording that lost data from one fed a poor signal.
	Dropped *uint64 `json:"dropped,omitempty"`
	// Stream is what the multiplex looked like on the way out, while streaming.
	Stream *StreamCounts `json:"stream,omitempty"`
}

// StreamCounts is what was seen in the multiplex leaving a tuner. Every field counts
// from when the stream was opened and none is ever reset, so a caller takes two
// readings and subtracts. They tell apart three failures that look identical in the
// recorded file:
//
//   - Nothing received: Packets stays at zero, or SyncLosses climbs without
//     stopping because the demodulator is locked to noise.
//   - Poor signal: Errors climbs. TLV has no such flag, so on ISDB-S3 this shows
//     as Discontinuities without Dropped.
//   - Not read fast enough: Discontinuities climbs alongside Dropped.
type StreamCounts struct {
	// Format is which multiplex this was read as, "ts" or "tlv". It decides which
	// fields below are filled in.
	Format string `json:"format"`
	// Bytes is everything the tuner produced; Packets is what was recognised as a
	// packet, stuffing included; Skipped is bytes outside any packet.
	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`
	Skipped uint64 `json:"skipped"`
	// SyncLosses counts breaks in packet-boundary alignment, not packets.
	SyncLosses uint64 `json:"sync_losses"`
	// Discontinuities is packets that did not follow the last one in their own run
	// -- a TS continuity counter within a PID, or an MMTP sequence number within a
	// packet identifier. Missing data, not damaged data.
	Discontinuities uint64 `json:"discontinuities"`
	// Nulls is stuffing. A multiplex that is almost all stuffing is one whose
	// services are not arriving.
	Nulls uint64 `json:"nulls"`

	// Errors is packets the demodulator marked as uncorrectable. TS only.
	Errors uint64 `json:"errors,omitempty"`
	// Scrambled is packets still carrying their scrambling bits. TS only.
	Scrambled uint64 `json:"scrambled,omitempty"`

	// Control is the TLV signalling that describes the multiplex, and IPv4, IPv6 and
	// CompressedIP how the datagrams inside are carried. TLV only.
	Control      uint64 `json:"control,omitempty"`
	IPv4         uint64 `json:"ipv4,omitempty"`
	IPv6         uint64 `json:"ipv6,omitempty"`
	CompressedIP uint64 `json:"compressed_ip,omitempty"`
	// Undecodable is TLV packets whose MMTP header could not be reached. TLV only.
	Undecodable uint64 `json:"undecodable,omitempty"`
	// Forgotten counts times the sequence table was emptied because more runs
	// appeared at once than any multiplex has. Anything but zero means the stream
	// is being read as something it is not, and Discontinuities beside it cannot
	// be believed.
	Forgotten uint64 `json:"forgotten,omitempty"`
}

// Setting is one of a tuner's settings, named the way a channel file names it.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Controls is what a tuner lets a channel set beside where to tune.
type Controls struct {
	// LNB is present where lnb_power is honoured.
	LNB *LNBControl `json:"lnb,omitempty"`
	// Gain is present where gain is honoured.
	Gain *GainControl `json:"gain,omitempty"`
}

// LNBControl is what lnb_power can ask of this tuner.
type LNBControl struct {
	// Levels is what lnb_power accepts here: "off" where the supply can be
	// switched off, then "low" and "high".
	Levels []string `json:"levels"`
	// Low and High are what the two levels are worth ("11V", "15V"), absent for a
	// supply that is only on or off.
	Low  string `json:"low,omitempty"`
	High string `json:"high,omitempty"`
	// Shared is "board" or "card" where one supply feeds more than this tuner.
	Shared string `json:"shared,omitempty"`
}

// GainControl is what gain can ask of this tuner.
type GainControl struct {
	// Kind is "switch" for an amplifier that is in or out (min, max, auto) and
	// "steps" for one with levels, which a gain in dB is matched to.
	Kind string `json:"kind"`
	// Decibels is the steps, least first, for kind "steps".
	Decibels []float64 `json:"decibels,omitempty"`
	// Deliveries narrows the setting to these systems, where the amplifier sits on
	// one input only. Absent means every system the tuner tunes.
	Deliveries []string `json:"deliveries,omitempty"`
}

// TuneResult is what GET /tune answers. Failing to lock is not an error here: a
// scan asks about hundreds of frequencies expecting most to be empty. Only a
// request that could not be attempted at all is an error.
type TuneResult struct {
	Tuner   string   `json:"tuner"`
	Backend string   `json:"backend"`
	Locked  bool     `json:"locked"`
	Status  []string `json:"status,omitempty"`

	CNR      *float64 `json:"cnr,omitempty"`
	Strength *float64 `json:"strength,omitempty"`
	Quality  *float64 `json:"quality,omitempty"`

	// RelativeStreams is how many places a satellite transponder's table holds,
	// for a driver that names a stream by its place rather than by its identifier.
	// The Earthsoft character devices read the transmission control block
	// themselves and take an index into it -- the numbering recpt1 writes as
	// BS01_1 -- so a caller holding identifiers from a network table needs this
	// count to translate them.
	RelativeStreams int `json:"relative_streams,omitempty"`

	ElapsedMS int64 `json:"elapsed_ms"`
	// Ignored names properties left out of the tuning because they describe what a
	// multiplex carries rather than how to receive it, so a caller pasting in a
	// channel-file section can see what was not used.
	Ignored []string `json:"ignored,omitempty"`
}

// Card is one card slot as GET /cards reports it.
type Card struct {
	ID      string `json:"id"`
	Reader  string `json:"reader"`
	Backend string `json:"backend"`
	Board   string `json:"board"`
	// Present is filled in only when the slot was looked at, which means opening
	// the board.
	Present *bool `json:"present,omitempty"`
}

// CardSession is what POST /cards/{id}/session answers: one reset and the
// answers to the commands that followed it.
type CardSession struct {
	ATR       string     `json:"atr"`
	Exchanges []Exchange `json:"exchanges,omitempty"`
}

// Exchange is one command APDU and what the card said back.
type Exchange struct {
	Command  string `json:"command"`
	Response string `json:"response"`
	// Status is the two trailing status bytes.
	Status string `json:"status,omitempty"`
}

// CardRequest is the body POST /cards/{id}/session takes.
type CardRequest struct {
	// APDUs are command APDUs in hexadecimal; separators are ignored.
	APDUs []string `json:"apdus"`
}

// CardConnection is what POST /cards/{id}/connect answers. The card is held by
// the HTTP connection the request arrived on and given up when it closes.
type CardConnection struct {
	// ATR is the card's answer to reset, which is what identifies it.
	ATR string `json:"atr"`
	// Protocol is what the reader and the card agreed on; these speak T=1 only.
	Protocol string `json:"protocol"`
	// MaxInput is the longest command APDU this reader carries, the paths behind it
	// moving one T=1 block per message.
	MaxInput int `json:"max_input,omitempty"`
}

// CardTransmitRequest is the body POST /cards/{id}/transmit takes.
type CardTransmitRequest struct {
	// APDU is one command in hexadecimal; separators are ignored.
	APDU string `json:"apdu"`
}

// CardTransmitResult is what the card said back to one command.
type CardTransmitResult struct {
	Response string `json:"response"`
	// Status is the two trailing status bytes.
	Status   string `json:"status,omitempty"`
	Protocol string `json:"protocol"`
}

// CardRelease is what a request that only had to succeed answers with.
type CardRelease struct {
	// Released reports whether this connection was holding what it asked to give up.
	// A false is not a failure: a client tidying up may ask twice.
	Released bool `json:"released"`
}

// CardState is what a slot looks like from outside, and what GET
// /cards/{id}/events answers with.
type CardState struct {
	// Events counts the insertions and removals this slot has seen. A client passes
	// the last one it knows as `since` and is answered when the count moves.
	Events int `json:"events"`
	// Changed says whether the count moved while this request waited, as against the
	// wait running out.
	Changed bool   `json:"changed"`
	Present bool   `json:"present"`
	ATR     string `json:"atr,omitempty"`
	// Sharing is how many connections hold the card, or -1 when one holds it
	// exclusively.
	Sharing int `json:"sharing"`
}

// Failure is the body every failed request answers with.
type Failure struct {
	Error Problem `json:"error"`
	// Status carries whatever was measured before the request failed, so a scan
	// still gets the signal strength of a frequency that did not lock.
	Status *TuneResult `json:"status,omitempty"`
}

// Problem says what went wrong in a form a program can branch on.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	// Reason narrows a code that covers more than one situation.
	Reason string `json:"reason,omitempty"`
}

// The codes a failure carries, so a client can branch without reading the
// message.
const (
	// CodeInvalidParameter is a request that could not be understood.
	CodeInvalidParameter = "invalid_parameter"
	// CodeUnknownTuner names a tuner or board nothing attached answers to.
	CodeUnknownTuner = "unknown_tuner"
	// CodeTunerBusy means every tuner that could have served the request is in use.
	CodeTunerBusy = "tuner_busy"
	// CodeUnsupported means no attached tuner can do what was asked. Reason says
	// which.
	CodeUnsupported = "unsupported"
	// CodeTuneFailed is a tuning attempt the hardware refused.
	CodeTuneFailed = "tune_failed"
	// CodeNoLock means the tuner was set to the channel and nothing came back.
	// Only a stream reports it as a failure; a channel query reports it as an
	// answer.
	CodeNoLock = "no_lock"

	// CodeNoSync means the tuner locked and then produced something that is not a
	// transport stream, which is what a lead nothing is feeding gives.
	CodeNoSync = "no_sync"
	// CodeDeviceGone means the hardware was unplugged.
	CodeDeviceGone = "device_gone"
	// CodeNoCardReader means the board has no card slot; CodeCardError is a card
	// that would not answer.
	CodeNoCardReader = "no_card_reader"
	CodeCardError    = "card_error"
	// CodeNoDevices means the bus could not be read at all, as opposed to nothing
	// being attached to it.
	CodeNoDevices = "no_devices"
	// CodeNoFirmware means a board needs a firmware file that is not installed.
	CodeNoFirmware = "no_firmware"
	// CodeNoAccess means the daemon is not allowed to open the device -- on Linux,
	// an ordinary user without the udev rule.
	CodeNoAccess = "no_access"
	// CodeInternal is a fault in the daemon rather than in the request.
	CodeInternal = "internal"
)

// Reasons that narrow CodeUnsupported.
const (
	ReasonDeliverySystem = "delivery_system"
	ReasonOutOfRange     = "out_of_range"
	// ReasonLNBSupply means the tuner cannot do what the channel asked of the supply
	// on its aerial lead. A property of the tuner, not a fault.
	ReasonLNBSupply = "lnb_supply"
	// ReasonGain means the tuner has no gain setting, or cannot set it the way the
	// channel asked (a level of an amplifier that is only in or out). A property of
	// the tuner, not a fault. Tuner.Controls says beforehand what it can do.
	ReasonGain = "gain"
)
