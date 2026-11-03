// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package px3

import (
	"errors"
	"fmt"
	"sort"
	"strings"
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
	attached, err := usbio.Devices()
	if err != nil {
		return err
	}
	var pending []usbio.Attached
	seen := make(map[string]bool)
	for _, candidate := range attached {
		if candidate.ID.In(coldIDs) {
			if candidate.Port == "" {
				return fmt.Errorf("cold ASV52xx bridge %s has no physical USB port", candidate.Location)
			}
			pending = append(pending, candidate)
			seen[candidate.Port] = true
		}
	}
	if len(pending) == 0 {
		return errNoColdBoard
	}
	// Start the first hub port before a sibling that its startup can disconnect.
	sort.Slice(pending, func(i, j int) bool { return pending[i].Port < pending[j].Port })
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
	for at := 0; at < len(pending); at++ {
		// USB addresses change at each enumeration, including sibling restoration.
		target := pending[at]
		transport, err := openColdAt(target.Port)
		if err != nil {
			return err
		}
		if transport != nil {
			uploadErr := asv5211.UploadFirmwareAt(transport, firmware.Image, firmware.Index)
			_ = transport.Close()
			if uploadErr != nil && !errors.Is(uploadErr, usbio.ErrGone) {
				return fmt.Errorf("upload ASV52xx firmware at %s: %w", target.Port, uploadErr)
			}
		}
		warm, err := awaitBootDevice(target.Port, supportedIDs, time.Now().Add(enumerationTimeout))
		if err != nil {
			return err
		}
		// GPIO 0x40 on the V2's warm bridge drops a sibling still in its boot
		// ROM, so only the W3U2 and W3U3 have theirs brought back this way.
		if warm.ID.Product != 0x0004 && warm.ID.Product != 0x0005 {
			continue
		}
		siblings, err := restoreBootSibling(warm)
		if err != nil {
			return err
		}
		for _, sibling := range siblings {
			if !seen[sibling.Port] {
				for port := range seen {
					if port != warm.Port && parentPort(port) == parentPort(warm.Port) {
						return fmt.Errorf("enclosure at %s changed sibling port from %s to %s", parentPort(warm.Port), port, sibling.Port)
					}
				}
				pending = append(pending, sibling)
				seen[sibling.Port] = true
			}
		}
	}
	for _, target := range pending {
		if _, err := awaitBootDevice(target.Port, supportedIDs, time.Now().Add(enumerationTimeout)); err != nil {
			return err
		}
	}
	return nil
}

// openColdAt resolves the port again if its address changes while opening.
// A bridge already running its firmware needs no further upload.
func openColdAt(port string) (*usbio.Transport, error) {
	deadline := time.Now().Add(enumerationTimeout)
	for {
		candidate, err := awaitBootDevice(port, append(coldIDs, supportedIDs...), deadline)
		if err != nil {
			return nil, err
		}
		if candidate.ID.In(supportedIDs) {
			return nil, nil
		}
		transport, err := usbio.Open(candidate.Location, coldIDs)
		if err == nil {
			return transport, nil
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("open cold ASV52xx bridge at %s: %w", port, err)
		}
		time.Sleep(enumerationPoll)
	}
}

// awaitBootDevice follows one physical bridge across USB address changes.
func awaitBootDevice(port string, ids []usbio.ID, deadline time.Time) (usbio.Attached, error) {
	for {
		if !time.Now().Before(deadline) {
			return usbio.Attached{}, fmt.Errorf("ASV52xx bridge at %s did not re-enumerate", port)
		}
		attached, err := usbio.Devices()
		if err != nil {
			return usbio.Attached{}, err
		}
		for _, candidate := range attached {
			if candidate.Port == port && candidate.ID.In(ids) {
				return candidate, nil
			}
		}
		time.Sleep(enumerationPoll)
	}
}

// restoreBootSibling enables the second enclosure bridge before its upload.
func restoreBootSibling(warm usbio.Attached) ([]usbio.Attached, error) {
	if !strings.Contains(warm.Port, ".") {
		return nil, fmt.Errorf("bridge at %s has no enclosure hub port", warm.Port)
	}
	restored := false
	deadline := time.Now().Add(enumerationTimeout)
	for {
		attached, err := usbio.Devices()
		if err != nil {
			return nil, err
		}
		var siblings []usbio.Attached
		for _, candidate := range attached {
			if candidate.Port != warm.Port && parentPort(candidate.Port) == parentPort(warm.Port) &&
				(candidate.ID.In(coldIDs) || candidate.ID == warm.ID) {
				siblings = append(siblings, candidate)
			}
		}
		if len(siblings) == 1 && (restored || siblings[0].ID == warm.ID) {
			return siblings, nil
		}
		if len(siblings) > 1 || !time.Now().Before(deadline) {
			return nil, fmt.Errorf("bridge at %s has %d enclosure siblings after startup", warm.Port, len(siblings))
		}
		if !restored {
			held, err := acquireBridge(warm.Location, time.Until(deadline))
			if err != nil {
				return nil, err
			}
			err = held.bridge.SetGPIO(0x40, 0x40)
			_ = held.release()
			if err != nil {
				return nil, fmt.Errorf("restore enclosure sibling at %s: %w", warm.Port, err)
			}
			restored = true
		}
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
