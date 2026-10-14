// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/internal/coaxd/usb/bridge/asv5211"
	"github.com/Till0196/coax/pkg/usbio"
)

// runLoadASV52xxFirmware uploads a firmware image to a cold PX-S3U/S3U2/W3U2/
// W3U3/W3U3 V2 and starts it. Every generation uploads through the same
// protocol; only the image and its jump address differ, and both come from the
// image itself (read out of a PLEX loader module, or recognised by digest), so
// --firmware-index is only for an image this program has not seen before.
func runLoadASV52xxFirmware(subcommand string, arguments []string, stdout, stderr *os.File) error {
	flags := flag.NewFlagSet("coaxd "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	var selector, firmwareDir, firmwarePath, firmwareIndexText string
	flags.StringVar(&selector, "device", "", "cold-device match index or USB bus:address")
	flags.StringVar(&firmwareDir, "firmware-dir", "", "firmware directory (OS default when omitted)")
	flags.StringVar(&firmwarePath, "firmware", "", "ASV52xx firmware: a decoded 0x4000-byte image, as fwextract writes (default: "+asv5211.FirmwareFile+" from the firmware directory)")
	flags.StringVar(&firmwareIndexText, "firmware-index", "", "firmware jump address (e.g. 0x5399); only needed for an image this program does not recognise")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%s takes no positional arguments", subcommand)
	}
	// Always a file, never unpacked from a vendor package here: what is uploaded
	// should be something the operator holds and can check.
	defaulted := firmwarePath == ""
	if defaulted {
		options := device.Options{FirmwareDir: firmwareDir}
		found, err := device.FindFirmware(options.FirmwareDirOrDefault(), asv5211.FirmwareFile)
		if err != nil {
			return fmt.Errorf("%w\nrun fwextract over PLEX's PX-SERIES_ver.1.0_Linux_Driver.zip to produce it, or name an image with --firmware", err)
		}
		firmwarePath = found
	}
	firmwareIndex := -1
	if firmwareIndexText != "" {
		value, err := strconv.ParseUint(firmwareIndexText, 0, 16)
		if err != nil {
			return fmt.Errorf("firmware-index %q is not an address in 0..0xffff", firmwareIndexText)
		}
		firmwareIndex = int(value)
	}
	firmware, err := asv5211.ReadFirmware(firmwarePath)
	if err != nil {
		if defaulted && errors.Is(err, fs.ErrNotExist) {
			// Say where an image comes from, not just that the file is missing.
			return fmt.Errorf("%w\nrun fwextract over PLEX's PX-SERIES_ver.1.0_Linux_Driver.zip to produce it, or name an image with --firmware", err)
		}
		return err
	}
	switch {
	case firmwareIndex >= 0:
		// An explicit address wins, but warn on a mismatch: starting a known
		// build at the wrong address wedges the board.
		if firmware.Known && uint16(firmwareIndex) != firmware.Index {
			fmt.Fprintf(stderr, "warning: %s carries %s, which starts at %#04x, but --firmware-index says %#04x\n",
				firmwarePath, firmware.Description, firmware.Index, firmwareIndex)
		}
	case firmware.Known:
		firmwareIndex = int(firmware.Index)
	default:
		return fmt.Errorf("%s is not a firmware image this program recognises (SHA-256 %s); pass --firmware-index with the address it starts at",
			firmwarePath, firmware.Digest())
	}
	transport, err := usbio.Open(selector, []usbio.ID{{Vendor: 0x1738, Product: 0x5211}, {Vendor: 0x1738, Product: 0x5216}})
	if err != nil {
		return fmt.Errorf("open cold ASV52xx tuner: %w", err)
	}
	defer transport.Close()
	name := transport.Name()
	if err := asv5211.UploadFirmwareAt(transport, firmware.Image, uint16(firmwareIndex)); err != nil {
		return err
	}
	if firmware.Description != "" {
		fmt.Fprintf(stdout, "loaded %s firmware\n", firmware.Description)
	}
	fmt.Fprintf(stdout, "ASV52xx firmware loaded on %s with jump address %#04x; waiting device should re-enumerate\n", name, firmwareIndex)
	return nil
}
