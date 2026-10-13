// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package usb

import (
	"errors"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/usb/usbio"
)

func TestLookupAcceptsAliasesAndPrefix(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"t230", "usb-t230", " T230 ", "us3pout", "usb-us3pout",
		"px4", "pxw3u4", "usb-pxq3pe4", "pxmlt", "isdb6014",
		"isdb2056", "m1ur", "s1ur", "isdbt2071", "s270", "pxs1ud",
	} {
		if Lookup(name) == nil {
			t.Errorf("Lookup(%q) = nil; want a backend", name)
		}
	}
	if Lookup("dvb") != nil {
		t.Error("Lookup(\"dvb\") matched a USB backend")
	}
}

func TestBackendsAreComplete(t *testing.T) {
	t.Parallel()
	for _, backend := range Backends() {
		if backend.Name == "" || backend.Description == "" || backend.Open == nil || backend.Supports == nil {
			t.Errorf("backend %+v is missing required fields", backend)
		}
		if Lookup(backend.Name) == nil {
			t.Errorf("backend %q is not reachable through Lookup", backend.Name)
		}
		// A cold board nothing boots is one the daemon never lists.
		if (len(backend.ColdIDs) > 0) != (backend.Boot != nil) {
			t.Errorf("backend %q has cold identifiers without a Boot, or a Boot without them", backend.Name)
		}
	}
}

func TestOpenRejectsUnknownBackend(t *testing.T) {
	t.Parallel()
	if _, err := Open("nosuchtuner", Options{}); err == nil {
		t.Fatal("Open succeeded for an unknown backend")
	}
}

func TestBackendNamesAndAliasesAreUnique(t *testing.T) {
	t.Parallel()
	// Two backends answering to the same name would make -backend ambiguous.
	seen := map[string]string{}
	for _, backend := range Backends() {
		for _, name := range append([]string{backend.Name}, backend.Aliases...) {
			if owner, taken := seen[name]; taken {
				t.Errorf("%q is claimed by both %s and %s", name, owner, backend.Name)
			}
			seen[name] = backend.Name
		}
	}
}

// TestEveryBackendDeclaresItsUSBIDs guards the registry against a board that
// cannot be detected: a Backend without IDs still scans when named explicitly,
// so nothing fails at build time, but it never appears in the list subcommand.
func TestEveryBackendDeclaresItsUSBIDs(t *testing.T) {
	t.Parallel()
	for _, backend := range Backends() {
		if len(backend.IDs) == 0 {
			t.Errorf("backend %q declares no USB IDs, so Detect can never find it", backend.Name)
		}
	}
}

// TestNoTwoBackendsClaimTheSameUSBID keeps a device from being reported twice
// under different backend names, which would leave a caller unable to tell
// which one to scan with.
func TestNoTwoBackendsClaimTheSameUSBID(t *testing.T) {
	t.Parallel()
	owner := map[usbio.ID]string{}
	for _, backend := range Backends() {
		for _, id := range backend.IDs {
			if previous, taken := owner[id]; taken {
				t.Errorf("USB ID %04x:%04x is claimed by both %q and %q",
					id.Vendor, id.Product, previous, backend.Name)
				continue
			}
			owner[id] = backend.Name
		}
	}
}

// TestBackendsAreSelfConsistent checks the metadata the list subcommand prints:
// every backend needs a description, and whatever it lists in Deliveries its
// Supports has to accept.
func TestBackendsAreSelfConsistent(t *testing.T) {
	t.Parallel()
	for _, backend := range Backends() {
		if backend.Description == "" {
			t.Errorf("backend %q has no description to list", backend.Name)
		}
		if len(backend.Deliveries) == 0 {
			t.Errorf("backend %q lists no delivery systems", backend.Name)
		}
		for _, delivery := range backend.Deliveries {
			if !backend.Supports(delivery) {
				t.Errorf("backend %q lists %q but Supports rejects it", backend.Name, delivery)
			}
		}
	}
}

// TestEveryBackendDescribesItsLayout checks the static tuner list each backend
// publishes against what the backend says it can do. The list is what a listing
// answers with without opening a board, so a disagreement would have a caller
// pick a tuner for a delivery system it cannot reach.
func TestEveryBackendDescribesItsLayout(t *testing.T) {
	t.Parallel()
	for _, backend := range Backends() {
		if backend.Layout == nil {
			t.Errorf("backend %q publishes no tuner layout", backend.Name)
			continue
		}
		for _, id := range backend.IDs {
			tuners := backend.Layout(id)
			if len(tuners) == 0 {
				t.Errorf("backend %q describes no tuners for %04x:%04x", backend.Name, id.Vendor, id.Product)
				continue
			}
			for index, tuner := range tuners {
				if tuner.Index != index {
					t.Errorf("backend %q names tuner %d at position %d", backend.Name, tuner.Index, index)
				}
				if tuner.Name == "" {
					t.Errorf("backend %q leaves tuner %d unnamed", backend.Name, index)
				}
				if len(tuner.Deliveries) == 0 {
					t.Errorf("backend %q gives tuner %d no delivery systems", backend.Name, index)
				}
				for _, delivery := range tuner.Deliveries {
					if !backend.Supports(delivery) {
						t.Errorf("backend %q gives tuner %d %q, which the backend does not support",
							backend.Name, index, delivery)
					}
				}
			}
		}
	}
}

// TestLayoutAnswersThroughTheRegistry checks the registry's own accessor, which
// is what a caller outside this package uses.
func TestLayoutAnswersThroughTheRegistry(t *testing.T) {
	t.Parallel()
	// PX-W3U4: two satellite tuners then two terrestrial ones.
	tuners := Layout("px4", usbio.ID{Vendor: 0x0511, Product: 0x083f})
	if len(tuners) != 4 {
		t.Fatalf("px4 has %d tuners, want 4", len(tuners))
	}
	for index, want := range []string{"ISDB-S", "ISDB-S", "ISDB-T", "ISDB-T"} {
		if got := tuners[index].Deliveries; len(got) != 1 || got[0] != want {
			t.Errorf("px4 tuner %d reaches %v, want %q", index, got, want)
		}
	}
	if Layout("nonesuch", usbio.ID{}) != nil {
		t.Error("an unknown backend produced a layout")
	}
}

// TestRecoverAndOpenKeepsTheFirstError pins what a caller is told when the reset
// cannot run. Only macOS can reset a device; elsewhere the caller wants the
// failure it already had, not the news that an unasked-for recovery was
// unavailable.
func TestRecoverAndOpenKeepsTheFirstError(t *testing.T) {
	t.Parallel()
	// No device carries these identifiers, so the lookup inside the recovery
	// finds nothing and the reset cannot run -- which is the same shape as a
	// system that has no reset at all.
	firmwareMissing := errors.New("firmware not found")
	attempts := 0
	backend := &Backend{
		Name: "test",
		IDs:  []usbio.ID{{Vendor: 0xffff, Product: 0xffff}},
		Open: func(Options) (Board, error) {
			attempts++
			return nil, firmwareMissing
		},
	}
	board, err := recoverAndOpen(backend, Options{}, firmwareMissing)
	if board != nil || !errors.Is(err, firmwareMissing) {
		t.Errorf("got (%v, %v), want the original failure back", board, err)
	}
	if attempts != 0 {
		t.Errorf("the board was opened again %d times without the device being reset", attempts)
	}
}
