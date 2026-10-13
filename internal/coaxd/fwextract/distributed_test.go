// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package fwextract

import (
	"archive/tar"
	"bytes"
	"testing"
)

// TestFirmwareShippedAsAFileIsRecognisedByName is the case a vendor makes when
// they do not hide the image: the file is the firmware, and there is nothing
// to recover from it but its name.
func TestFirmwareShippedAsAFileIsRecognisedByName(t *testing.T) {
	image := bytes.Repeat([]byte{0x5a}, 8192)
	for name, want := range map[string]bool{
		"dvb-usb-id5520se.fw":          true,
		"dvb-demod-si2183-b60-01.fw":   true,
		"dvb-tuner-si2158-a20-01.fw":   true,
		"sms1xxx-nova-a-dvbt-01.fw":    true,
		"firmware/dvb-usb-id5530.fw":   true,
		"readme.txt":                   false,
		"driver.sys":                   false,
		"dvb-usb-id5520se.fw.txt":      false,
		"DVB-USB-ID5520SE.FW":          true, // the name is folded before matching
		"isdbt_rio.inp":                true,
		"fm_radio_rio.inp":             true,
		"x64Inf/dvb_nova_12mhz_b0.inp": true,
		"setup.inp":                    false,
	} {
		if got := IsDistributedFirmware(name, image); got != want {
			t.Errorf("IsDistributedFirmware(%q) = %v, want %v", name, got, want)
		}
	}
	// Size is part of it: an archive that happens to be named like firmware is
	// not firmware, and neither is an empty file.
	if IsDistributedFirmware("dvb-usb-id5520se.fw", []byte{1, 2, 3}) {
		t.Error("three bytes should not pass for a microcontroller image")
	}
}

// TestCollectTakesEveryImageInAPackage is what provisioning a board needs: a
// vendor archive carries the bridge's firmware and the demodulator's and
// sometimes the tuner's, and a board wants all of them.
func TestCollectTakesEveryImageInAPackage(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, name := range []string{
		"dvb-usb-id5520se.fw",
		"dvb-demod-si2183-b60-01.fw",
		"dvb-tuner-si2158-a20-01.fw",
		"readme.txt",
	} {
		image := bytes.Repeat([]byte{0x11}, 4096)
		if err := writer.WriteHeader(&tar.Header{Name: name, Size: int64(len(image)), Mode: 0o644}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(image); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	found := Collect(archive.Bytes(), "tbs-tuner-firmwares.tar")
	if len(found) != 3 {
		names := make([]string, 0, len(found))
		for _, one := range found {
			names = append(names, one.FileName)
		}
		t.Fatalf("collected %v, want the three firmware files and not the readme", names)
	}
	for _, one := range found {
		if len(one.Image) != 4096 {
			t.Errorf("%s came out %d bytes", one.FileName, len(one.Image))
		}
	}
}
