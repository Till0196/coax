// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tuning

import (
	"errors"
	"fmt"
	"testing"
)

func TestParseLNBTakesEverySpellingAChannelMightUse(t *testing.T) {
	for _, one := range []struct {
		written string
		want    LNB
	}{
		{"", LNBUnset},
		{"off", LNBOff}, {"OFF", LNBOff}, {"0", LNBOff}, {"none", LNBOff},
		{"low", LNBLow}, {"LOW", LNBLow}, {"11", LNBLow}, {"13", LNBLow},
		{"high", LNBHigh}, {"on", LNBHigh}, {"15", LNBHigh}, {"18", LNBHigh},
		// The channel-file spellings arrive quoted and padded often enough
		// that the shared normaliser is what reads them.
		{" \"15\" ", LNBHigh},
	} {
		got, err := ParseLNB(map[string]string{"VOLTAGE": one.written})
		if err != nil || got != one.want {
			t.Errorf("VOLTAGE=%q parsed to %v (%v), want %v", one.written, got, err, one.want)
		}
	}
}

// A number that is not one of the two levels is refused rather than passed on:
// it used to reach the Linux DVB frontend as though SEC_VOLTAGE_15 existed.
func TestParseLNBRefusesAVoltageNoTunerHas(t *testing.T) {
	for _, written := range []string{"12", "14", "24", "yes", "-1"} {
		if _, err := ParseLNB(map[string]string{"VOLTAGE": written}); err == nil {
			t.Errorf("VOLTAGE=%q should have been refused", written)
		}
	}
}

func TestLNBNamesItselfTheWayItIsWritten(t *testing.T) {
	for _, one := range []struct {
		lnb  LNB
		want string
	}{{LNBUnset, ""}, {LNBOff, "off"}, {LNBLow, "low"}, {LNBHigh, "high"}} {
		if got := one.lnb.String(); got != one.want {
			t.Errorf("%d.String() = %q, want %q", one.lnb, got, one.want)
		}
	}
}

// On the boards where the voltage is the polarization, the polarization
// decides whether the channel arrives at all while the level is a preference.
// Asking for no supply beats both.
func TestSatelliteSupplyLetsThePolarizationDecideTheLevel(t *testing.T) {
	for _, one := range []struct {
		name       string
		properties map[string]string
		want       Supply
	}{
		{"horizontal is the higher voltage",
			map[string]string{"POLARIZATION": "H"}, Supply{High: true, Explicit: true}},
		{"vertical is the lower one",
			map[string]string{"POLARIZATION": "V"}, Supply{Explicit: true}},
		{"left-hand circular sits with horizontal",
			map[string]string{"POLARIZATION": "L"}, Supply{High: true, Explicit: true}},
		{"the polarization wins over the level",
			map[string]string{"POLARIZATION": "V", "VOLTAGE": "18"}, Supply{Explicit: true}},
		{"the level decides when no polarization is given",
			map[string]string{"VOLTAGE": "18"}, Supply{High: true, Explicit: true}},
		{"and the lower level likewise",
			map[string]string{"VOLTAGE": "13"}, Supply{Explicit: true}},
		{"no supply beats the polarization",
			map[string]string{"POLARIZATION": "H", "VOLTAGE": "off"}, Supply{Off: true, Explicit: true}},
		{"a channel that says nothing gets no supply, and did not ask for it",
			nil, Supply{Off: true}},
		{"a polarization of off is a file with none to give, not a choice",
			map[string]string{"POLARIZATION": "OFF"}, Supply{Off: true}},
	} {
		t.Run(one.name, func(t *testing.T) {
			supply, err := SatelliteSupply(one.properties)
			if err != nil {
				t.Fatalf("SatelliteSupply: %v", err)
			}
			if supply != one.want {
				t.Errorf("got %+v, want %+v", supply, one.want)
			}
		})
	}
}

// A board that cannot cut its supply is right to refuse a channel that asked
// for none, and would be broken if it refused every channel that never said.
func TestApplySupplyOnlyInsistsOnAnOffTheChannelAskedFor(t *testing.T) {
	for _, one := range []struct {
		name    string
		supply  Supply
		refused bool
		calls   []bool
	}{
		{"a channel that asked for none is told the board cannot",
			Supply{Off: true, Explicit: true}, true, []bool{true}},
		{"a channel that said nothing settles for the supply being wired on",
			Supply{Off: true}, false, []bool{true, false}},
		{"a channel that asked for a level never reaches the question",
			Supply{High: true, Explicit: true}, false, []bool{false}},
	} {
		t.Run(one.name, func(t *testing.T) {
			var asked []bool
			err := ApplySupply(one.supply, func(_, off bool) error {
				asked = append(asked, off)
				if off {
					return fmt.Errorf("%w: wired on", ErrNoLNBSwitch)
				}
				return nil
			})
			if one.refused != (err != nil) {
				t.Errorf("ApplySupply returned %v", err)
			}
			if len(asked) != len(one.calls) {
				t.Fatalf("the board was set %v, wanted %v", asked, one.calls)
			}
			for index, off := range one.calls {
				if asked[index] != off {
					t.Errorf("call %d asked off=%v, wanted %v", index, asked[index], off)
				}
			}
		})
	}
}

