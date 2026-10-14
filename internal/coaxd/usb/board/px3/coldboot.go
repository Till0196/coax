// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"errors"
	"fmt"
	"time"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
	"github.com/Till0196/coax/pkg/usbio"
)

// A board that has just been plugged in runs the bridge's boot ROM and
// enumerates under ASICEN's own identifier rather than PLEX's. Uploading the
// firmware makes it re-enumerate as the board proper, and the handle to the
// cold device dies the moment the firmware starts -- unlike every other board
// here, which downloads its firmware over the device it already has open.
var coldIDs = []usbio.ID{{Vendor: 0x1738, Product: 0x5211}, {Vendor: 0x1738, Product: 0x5216}}

const (
	// enumerationTimeout bounds the wait for a booted board to come back. It
	// re-enumerates in well under a second; the rest is slack for a hub.
	enumerationTimeout = 5 * time.Second
	enumerationPoll    = 100 * time.Millisecond
	// coldBoardLimit guards the boot loop against a board that fails to leave
	// the cold identity; the loop otherwise ends on its own.
	coldBoardLimit = 8
)

// errNoColdBoard reports that nothing is waiting to be booted.
var errNoColdBoard = errors.New("no cold ASV52xx board is attached")

// bootColdBoards uploads the firmware to every cold board attached.
//
// The jump address has to come from the image itself: starting the right image
// at the wrong address wedges the board until it is replugged, so an
// unrecognised image is refused here and left to load-asv52xx-firmware, where
// an address can be given deliberately.
func bootColdBoards(options device.Options) error {
	probe, err := usbio.Open("", coldIDs)
	if err != nil {
		return errNoColdBoard
	}
	// Released straight away: the firmware is read before anything is
	// uploaded, so a missing image is reported without having claimed a board.
	_ = probe.Close()
	// Looked for where every other blob is, not in one directory only.
	path, err := device.FindFirmware(options.FirmwareDirOrDefault(), asv5211.FirmwareFile)
	if err != nil {
		return fmt.Errorf("a cold ASV52xx board is attached but its firmware could not be found: %w\nrun fwextract over PLEX's PX-SERIES_ver.1.0_Linux_Driver.zip to produce %s", err, asv5211.FirmwareFile)
	}
	firmware, err := asv5211.ReadFirmware(path)
	if err != nil {
		return fmt.Errorf("a cold ASV52xx board is attached but its firmware could not be read: %w\nrun fwextract over PLEX's PX-SERIES_ver.1.0_Linux_Driver.zip to produce %s", err, asv5211.FirmwareFile)
	}
	if !firmware.Known {
		return fmt.Errorf("%s is not an image this program recognises (SHA-256 %s), so the address to start it at is unknown; upload it with \"load-asv52xx-firmware --firmware-index\" instead", path, firmware.Digest())
	}
	for attempt := 0; attempt < coldBoardLimit; attempt++ {
		cold := coldBoardCount()
		transport, openErr := usbio.Open("", coldIDs)
		if openErr != nil {
			if attempt == 0 {
				return errNoColdBoard
			}
			return nil
		}
		uploadErr := asv5211.UploadFirmwareAt(transport, firmware.Image, firmware.Index)
		_ = transport.Close()
		if errors.Is(uploadErr, usbio.ErrGone) {
			// The board opened was one already booted and on its way off the bus:
			// the PX-W3U3 V2's two bridges sit behind one hub, and the listing can
			// still carry the first under its cold identity as the second is opened.
			time.Sleep(enumerationPoll)
			continue
		}
		if uploadErr != nil {
			return fmt.Errorf("upload ASV52xx firmware: %w", uploadErr)
		}
		awaitColdDeparture(cold)
	}
	return nil
}

// coldBoardCount is how many boards are attached under a cold identity.
func coldBoardCount() int {
	attached, err := usbio.Devices()
	if err != nil {
		return 0
	}
	count := 0
	for _, candidate := range attached {
		if candidate.ID.In(coldIDs) {
			count++
		}
	}
	return count
}

// awaitColdDeparture waits for a board just sent its firmware to leave its cold
// identity, so that the next open does not find it again.
func awaitColdDeparture(before int) {
	deadline := time.Now().Add(enumerationTimeout)
	for coldBoardCount() >= before && time.Now().Before(deadline) {
		time.Sleep(enumerationPoll)
	}
}

// boot is bootColdBoards for a caller that looks for cold boards itself, and so
// has nothing to learn from there being none.
func boot(options device.Options) error {
	if err := bootColdBoards(options); err != nil && !errors.Is(err, errNoColdBoard) {
		return err
	}
	return nil
}

// openWarm opens a booted board, waiting a moment for one that has just been
// sent its firmware to finish re-enumerating.
func openWarm(selector string, wait time.Duration) (*usbio.Transport, error) {
	deadline := time.Now().Add(wait)
	for {
		transport, err := usbio.Open(selector, supportedIDs)
		if err == nil || !time.Now().Before(deadline) {
			return transport, err
		}
		time.Sleep(enumerationPoll)
	}
}
