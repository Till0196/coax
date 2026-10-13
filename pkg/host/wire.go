// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package host talks to the tuner daemon.
//
// The daemon owns the hardware, because a USB tuner board can only be driven by
// one process and one board is several tuners and a card slot that different
// programs want at once. This program asks it for what it needs over HTTP, and
// starts one if none is running.
//
// The shapes below mirror the ones the daemon sends. They are written out again
// rather than imported so the two programs share a protocol instead of a build;
// a test on each side decoding the same recorded answers is what keeps them
// honest.
package host

import (
	"encoding/hex"
	"fmt"
	"strings"
)

type Version struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	Started string `json:"started,omitempty"`
	Arch    string `json:"arch,omitempty"`
}

type Board struct {
	Name   string `json:"name"`
	Naming string `json:"naming"`
	Stable bool   `json:"stable"`

	Backend     string `json:"backend"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location"`
	Serial      string `json:"serial,omitempty"`
	Port        string `json:"port,omitempty"`

	Deliveries []string `json:"deliveries,omitempty"`
	Open       bool     `json:"open"`
	Probed     bool     `json:"probed"`
	// ProbeError says why a listing that was asked to open the boards could
	// not confirm this one: firmware that is not installed, a permission that
	// is missing, hardware someone else is holding.
	ProbeError string  `json:"probe_error,omitempty"`
	Tuners     []Tuner `json:"tuners"`
}

// Setting is one of a tuner's settings, named the way a channel file names it
// wherever there is a spelling to reuse.
type Setting struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Tuner is one tuner.
type Tuner struct {
	ID         string   `json:"id"`
	Index      int      `json:"index"`
	Name       string   `json:"name"`
	Deliveries []string `json:"deliveries,omitempty"`
	Leased     bool     `json:"leased"`
	// Priority is how much the work holding this tuner said it mattered, as
	// its X-Coax-Priority header gave it. Absent for a tuner nobody holds,
	// since zero is a real priority and cannot also mean "free".
	Priority *int `json:"priority,omitempty"`
	// Mode is a hardware setting the tuner has beside the channel, absent for most
	// tuners. A TBS card's demodulator mode is card-wide and restarts every tuner
	// on the card when it moves, so it is worth seeing before asking for a
	// standard the card is not in.
	Mode string `json:"mode,omitempty"`
	// Settings is what the hardware says about how it is currently tuned:
	// what the driver measured rather than what it was asked for.
	Settings []Setting `json:"settings,omitempty"`

	// Tuned is the channel the tuner is set to, as the properties the tune
	// took: what was asked for, not what the hardware measured. Absent for a
	// tuner nobody holds, and for one held but not yet tuned.
	Tuned []Setting `json:"tuned,omitempty"`

	Locked   *bool    `json:"locked,omitempty"`
	Status   []string `json:"status,omitempty"`
	CNR      *float64 `json:"cnr,omitempty"`
	Strength *float64 `json:"strength,omitempty"`
	Quality  *float64 `json:"quality,omitempty"`
	Dropped  *uint64  `json:"dropped,omitempty"`
	// Stream is what the multiplex leaving the tuner looked like, absent
	// unless something is streaming: the daemon reads every stream it hands
	// out, and this is what it made of the bytes.
	Stream *StreamCounts `json:"stream,omitempty"`
}

// StreamCounts is what the daemon saw in the multiplex leaving a tuner. Every
// field counts from when the stream was opened and none is ever reset, so a
// caller takes two readings and subtracts.
type StreamCounts struct {
	// Format is which multiplex the bytes lined up as: "ts" or "tlv". Empty
	// means neither, which is what a demodulator locked to noise produces.
	Format string `json:"format"`
	// Bytes is everything the tuner produced; Packets is what was recognised
	// as a packet, stuffing included; Skipped is bytes outside any packet.
	Bytes   uint64 `json:"bytes"`
	Packets uint64 `json:"packets"`
	Skipped uint64 `json:"skipped"`
	// SyncLosses counts breaks in packet-boundary alignment, not packets.
	SyncLosses uint64 `json:"sync_losses"`
	// Discontinuities is packets that did not follow the last one in their own
	// run. Missing data, not damaged data.
	Discontinuities uint64 `json:"discontinuities"`
	// Nulls is stuffing. A multiplex that is almost all stuffing is one whose
	// services are not arriving.
	Nulls uint64 `json:"nulls"`

	// Errors is packets the demodulator marked as uncorrectable, and Scrambled
	// packets still carrying their scrambling bits. Transport streams only.
	Errors    uint64 `json:"errors,omitempty"`
	Scrambled uint64 `json:"scrambled,omitempty"`

	// Control is the TLV signalling that describes the multiplex, and IPv4,
	// IPv6 and CompressedIP how the datagrams inside are carried. TLV only.
	Control      uint64 `json:"control,omitempty"`
	IPv4         uint64 `json:"ipv4,omitempty"`
	IPv6         uint64 `json:"ipv6,omitempty"`
	CompressedIP uint64 `json:"compressed_ip,omitempty"`
	// Undecodable is TLV packets whose MMTP header could not be reached.
	Undecodable uint64 `json:"undecodable,omitempty"`
	// Forgotten counts times the sequence table was emptied because more runs
	// appeared at once than any multiplex has. Anything but zero means the
	// stream is being read as something it is not. TLV only.
	Forgotten uint64 `json:"forgotten,omitempty"`
}

