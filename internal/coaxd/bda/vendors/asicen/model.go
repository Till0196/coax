// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

// filter is one tuner path a PLEX model publishes: the reference string of
// its capture filter, which must belong to the same device instance, and what
// it receives.
type filter struct {
	capture    string
	deliveries []string
	index      byte
}

var (
	satellite   = []string{"ISDB-S"}
	terrestrial = []string{"ISDB-T"}
)

// filters are the tuner filters by their reference strings. The PX-Q3PE's
// first four are the PX-W3PE's, so the reference string alone does not tell
// the models apart.
var filters = map[string]filter{
	"{8c16b9c1-b2ec-4657-b69f-f1925fcfc174}": {"{49b710c9-9c30-4d27-b664-1a9fa41dc27e}", satellite, 0},                    // PX-Q3PE, PX-W3PE 0
	"{ead4691e-4ed3-4491-b193-8abdc2895010}": {"{5fdb68db-1b0a-496f-b47a-7db2f8057d50}", satellite, 1},                    // PX-Q3PE, PX-W3PE 1
	"{b332d1a0-ce5e-4ccd-bbaa-5d27da1937f7}": {"{21915155-d273-4023-9258-258416f48bd9}", terrestrial, 2},                  // PX-Q3PE, PX-W3PE 2
	"{1bb610b9-a113-43ef-8707-0d0eb2786d0c}": {"{d3b05569-c1c2-4f8d-b03c-dd7ba04ebc6a}", terrestrial, 3},                  // PX-Q3PE, PX-W3PE 3
	"{8181256c-91d3-440a-87a7-74e193e12ca6}": {"{84503d7b-3f36-4271-9a0e-72e13124b22b}", satellite, 4},                    // PX-Q3PE 4
	"{7d80835e-8f9c-4928-bcab-f0262cbb6bda}": {"{e9e1b74f-04b0-4016-a981-7b21482c9456}", satellite, 5},                    // PX-Q3PE 5
	"{f7dffb42-19bb-495c-8025-cae755351ddb}": {"{fa8b2a09-53d3-42f7-b9c8-6ce79092f3f6}", terrestrial, 6},                  // PX-Q3PE 6
	"{4f47ce49-ca06-4482-8fda-aa130873f9fb}": {"{da7b3902-c5f9-4829-a46f-f55922324cd5}", terrestrial, 7},                  // PX-Q3PE 7
	"{06cd74f9-2ec3-47de-b7c0-fbd1f87bb172}": {"{e367dc1b-bfb5-4698-a036-f49a19a63c55}", satellite, 0},                    // PX-W3PE V2 0
	"{67a09e55-6937-4197-9854-aebabb867cf0}": {"{041ca304-19fc-4885-93a0-657c2e2c83bf}", terrestrial, 2},                  // PX-W3PE V2 2
	"{955b2355-62fc-42bd-a0bb-e75e9451b15d}": {"{5b24d815-b61b-4c85-9ed6-dc2f1772e204}", satellite, 4},                    // PX-W3PE V2 4
	"{2ef1f363-01f6-4d40-abf0-bcf60412a91f}": {"{52a7dfeb-de46-4b7f-9196-c1a8ec86d7d7}", terrestrial, 6},                  // PX-W3PE V2 6
	"{58d97867-8a41-4f07-97e3-f2819af81a92}": {"{7e65574d-e061-4129-8998-51881804e4a4}", []string{"ISDB-T", "ISDB-S"}, 0}, // PX-S3U
	"{64936de7-6ab9-470a-b0e6-10014f83b75f}": {"{25dcdf37-fb57-4515-8971-ae392196ff27}", satellite, 0},                    // PX-S3U2 0
	"{6d693f4b-8575-40c6-9537-4515579d1cd8}": {"{80e65bb1-eed0-4039-bdfc-4c365ce02477}", terrestrial, 1},                  // PX-S3U2 1
	"{f08b8084-075c-4542-9405-c53c16c981ac}": {"{7a172030-e4c3-4903-91d1-d831701ae6b9}", satellite, 0},                    // PX-W3U2 0
	"{c2aca3d2-a439-4f7f-b064-75362763edb7}": {"{eb561c45-dcbc-410a-bca2-98a05309eb53}", terrestrial, 1},                  // PX-W3U2 1
	"{37a61614-c9cd-446f-8214-430b59d769fb}": {"{34a320cb-f54b-47cf-ad49-e66a23a20630}", satellite, 0},                    // PX-W3U3 0
	"{2e55751d-b8ad-4f98-8de7-3b06c2d4cfe9}": {"{71c25d40-b6bd-46a4-8363-329d28e663ab}", terrestrial, 1},                  // PX-W3U3 1
	"{fc7eee4c-58ab-47bd-a511-1149f49007ea}": {"{5ea23aed-35cb-44d4-8d85-748f36b1ac5b}", satellite, 0},                    // PX-W3U3 V2 0
	"{92644c53-ca6c-40b3-89e0-91c8c40ad263}": {"{e2b5be0c-39e8-4d9f-bc14-79ba8805181a}", terrestrial, 1},                  // PX-W3U3 V2 1
	"{ef4af615-7acd-45d2-b7b4-ded5c22a382e}": {"{d572e115-62c7-49ff-956c-84369aada823}", satellite, 2},                    // PX-W3U3 V2 2
	"{6430d7ee-7dc7-4978-9481-d9657cbd583f}": {"{974860c1-a2a9-48e8-82d2-5e8c64a39878}", terrestrial, 3},                  // PX-W3U3 V2 3
}

