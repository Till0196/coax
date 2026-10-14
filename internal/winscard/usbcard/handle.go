// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usbcard

import (
	"strings"

	"github.com/Till0196/coax/pkg/winscard"
)

// Handle is one reader held open below the level PC/SC works at, for the IFD
// handler. An IFD handler sits underneath a resource manager: pcscd does the
// enumeration, sharing and transactions itself and asks the driver only to
// power a card up, say whether one is there, and carry an APDU. Going through
// Connect would run a second set of those rules under the one already running.
//
// From the slot beneath it a Handle still gets the board's lifetime and the T=1
// session, which belong to the hardware rather than to PC/SC.
type Handle struct {
	slot *slot
}

// OpenDevice takes a reader by its USB bus and address, the "001:004" form the
// board registry uses, and holds its board open. This is what a resource
// manager gives a driver: it has already chosen the device and names it as a
// device path.
func (p *Provider) OpenDevice(selector string) (*Handle, error) {
	found, err := p.devices()
	if err != nil {
		return nil, err
	}
	for _, one := range found {
		if one.selector != selector {
			continue
		}
		target := p.slotFor(one)
		if err := target.open(); err != nil {
			return nil, err
		}
		return &Handle{slot: target}, nil
	}
	return nil, winscard.ErrUnknownReader
}

// OpenFirst takes the first attached board of a card-carrying backend that
// nothing else has open. It is the fallback for a resource manager whose device
// name this package cannot read; taking the first free board is a guess that is
// only right when one board is attached, so a binding using it says so.
func (p *Provider) OpenFirst() (*Handle, error) {
	found, err := p.devices()
	if err != nil {
		return nil, err
	}
	for _, one := range found {
		target := p.slotFor(one)
		target.state.Lock()
		busy := target.opens > 0
		target.state.Unlock()
		if busy {
			continue
		}
		if err := target.open(); err != nil {
			continue
		}
		return &Handle{slot: target}, nil
	}
	return nil, winscard.ErrUnknownReader
}

// Name is the reader name this device would be listed under.
func (h *Handle) Name() string { return h.slot.cardDevice.name }

// Vendor and Model name the hardware for the attributes a driver reports.
func (h *Handle) Vendor() string { return h.slot.cardDevice.vendor }
func (h *Handle) Model() string  { return h.slot.cardDevice.model }

// Close releases the board.
func (h *Handle) Close() error {
	h.slot.close()
	return nil
}

// Present reports whether a card is seated.
func (h *Handle) Present() (bool, error) {
	h.slot.claim(h)
	defer h.slot.drop()
	present, _, err := h.slot.look()
	return present, err
}

// PowerUp resets the card and returns its ATR, which is what powering a card
// up means on interfaces that have no separate power control.
func (h *Handle) PowerUp() ([]byte, error) {
	h.slot.claim(h)
	defer h.slot.drop()
	h.slot.invalidate()
	return h.slot.session()
}

// ATR returns the answer to reset of the current session, empty when there is
// none. A resource manager asks for it separately from powering the card up.
func (h *Handle) ATR() []byte {
	h.slot.state.Lock()
	defer h.slot.state.Unlock()
	return append([]byte(nil), h.slot.atr...)
}

// PowerDown gives up the session. Nothing here can take the power off one card
// of a board on its own, so what the caller is promised is the part that can
// be kept: the conversation is over and the next one starts from a reset.
func (h *Handle) PowerDown() error {
	h.slot.claim(h)
	defer h.slot.drop()
	h.slot.invalidate()
	return nil
}

// Transmit sends one command APDU on an established session.
func (h *Handle) Transmit(apdu []byte) ([]byte, error) {
	if len(apdu) == 0 || len(apdu) > MaxInput {
		return nil, winscard.ErrInvalidParameter
	}
	h.slot.claim(h)
	defer h.slot.drop()
	return h.slot.exchange(apdu)
}

// DeviceSelector reads the USB bus and address out of the device name a
// resource manager hands its driver. pcsc-lite names a USB device by the path
// it found it at, as in "usb:0511/083f:libudev:0:/dev/bus/usb/001/004", whose
// trailing bus and device numbers are what the board registry selects on.
// Anything else comes back empty.
func DeviceSelector(name string) string {
	const marker = "/dev/bus/usb/"
	index := strings.LastIndex(name, marker)
	if index < 0 {
		return ""
	}
	rest := name[index+len(marker):]
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return ""
	}
	bus, address := parts[0], parts[1]
	if !isDigits(bus) || !isDigits(address) {
		return ""
	}
	return pad(bus) + ":" + pad(address)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, letter := range value {
		if letter < '0' || letter > '9' {
			return false
		}
	}
	return true
}

func pad(value string) string {
	for len(value) < 3 {
		value = "0" + value
	}
	return value
}

// OpenReader takes a reader by the name it is listed under and holds its board
// open. It is what a binding that works in reader names rather than in device
// paths uses to watch a slot.
func (p *Provider) OpenReader(reader string) (*Handle, error) {
	found, err := p.devices()
	if err != nil {
		return nil, err
	}
	for _, one := range found {
		if one.name != reader {
			continue
		}
		target := p.slotFor(one)
		if err := target.open(); err != nil {
			return nil, err
		}
		return &Handle{slot: target}, nil
	}
	return nil, winscard.ErrUnknownReader
}

// Session resets the card and sends a run of command APDUs to it, holding the
// slot for the whole exchange. The claim covers all of it deliberately: a reset
// and the commands after it are one conversation, so another client's APDU in
// the middle would break both. It is the smallest unit a caller that is not
// speaking PC/SC can be given.
func (h *Handle) Session(apdus [][]byte) (atr []byte, responses [][]byte, err error) {
	for _, apdu := range apdus {
		if len(apdu) == 0 || len(apdu) > MaxInput {
			return nil, nil, winscard.ErrInvalidParameter
		}
	}
	h.slot.claim(h)
	defer h.slot.drop()

	h.slot.invalidate()
	atr, err = h.slot.session()
	if err != nil {
		return nil, nil, err
	}
	responses = make([][]byte, 0, len(apdus))
	for _, apdu := range apdus {
		response, err := h.slot.exchange(apdu)
		if err != nil {
			// What came back before the failure is still what the card said,
			// and a caller working out how far it got needs it.
			return atr, responses, err
		}
		responses = append(responses, response)
	}
	return atr, responses, nil
}

// Sharing reports how the slot is being used, as the number of shared
// connections, or -1 when one connection holds it exclusively. It is what a
// resource manager publishes so that clients can see a reader is busy.
func (h *Handle) Sharing() int {
	h.slot.state.Lock()
	defer h.slot.state.Unlock()
	if h.slot.exclusive > 0 {
		return -1
	}
	return h.slot.shared
}

// Events counts the card insertions and removals seen on this slot.
func (h *Handle) Events() uint16 {
	h.slot.state.Lock()
	defer h.slot.state.Unlock()
	return h.slot.events
}