// An error that is not about cutting the supply is the board's answer, and is
// not retried behind its back.
func TestApplySupplyDoesNotRetryAnUnrelatedFailure(t *testing.T) {
	calls := 0
	err := ApplySupply(Supply{Off: true}, func(bool, bool) error {
		calls++
		return errors.New("the bus is not answering")
	})
	if err == nil {
		t.Error("a failure to set the supply was swallowed")
	}
	if calls != 1 {
		t.Errorf("the board was set %d times, wanted once", calls)
	}
}

func TestWantsSupplyIsOnlyTheTwoLevels(t *testing.T) {
	for lnb, want := range map[LNB]bool{
		LNBUnset: false, LNBOff: false, LNBLow: true, LNBHigh: true,
	} {
		if got := WantsSupply(lnb); got != want {
			t.Errorf("WantsSupply(%v) = %v, want %v", lnb, got, want)
		}
	}
}

// The four spellings a DVBv5 file carries are whole words; other tools write
// the single letters. Both are read, and "off" means the file has no
// polarization to give rather than a voltage to choose.
func TestPolarizationReadsTheDVBv5Spellings(t *testing.T) {
	for _, one := range []struct {
		written     string
		high, known bool
	}{
		{"HORIZONTAL", true, true}, {"H", true, true},
		{"LEFT", true, true}, {"L", true, true},
		{"VERTICAL", false, true}, {"V", false, true},
		{"RIGHT", false, true}, {"R", false, true},
		{"OFF", false, false}, {"", false, false}, {"AUTO", false, false},
	} {
		high, known := PolarizationHigh(map[string]string{"POLARIZATION": one.written})
		if high != one.high || known != one.known {
			t.Errorf("POLARIZATION=%q gave high=%v known=%v, want high=%v known=%v",
				one.written, high, known, one.high, one.known)
		}
	}
}

// A linear polarization needs a voltage a switched board cannot choose, and
// the tone reaches a satellite it cannot select. Feeding the line anyway locks
// the wrong polarization or satellite and reads as an aerial fault.
func TestSwitchedSupplyRefusesWhatASwitchCannotDo(t *testing.T) {
	for _, test := range []struct {
		what       string
		properties map[string]string
		wantErr    error
	}{
		{"horizontal", map[string]string{"POLARIZATION": "H"}, ErrNoLNBVoltageChoice},
		{"vertical", map[string]string{"POLARIZATION": "VERTICAL"}, ErrNoLNBVoltageChoice},
		{"the tone", map[string]string{"TONE": "on", "VOLTAGE": "15"}, ErrNoTone},
	} {
		_, err := SwitchedSupply(test.properties)
		if !errors.Is(err, test.wantErr) {
			t.Errorf("%s: SwitchedSupply(%v) = %v, want %v", test.what, test.properties, err, test.wantErr)
		}
	}
}

// Circular polarization is a property of the carrier, not a request: Japan's
// satellites separate left from right by intermediate frequency, so a channel
// naming one asks nothing of the supply.
func TestSwitchedSupplyPassesWhatTheLeadIsNotAskedFor(t *testing.T) {
	for _, test := range []struct {
		what       string
		properties map[string]string
		wantFeed   bool
	}{
		{"left-hand circular, fed", map[string]string{"POLARIZATION": "L", "VOLTAGE": "15"}, true},
		{"right-hand circular, unfed", map[string]string{"POLARIZATION": "R", "VOLTAGE": "off"}, false},
		{"a level this board does not have is still a request to feed",
			map[string]string{"VOLTAGE": "11"}, true},
		{"nothing said means no supply", map[string]string{}, false},
		{"the tone explicitly off", map[string]string{"TONE": "off", "VOLTAGE": "15"}, true},
		{"DVBv5 polarization off", map[string]string{"POLARIZATION": "off", "VOLTAGE": "15"}, true},
	} {
		feed, err := SwitchedSupply(test.properties)
		if err != nil {
			t.Errorf("%s: %v", test.what, err)
			continue
		}
		if feed != test.wantFeed {
			t.Errorf("%s: feed = %v, want %v", test.what, feed, test.wantFeed)
		}
	}
}

// The distinction the refusal above rests on.
func TestLinearPolarizationTellsTheTwoKindsApart(t *testing.T) {
	for _, test := range []struct {
		value         string
		linear, known bool
	}{
		{"H", true, true}, {"HORIZONTAL", true, true},
		{"V", true, true}, {"VERTICAL", true, true},
		{"L", false, true}, {"LEFT", false, true}, {"CIRCULAR_LEFT", false, true},
		{"R", false, true}, {"RIGHT", false, true}, {"CIRCULAR_RIGHT", false, true},
		{"", false, false}, {"off", false, false}, {"nonsense", false, false},
	} {
		linear, known := LinearPolarization(map[string]string{"POLARIZATION": test.value})
		if linear != test.linear || known != test.known {
			t.Errorf("LinearPolarization(%q) = %v, %v; want %v, %v",
				test.value, linear, known, test.linear, test.known)
		}
	}
}