// models are the products by the start of the friendly names their drivers
// give the tuner filters.
var models = []struct{ prefix, model string }{
	{"HDTV_PX_Q3PE ", "PX-Q3PE"},
	{"HDTV_PX_W3PE ", "PX-W3PE"},
	{"HDTV_PX_W3PEV2 ", "PX-W3PE V2"},
	{"HDTV_PX_S3U ", "PX-S3U"},
	{"HDTV_PX_S3U2 ", "PX-S3U2"},
	{"HDTV_PX_W3U2 ", "PX-W3U2"},
	{"HDTV_PX_W3U3 ", "PX-W3U3"},
	{"HDTV_PX_W3U3_V2 ", "PX-W3U3 V2"},
}

// lookUp finds a PLEX tuner filter by the reference string its path ends in.
func lookUp(t vendors.Tuner) (filter, bool) {
	known, ok := filters[reference(t)]
	return known, ok
}

func MatchCapture(tuner, capture vendors.Tuner) bool {
	t, c := strings.ToLower(tuner.Path), strings.ToLower(capture.Path)
	ti, ci := strings.Index(t, "#{"), strings.Index(c, "#{")
	if ti < 0 || ci < 0 || t[:ti] != c[:ci] {
		return false
	}
	known, ok := lookUp(tuner)
	return ok && strings.HasSuffix(c, known.capture)
}

// Is recognises a published ASICEN tuner filter independently of its generation.
func Is(t vendors.Tuner) bool { _, ok := lookUp(t); return ok }

// Delivery is the single standard of a dedicated tuner. The original S3U
// publishes both standards on one filter and is therefore left unspecified.
func Delivery(t vendors.Tuner) string {
	known, ok := lookUp(t)
	if !ok || len(known.deliveries) != 1 {
		return ""
	}
	if known.deliveries[0] == "ISDB-S" {
		return "ISDBS"
	}
	return "ISDBT"
}

// CarriesCard identifies the filter owning a driver's card state. USB bridge
// candidates remain separate because the driver resolves their shared card
// state from hardware metadata; a PCI device has one published owner.
func CarriesCard(t vendors.Tuner) bool {
	known, ok := lookUp(t)
	return ok && known.index == 0
}

// CardMayHavePeer describes the multi-bridge USB enclosures whose card
// operations may be forwarded by the driver. It is separate from I2C routing.
func CardMayHavePeer(t vendors.Tuner) bool {
	if !CarriesCard(t) {
		return false
	}
	switch USBProduct(t) {
	case 4, 5, 6:
		return true
	}
	return false
}

// NeedsControlPeer describes a path whose demodulators are reached through
// another bridge. This is a wiring property, not a transport capability.
func NeedsControlPeer(t vendors.Tuner) bool {
	switch reference(t) {
	case "{ef4af615-7acd-45d2-b7b4-ded5c22a382e}", "{6430d7ee-7dc7-4978-9481-d9657cbd583f}":
		return true
	}
	return false
}

