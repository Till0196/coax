// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package asicen

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
)

func TestModelsAreToldApartByTheirFriendlyNames(t *testing.T) {
	for _, c := range []struct {
		name, path string
		model      string
		deliveries []string
		product    uint16
	}{
		{"HDTV_PX_W3U3_V2 BDA Digital Tuner_2", `@device:pnp:\\?\usb#vid_0b06&pid_0006&mi_00#a&14cf1e6a&0&0000#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{ef4af615-7acd-45d2-b7b4-ded5c22a382e}`, "PX-W3U3 V2", []string{"ISDB-S"}, 6},
		{"HDTV_PX_W3U3 BDA Digital Tuner_1", `@device:pnp:\\?\usb#vid_0b06&pid_0005#5&1&0&1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{2E55751D-B8AD-4F98-8DE7-3B06C2D4CFE9}`, "PX-W3U3", []string{"ISDB-T"}, 5},
		// The PX-Q3PE publishes its first four under the PX-W3PE's strings.
		{"HDTV_PX_Q3PE BDA Digital Tuner_2", `@device:pnp:\\?\pci#ven_0000&dev_0000#4&1&0&00e0#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{b332d1a0-ce5e-4ccd-bbaa-5d27da1937f7}`, "PX-Q3PE", []string{"ISDB-T"}, 0},
		{"HDTV_PX_W3PE BDA Digital Tuner_2", `@device:pnp:\\?\pci#ven_0000&dev_0000#4&1&0&00e0#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{b332d1a0-ce5e-4ccd-bbaa-5d27da1937f7}`, "PX-W3PE", []string{"ISDB-T"}, 0},
		{"HDTV_PX_W3PEV2 BDA Digital Tuner_4", `@device:pnp:\\?\pci#ven_0000&dev_0000#4&1&0&00e0#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{955b2355-62fc-42bd-a0bb-e75e9451b15d}`, "PX-W3PE V2", []string{"ISDB-S"}, 0},
		{"HDTV_PX_S3U BDA Digital Tuner_0", `@device:pnp:\\?\usb#vid_0b06&pid_0001#5&1&0&1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{58d97867-8a41-4f07-97e3-f2819af81a92}`, "PX-S3U", []string{"ISDB-T", "ISDB-S"}, 1},
		// A filter not in the table is not taken for a PLEX one by its name.
		{"HDTV_PX_W3U3_V2 BDA Digital Tuner_5", `@device:pnp:\\?\usb#vid_0b06&pid_0006&mi_00#a&1#{71985f48-1ca1-11d3-9cc8-00c04f7971e0}\{a12cea30-b753-44e8-a027-9dc868d68888}`, "", nil, 6},
	} {
		tuner := vendors.Tuner{Name: c.name, Path: c.path}
		if model := Model(tuner); model != c.model {
			t.Errorf("%s: model %q, want %q", c.name, model, c.model)
		}
		if deliveries := Deliveries(tuner); !slices.Equal(deliveries, c.deliveries) {
			t.Errorf("%s: deliveries %q, want %q", c.name, deliveries, c.deliveries)
		}
		if product := USBProduct(tuner); product != c.product {
			t.Errorf("%s: product %#04x, want %#04x", c.name, product, c.product)
		}
	}
}

