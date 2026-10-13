// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package logo

import (
	"bytes"
	"hash/crc32"
	"testing"
)

func TestLogoModulesAreNamedBySubstring(t *testing.T) {
	for info, want := range map[string]bool{
		"\x02\x07LOGO-02":    true,
		"\x02\x0aCS_LOGO-00": true,
		"\x02\x09CATV_LOGO":  true,
		"\x02\x05GENRE":      false,
		"\x01\x04LOGO":       false,
		"\x02\x09LOG":        false,
	} {
		if got := IsLogoModule([]byte(info)); got != want {
			t.Errorf("IsLogoModule(%q) = %v", info, got)
		}
	}
	if !IsCommonDataModel(0xfe) || !IsCommonDataModel(0xf1) || IsCommonDataModel(0xf2) {
		t.Error("common data models")
	}
}

func TestALogoModuleIsRead(t *testing.T) {
	data := []byte{0x02, 0x00, 0x02}
	// logo_id 0x123 with two services and a three-byte "PNG".
	data = append(data, 0x01, 0x23, 2)
	data = append(data, 0x00, 0x04, 0x40, 0xf1, 0x00, 0x65)
	data = append(data, 0x00, 0x04, 0x40, 0xf1, 0x00, 0x66)
	data = append(data, 0x00, 0x03, 1, 2, 3)
	// logo_id 5, no services, an empty picture.
	data = append(data, 0xfe, 0x05, 0, 0x00, 0x00)
	module, ok := ParseModule(data)
	if !ok || module.LogoType != 2 || len(module.Entries) != 2 {
		t.Fatalf("module %+v %v", module, ok)
	}
	first := module.Entries[0]
	if first.LogoID != 0x123 || len(first.Services) != 2 || !bytes.Equal(first.PNG, []byte{1, 2, 3}) {
		t.Errorf("first %+v", first)
	}
	if first.Services[1] != (Service{OriginalNetworkID: 4, TransportStreamID: 0x40f1, ServiceID: 0x66}) {
		t.Errorf("service %+v", first.Services[1])
	}
	if second := module.Entries[1]; second.LogoID != 5 || len(second.Services) != 0 {
		t.Errorf("second %+v", second)
	}
	// Short by a byte: nothing.
	if _, ok := ParseModule(data[:len(data)-1]); ok {
		t.Error("a short module read")
	}
}

func TestTheDataModuleOfACommonDataTableIsALogo(t *testing.T) {
	data := []byte{0x05, 0xfe, 0x23, 0xf0, 0x07, 0x00, 0x02, 0xaa, 0xbb, 0xcc}
	logo, ok := ParseCommonData(data)
	if !ok || logo.LogoType != 5 || logo.LogoID != 0x023 || logo.LogoVersion != 7 || !bytes.Equal(logo.PNG, []byte{0xaa, 0xbb}) {
		t.Errorf("logo %+v %v", logo, ok)
	}
	if _, ok := ParseCommonData(data[:8]); ok {
		t.Error("a short data module read")
	}
}

func TestTheCLUTIsTable5_7(t *testing.T) {
	for at, want := range map[int][4]byte{
		0:   {0, 0, 0, 255},
		7:   {255, 255, 255, 255},
		8:   {0, 0, 0, 0},
		9:   {170, 0, 0, 255},
		64:  {255, 255, 170, 255},
		65:  {0, 0, 0, 128},
		72:  {255, 255, 255, 128},
		73:  {170, 0, 0, 128},
		86:  {0, 170, 255, 128},
		107: {170, 85, 0, 128},
		127: {255, 255, 85, 128},
	} {
		if DefaultCLUT[at] != want {
			t.Errorf("CLUT %d = %v, want %v", at, DefaultCLUT[at], want)
		}
	}
}

func indexedPNG(withPalette bool) []byte {
	png := append([]byte(nil), pngSignature...)
	png = appendChunk(png, "IHDR", []byte{0, 0, 0, 2, 0, 0, 0, 1, 8, 3, 0, 0, 0})
	if withPalette {
		png = appendChunk(png, "PLTE", []byte{1, 2, 3})
	}
	png = appendChunk(png, "IDAT", []byte{0x78, 0x9c, 0x63, 0x60, 0x60, 0x00, 0x00, 0x00, 0x04, 0x00, 0x01})
	return appendChunk(png, "IEND", nil)
}

func TestAPaletteIsPutAfterTheHeader(t *testing.T) {
	png := indexedPNG(false)
	out := WithPalette(png)
	if !bytes.Equal(out[:33], png[:33]) {
		t.Error("the header moved")
	}
	if string(out[33:41]) != "\x00\x00\x03\x00PLTE" {
		t.Errorf("PLTE head %q", out[33:41])
	}
	if !bytes.Equal(out[41:44], []byte{0, 0, 0}) || !bytes.Equal(out[44:47], []byte{255, 0, 0}) {
		t.Errorf("palette %v", out[41:47])
	}
	trns := 33 + 8 + 768 + 4
	if string(out[trns:trns+8]) != "\x00\x00\x01\x00tRNS" {
		t.Errorf("tRNS head %q", out[trns:trns+8])
	}
	if out[trns+8+8] != 0 || out[trns+8+9] != 255 {
		t.Errorf("transparency %d %d", out[trns+8+8], out[trns+8+9])
	}
	if !bytes.Equal(out[trns+8+256+4:], png[33:]) {
		t.Error("the rest moved")
	}
	if crc32.ChecksumIEEE([]byte("IEND")) != 0xae426082 {
		t.Error("the chunk CRC")
	}
	// Already palettised, or not indexed: untouched.
	if !bytes.Equal(WithPalette(indexedPNG(true)), indexedPNG(true)) {
		t.Error("a palettised picture changed")
	}
	rgb := indexedPNG(false)
	rgb[25] = 2
	if !bytes.Equal(WithPalette(rgb), rgb) {
		t.Error("a picture that is not indexed changed")
	}
}