// OnFirstBridge identifies the control paths paired with NeedsControlPeer.
func OnFirstBridge(t vendors.Tuner) bool {
	switch reference(t) {
	case "{fc7eee4c-58ab-47bd-a511-1149f49007ea}", "{92644c53-ca6c-40b3-89e0-91c8c40ad263}":
		return true
	}
	return false
}

// IsControlPeer pairs a second-bridge tuner with the first bridge's filter
// of the same reception standard.
func IsControlPeer(tuner, candidate vendors.Tuner) bool {
	if !NeedsControlPeer(tuner) || !OnFirstBridge(candidate) {
		return false
	}
	t, _ := lookUp(tuner)
	c, _ := lookUp(candidate)
	return t.index == c.index+2
}

// reference is the reference string a filter's path ends in.
func reference(t vendors.Tuner) string {
	path := strings.ToLower(t.Path)
	if at := strings.LastIndex(path, "\\{"); at >= 0 {
		return path[at+1:]
	}
	return ""
}

// SatelliteDemod returns the 8-bit I2C address for a published satellite path.
// The S3U's combined filter can also tune satellite. Shared PCI filter GUIDs
// need the device identity to distinguish the original W3PE from the Q3PE.
func SatelliteDemod(t vendors.Tuner) (byte, bool) {
	known, ok := lookUp(t)
	if !ok || !slices.Contains(known.deliveries, "ISDB-S") {
		return 0, false
	}
	switch Model(t) {
	case "PX-S3U", "PX-S3U2", "PX-W3U2", "PX-W3U3":
		return 0x32, true
	case "PX-W3PE":
		address, ok := map[byte]byte{0: 0x32, 1: 0x36}[known.index]
		return address, ok
	case "PX-Q3PE":
		address, ok := map[byte]byte{0: 0x22, 1: 0x2a, 4: 0x26, 5: 0x2e}[known.index]
		return address, ok
	case "PX-W3PE V2":
		address, ok := map[byte]byte{0: 0x22, 4: 0x26}[known.index]
		return address, ok
	case "PX-W3U3 V2":
		address, ok := map[byte]byte{0: 0x22, 2: 0x26}[known.index]
		return address, ok
	}
	return 0, false
}

// Model uses the USB/PCI identity first, then the driver's friendly name
// where no hardware identity is available. An unrecognised filter is empty.
func Model(t vendors.Tuner) string {
	if _, ok := lookUp(t); !ok {
		return ""
	}
	switch USBProduct(t) {
	case 1:
		return "PX-S3U"
	case 3:
		return "PX-S3U2"
	case 4:
		return "PX-W3U2"
	case 5:
		return "PX-W3U3"
	case 6:
		return "PX-W3U3 V2"
	}
	path := strings.ToLower(t.Path)
	for _, model := range []struct{ identity, name string }{
		{"pci#ven_188b&dev_5220&subsys_00010b06", "PX-W3PE"},
		{"pci#ven_188b&dev_5220&subsys_00020b06", "PX-Q3PE"},
		{"pci#ven_188b&dev_5220&subsys_00030b06", "PX-W3PE V2"},
	} {
		if strings.Contains(path, model.identity) {
			return model.name
		}
	}
	for _, known := range models {
		if strings.HasPrefix(t.Name, known.prefix) {
			return known.model
		}
	}
	return ""
}

// Deliveries is what a PLEX tuner filter receives, or nil for another device.
func Deliveries(t vendors.Tuner) []string {
	known, ok := lookUp(t)
	if !ok {
		return nil
	}
	return append([]string(nil), known.deliveries...)
}

// USBProduct is a PLEX USB model's product ID, or 0 for a card or another
// device.
func USBProduct(t vendors.Tuner) uint16 {
	path := strings.ToLower(t.Path)
	at := strings.Index(path, "usb#vid_0b06&pid_")
	if at < 0 {
		return 0
	}
	digits := path[at+len("usb#vid_0b06&pid_"):]
	if len(digits) < 4 {
		return 0
	}
	product, err := strconv.ParseUint(digits[:4], 16, 16)
	if err != nil {
		return 0
	}
	return uint16(product)
}