// Expected driver numbering and addresses come from the supplied INF and
// driver tables, not the order in which Windows happens to enumerate filters.
func TestPublishedPathsUseTheirOwnWiring(t *testing.T) {
	for _, c := range []struct {
		model     string
		prefix    string
		product   uint16
		refs      []string
		satellite map[byte]byte
	}{
		{"PX-W3PE", "HDTV_PX_W3PE ", 0,
			[]string{"{8c16b9c1-b2ec-4657-b69f-f1925fcfc174}", "{ead4691e-4ed3-4491-b193-8abdc2895010}", "{b332d1a0-ce5e-4ccd-bbaa-5d27da1937f7}", "{1bb610b9-a113-43ef-8707-0d0eb2786d0c}"}, map[byte]byte{0: 0x32, 1: 0x36}},
		{"PX-Q3PE", "HDTV_PX_Q3PE ", 0,
			[]string{"{8c16b9c1-b2ec-4657-b69f-f1925fcfc174}", "{ead4691e-4ed3-4491-b193-8abdc2895010}", "{b332d1a0-ce5e-4ccd-bbaa-5d27da1937f7}", "{1bb610b9-a113-43ef-8707-0d0eb2786d0c}", "{8181256c-91d3-440a-87a7-74e193e12ca6}", "{7d80835e-8f9c-4928-bcab-f0262cbb6bda}", "{f7dffb42-19bb-495c-8025-cae755351ddb}", "{4f47ce49-ca06-4482-8fda-aa130873f9fb}"}, map[byte]byte{0: 0x22, 1: 0x2a, 4: 0x26, 5: 0x2e}},
		{"PX-W3PE V2", "HDTV_PX_W3PEV2 ", 0,
			[]string{"{06cd74f9-2ec3-47de-b7c0-fbd1f87bb172}", "{67a09e55-6937-4197-9854-aebabb867cf0}", "{955b2355-62fc-42bd-a0bb-e75e9451b15d}", "{2ef1f363-01f6-4d40-abf0-bcf60412a91f}"}, map[byte]byte{0: 0x22, 4: 0x26}},
		{"PX-S3U", "HDTV_PX_S3U ", 1, []string{"{58d97867-8a41-4f07-97e3-f2819af81a92}"}, map[byte]byte{0: 0x32}},
		{"PX-S3U2", "HDTV_PX_S3U2 ", 3, []string{"{64936de7-6ab9-470a-b0e6-10014f83b75f}", "{6d693f4b-8575-40c6-9537-4515579d1cd8}"}, map[byte]byte{0: 0x32}},
		{"PX-W3U2", "HDTV_PX_W3U2 ", 4, []string{"{f08b8084-075c-4542-9405-c53c16c981ac}", "{c2aca3d2-a439-4f7f-b064-75362763edb7}"}, map[byte]byte{0: 0x32}},
		{"PX-W3U3", "HDTV_PX_W3U3 ", 5, []string{"{37a61614-c9cd-446f-8214-430b59d769fb}", "{2e55751d-b8ad-4f98-8de7-3b06c2d4cfe9}"}, map[byte]byte{0: 0x32}},
		{"PX-W3U3 V2", "HDTV_PX_W3U3_V2 ", 6, []string{"{fc7eee4c-58ab-47bd-a511-1149f49007ea}", "{92644c53-ca6c-40b3-89e0-91c8c40ad263}", "{ef4af615-7acd-45d2-b7b4-ded5c22a382e}", "{6430d7ee-7dc7-4978-9481-d9657cbd583f}"}, map[byte]byte{0: 0x22, 2: 0x26}},
	} {
		t.Run(c.model, func(t *testing.T) {
			for _, ref := range c.refs {
				path := "pci#ven_188b&dev_5220#board"
				if c.product != 0 {
					path = fmt.Sprintf("usb#vid_0b06&pid_%04x#bridge", c.product)
				}
				tuner := vendors.Tuner{Name: c.prefix + "BDA Digital Tuner", Path: path + "#{category}\\" + ref}
				if !Is(tuner) || Model(tuner) != c.model {
					t.Fatalf("not recognised: %+v", tuner)
				}
				known := filters[ref]
				want, satellite := c.satellite[known.index]
				if got, ok := SatelliteDemod(tuner); got != want || ok != satellite {
					t.Errorf("unit %d: satellite %#x/%v, want %#x/%v", known.index, got, ok, want, satellite)
				}
				if CarriesCard(tuner) != (known.index == 0) {
					t.Errorf("unit %d: wrong card owner", known.index)
				}
				cardPeer := known.index == 0 && (c.product == 4 || c.product == 5 || c.product == 6)
				if CardMayHavePeer(tuner) != cardPeer {
					t.Errorf("unit %d: wrong card sharing scope", known.index)
				}
				peer := c.model == "PX-W3U3 V2" && known.index >= 2
				if NeedsControlPeer(tuner) != peer {
					t.Errorf("unit %d: wrong control route", known.index)
				}
				if c.model == "PX-S3U" {
					if Delivery(tuner) != "" || len(Deliveries(tuner)) != 2 {
						t.Fatal("combined filter lost one standard")
					}
				} else if Delivery(tuner) == "" {
					t.Errorf("unit %d: missing dedicated delivery", known.index)
				}
			}
		})
	}
}

func TestPCISubsystemSeparatesSharedFilterGUIDs(t *testing.T) {
	for _, c := range []struct {
		subsystem, model string
		address          byte
	}{
		{"00010b06", "PX-W3PE", 0x36}, {"00020b06", "PX-Q3PE", 0x2a},
	} {
		tuner := vendors.Tuner{Name: "renamed tuner", Path: "pci#ven_188b&dev_5220&subsys_" + c.subsystem + "&rev_01#board#{category}\\{ead4691e-4ed3-4491-b193-8abdc2895010}"}
		address, ok := SatelliteDemod(tuner)
		if Model(tuner) != c.model || !ok || address != c.address {
			t.Fatalf("%s: %s %#x/%v", c.subsystem, Model(tuner), address, ok)
		}
	}
}

func TestControlPeerKeepsTheMatchingRFPathOpen(t *testing.T) {
	path := func(ref string) vendors.Tuner {
		return vendors.Tuner{Path: `usb#vid_0b06&pid_0006#bridge#{category}\` + ref}
	}
	s0 := path("{fc7eee4c-58ab-47bd-a511-1149f49007ea}")
	t1 := path("{92644c53-ca6c-40b3-89e0-91c8c40ad263}")
	s2 := path("{ef4af615-7acd-45d2-b7b4-ded5c22a382e}")
	t3 := path("{6430d7ee-7dc7-4978-9481-d9657cbd583f}")
	unknown := path("{a12cea30-b753-44e8-a027-9dc868d68888}")
	for _, c := range []struct {
		tuner vendors.Tuner
		want  int
	}{{s2, 0}, {t3, 1}, {s0, -1}, {t1, -1}, {unknown, -1}} {
		// Windows enumerates satellite first on the affected machine. A
		// terrestrial tuner must still retain the terrestrial peer.
		got := -1
		for i, candidate := range []vendors.Tuner{s0, t1, s2, t3, unknown} {
			if IsControlPeer(c.tuner, candidate) {
				if got != -1 {
					t.Fatal("multiple control peers for one tuner")
				}
				got = i
			}
		}
		if got != c.want {
			t.Errorf("%s: peer %d, want %d", reference(c.tuner), got, c.want)
		}
	}
}
