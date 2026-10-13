// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package mode

import (
	"testing"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
)

// TestForOnlyTouchesCardsThatHaveOne checks that For dispatches each card to the
// right switcher, and guards a fault that reached hardware: the mode property is
// a TBS one, but a Digital Devices card answers the same identifier on the same
// GUID with a number of its own. That number is its filter's standard, which
// compares unequal to the wanted mode, so the next step was a write to another
// vendor's card -- observed on a MAX M4 before this test existed.
func TestForOnlyTouchesCardsThatHaveOne(t *testing.T) {
	for _, test := range []struct {
		name  string
		tuner filters.Moniker
		want  string
	}{
		{"a TBS card by vendor",
			filters.Moniker{
				Display: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016205#4&1&0#{guid}\{x}`,
				Name:    "TBS 6205se Tuner 0",
			}, "*mode.tbsSwitcher"},
		{"a TBS 6812 by its reference string",
			filters.Moniker{
				Display: `@device:pnp:\\?\pci#ven_544d&dev_6178&subsys_00016812#4&1&0#{guid}\{x}`,
				Name:    "TBS 6812 Tuner 0",
			}, "*mode.tbsSwitcher"},
		{"a Digital Devices card",
			filters.Moniker{
				Display: `@device:pnp:\\?\dd_dvb#ven_dd01&dev_000a&subsys_0050dd01&rev_00&tuner#5&1&0#{guid}\{x}`,
				Name:    "Digital Devices ISDB-T Tuner 1",
			}, "*mode.ddSwitcher"},
		{"somebody else's card is left alone",
			filters.Moniker{
				Display: `@device:pnp:\\?\usb#vid_0511&pid_0045#0#{guid}\{x}`,
				Name:    "Generic DVB-T Tuner",
			}, "*mode.noSwitcher"},
	} {
		switcher := For(test.tuner)
		if got := typeName(switcher); got != test.want {
			t.Errorf("%s: got %s, want %s", test.name, got, test.want)
		}
		// A switcher with nothing to settle must not fail: callers treat an
		// error here as a reason to refuse the tune. A tbsSwitcher may fail
		// (there is no hardware behind it), so only the other two are checked.
		if _, err := switcher.BeforeGraph(tuning.TuneProfile{System: "ISDBT"}); err != nil {
			if test.want == "*mode.noSwitcher" || test.want == "*mode.ddSwitcher" {
				t.Errorf("%s: BeforeGraph should do nothing, got %v", test.name, err)
			}
		}
	}
}

func typeName(value any) string {
	switch value.(type) {
	case *tbsSwitcher:
		return "*mode.tbsSwitcher"
	case *ddSwitcher:
		return "*mode.ddSwitcher"
	case *noSwitcher:
		return "*mode.noSwitcher"
	}
	return "unknown"
}