// TuneResult is what GET /tune answers.
type TuneResult struct {
	Tuner   string   `json:"tuner"`
	Backend string   `json:"backend"`
	Locked  bool     `json:"locked"`
	Status  []string `json:"status,omitempty"`

	CNR      *float64 `json:"cnr,omitempty"`
	Strength *float64 `json:"strength,omitempty"`
	Quality  *float64 `json:"quality,omitempty"`

	// RelativeStreams is how many places a satellite transponder's table holds,
	// for a tuner whose driver names a stream by its place rather than its
	// identifier. Absent for nearly every tuner. See measureSlots for what a scan
	// does about it.
	RelativeStreams int `json:"relative_streams,omitempty"`

	ElapsedMS int64    `json:"elapsed_ms"`
	Ignored   []string `json:"ignored,omitempty"`
}

type Card struct {
	ID      string `json:"id"`
	Reader  string `json:"reader"`
	Backend string `json:"backend"`
	Board   string `json:"board"`
	Present *bool  `json:"present,omitempty"`
}

// CardSession is one reset and the answers to the commands that followed it.
type CardSession struct {
	ATR       string     `json:"atr"`
	Exchanges []Exchange `json:"exchanges,omitempty"`
}

type Exchange struct {
	Command  string `json:"command"`
	Response string `json:"response"`
	Status   string `json:"status,omitempty"`
}

type CardRequest struct {
	APDUs []string `json:"apdus"`
}

type Failure struct {
	Error  Problem     `json:"error"`
	Status *TuneResult `json:"status,omitempty"`
}

// Problem says what went wrong in a form this program can branch on.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// Error makes a problem usable as one, so it can be examined with errors.As
// rather than turned into a string.
func (p Problem) Error() string {
	if p.Message != "" {
		return p.Message
	}
	return p.Code
}

// The codes a failure carries.
const (
	CodeInvalidParameter = "invalid_parameter"
	CodeUnknownTuner     = "unknown_tuner"
	CodeTunerBusy        = "tuner_busy"
	CodeUnsupported      = "unsupported"
	CodeTuneFailed       = "tune_failed"
	CodeNoLock           = "no_lock"
	CodeDeviceGone       = "device_gone"
	CodeNoCardReader     = "no_card_reader"
	CodeCardError        = "card_error"
	CodeNoDevices        = "no_devices"
	CodeNoFirmware       = "no_firmware"
	CodeNoAccess         = "no_access"
	CodeInternal         = "internal"
)

// PriorityHeader is how a request says how much the work behind it matters, and
// DefaultPriority what one that says nothing is worth. The daemon takes a tuner
// from work that asked at a lower number and never from work that asked at the
// same.
const (
	PriorityHeader  = "X-Coax-Priority"
	DefaultPriority = 0
)

// Reasons that narrow CodeUnsupported.
const (
	ReasonDeliverySystem = "delivery_system"
	ReasonOutOfRange     = "out_of_range"
	// ReasonLNBSupply means the tuner cannot do what the channel asked of the
	// supply on its aerial lead -- in practice, cannot switch it off.
	ReasonLNBSupply = "lnb_supply"
)

// CardConnection is what opening a card answers with.
type CardConnection struct {
	ATR      string `json:"atr"`
	Protocol string `json:"protocol"`
	MaxInput int    `json:"max_input,omitempty"`
}

// CardTransmitRequest is one command APDU on its way to the card.
type CardTransmitRequest struct {
	APDU string `json:"apdu"`
}

// CardTransmitResult is what the card said back.
type CardTransmitResult struct {
	Response string `json:"response"`
	Status   string `json:"status,omitempty"`
	Protocol string `json:"protocol"`
}

// CardRelease is the answer to a request that only had to succeed.
type CardRelease struct {
	Released bool `json:"released"`
}

// CardState is what a slot looks like from outside.
type CardState struct {
	Events  int    `json:"events"`
	Changed bool   `json:"changed"`
	Present bool   `json:"present"`
	ATR     string `json:"atr,omitempty"`
	Sharing int    `json:"sharing"`
}

// ParseHex reads the hexadecimal the daemon writes bytes as, ignoring the
// separators it puts between them.
func ParseHex(written string) ([]byte, error) {
	cleaned := strings.NewReplacer(" ", "", ":", "", "-", "", "_", "").Replace(written)
	if cleaned == "" {
		return nil, nil
	}
	return hex.DecodeString(cleaned)
}

// FormatHex writes bytes the way the daemon reads them.
func FormatHex(data []byte) string {
	parts := make([]string, len(data))
	for index, value := range data {
		parts[index] = fmt.Sprintf("%02X", value)
	}
	return strings.Join(parts, " ")
}
