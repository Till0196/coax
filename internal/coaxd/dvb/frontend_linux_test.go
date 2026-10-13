// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
	"unsafe"
)

func TestLinuxDVBABILayout(t *testing.T) {
	if got := unsafe.Sizeof(dtvProperty{}); got != 76 {
		t.Fatalf("dtvProperty size = %d, want 76", got)
	}
	if got := unsafe.Sizeof(dmxPESFilter{}); got != 20 {
		t.Fatalf("dmxPESFilter size = %d, want 20", got)
	}
	pointerSize := unsafe.Sizeof(uintptr(0))
	wantPropertiesSize := uintptr(8)
	if pointerSize == 8 {
		wantPropertiesSize = 16
	}
	if got := unsafe.Sizeof(dtvProperties{}); got != wantPropertiesSize {
		t.Fatalf("dtvProperties size = %d, want %d", got, wantPropertiesSize)
	}
	if pointerSize == 8 {
		if got := ioctlRequest(iocWrite, dvbIoctlType, feSetPropertyNR, unsafe.Sizeof(dtvProperties{})); got != 0x40106f52 {
			t.Fatalf("FE_SET_PROPERTY request = 0x%x, want 0x40106f52", got)
		}
		if got := ioctlRequest(iocWrite, dvbIoctlType, dmxSetPESNR, unsafe.Sizeof(dmxPESFilter{})); got != 0x40146f2c {
			t.Fatalf("DMX_SET_PES_FILTER request = 0x%x, want 0x40146f2c", got)
		}
	}
}

func TestBuildTuneProperties(t *testing.T) {
	properties, delivery, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBC/ANNEX_B",
		"FREQUENCY":       "73750000",
		"SYMBOL_RATE":     "5600000",
		"MODULATION":      "QAM/256",
	})
	if err != nil {
		t.Fatal(err)
	}
	if delivery != deliverySystemValues["DVBC/ANNEX_B"] {
		t.Fatalf("unexpected delivery system: %d", delivery)
	}
	if len(properties) != 4 {
		t.Fatalf("got %d properties, want 4", len(properties))
	}
	if properties[0].Command != dtvDeliverySystem || properties[1].Command != dtvFrequency {
		t.Fatalf("unexpected property order: %#v", properties)
	}
}

// TestFrontendInfoLayout pins struct dvb_frontend_info, which the frequency
// range is read out of. A field that drifts would silently read the wrong
// numbers and reject channels the tuner can in fact reach.
func TestFrontendInfoLayout(t *testing.T) {
	var info frontendInfo
	fields := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"sizeof", unsafe.Sizeof(info), 168},
		{"type", unsafe.Offsetof(info.Type), 128},
		{"frequency_min", unsafe.Offsetof(info.FrequencyMin), 132},
		{"frequency_max", unsafe.Offsetof(info.FrequencyMax), 136},
		{"caps", unsafe.Offsetof(info.Caps), 164},
	}
	for _, field := range fields {
		if field.got != field.want {
			t.Errorf("dvb_frontend_info %s = %d, want %d", field.name, field.got, field.want)
		}
	}
	// FE_GET_INFO is _IOR('o', 61, struct dvb_frontend_info).
	if got := ioctlRequest(iocRead, dvbIoctlType, feGetInfoNR, unsafe.Sizeof(info)); got != 0x80a86f3d {
		t.Errorf("FE_GET_INFO = %#08x, want 0x80a86f3d", got)
	}
}

// TestISDBCTunesAsJ83AnnexC pins the one delivery system whose obvious mapping
// is wrong.
//
// The API's enum has SYS_ISDBC, but no frontend driver in the kernel tree
// declares it, and the core groups it with SYS_UNDEFINED. Japanese cable is
// J.83 Annex C, which real demodulators do declare, so that is what an ISDB-C
// channel has to be tuned as.
func TestISDBCTunesAsJ83AnnexC(t *testing.T) {
	const (
		annexC   = 18
		sysISDBC = 10
	)
	for _, spelling := range []string{"ISDBC", "ISDB-C"} {
		_, delivery, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": spelling,
			"FREQUENCY":       "93000000",
			"SYMBOL_RATE":     "5274000",
		})
		if err != nil {
			t.Errorf("BuildTuneProperties(%q): %v", spelling, err)
			continue
		}
		switch delivery {
		case annexC:
		case sysISDBC:
			t.Errorf("%q maps to SYS_ISDBC, which no driver implements", spelling)
		default:
			t.Errorf("%q maps to delivery system %d, want %d (SYS_DVBC_ANNEX_C)",
				spelling, delivery, annexC)
		}
	}
	// The explicit Annex C spellings must land in the same place, so a channel
	// file works whichever name it uses.
	for _, spelling := range []string{"DVBC/ANNEX_C", "DVB-C/ANNEX_C"} {
		_, delivery, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": spelling,
			"FREQUENCY":       "93000000",
			"SYMBOL_RATE":     "5274000",
		})
		if err != nil {
			t.Errorf("BuildTuneProperties(%q): %v", spelling, err)
			continue
		}
		if delivery != annexC {
			t.Errorf("%q maps to %d, want %d", spelling, delivery, annexC)
		}
	}
}

// TestIoctlRetriesAnInterruptedCall aims an ioctl at a pipe while the runtime
// is preempting the goroutine: what comes back must be the kernel's refusal of
// that ioctl (ENOTTY or EINVAL), never EINTR.
//
// The Go runtime preempts goroutines with SIGURG, so a raw ioctl can come back
// EINTR at any time. On real hardware this failed with "DMX_SET_PES_FILTER:
// interrupted system call".
func TestIoctlRetriesAnInterruptedCall(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	defer writer.Close()

	stop := make(chan struct{})
	defer close(stop)
	// Keep the runtime preempting this goroutine while it makes the calls.
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				runtime.Gosched()
			}
		}
	}()

	var value uint32
	request := ioctlRequest(iocRead, dvbIoctlType, feGetInfoNR, unsafe.Sizeof(value))
	for i := 0; i < 2000; i++ {
		err := ioctlPointer(reader, request, unsafe.Pointer(&value))
		if errors.Is(err, syscall.EINTR) {
			t.Fatalf("an interrupted ioctl was reported instead of being retried (call %d)", i)
		}
		if !errors.Is(err, syscall.ENOTTY) && !errors.Is(err, syscall.EINVAL) {
			t.Fatalf("call %d failed with something unexpected: %v", i, err)
		}
	}
}

// The supply setting reaches the frontend as one of the three SEC_VOLTAGE_*
// values, whichever way it was written. "15" is not one of them, so a reader
// passing an unrecognised number through would ask the driver for voltage
// number fifteen.
func TestVoltageBecomesOneOfTheThreeValuesTheAPIHas(t *testing.T) {
	for _, one := range []struct {
		written string
		want    uint32
	}{
		{"13", 0}, {"low", 0}, {"11", 0},
		{"18", 1}, {"high", 1}, {"15", 1},
		{"off", 2}, {"0", 2},
	} {
		properties, _, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": "DVBS2",
			"FREQUENCY":       "1049480",
			"VOLTAGE":         one.written,
		})
		if err != nil {
			t.Fatalf("VOLTAGE=%q: %v", one.written, err)
		}
		found := false
		for _, property := range properties {
			if property.Command != dtvVoltage {
				continue
			}
			found = true
			if property.Value != one.want {
				t.Errorf("VOLTAGE=%q became %d, want %d", one.written, property.Value, one.want)
			}
		}
		if !found {
			t.Errorf("VOLTAGE=%q sent no voltage at all", one.written)
		}
	}
}

// A voltage no tuner has is refused rather than handed to the driver.
func TestVoltageRefusesAValueTheAPICannotCarry(t *testing.T) {
	if _, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "VOLTAGE": "24",
	}); err == nil {
		t.Error("VOLTAGE=24 should have been refused")
	}
}

// Asking for no supply is not overruled by the channel's own polarization,
// which lands on the same property and is written after it.
func TestNoSupplyWinsOverThePolarization(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2",
		"FREQUENCY":       "1049480",
		"VOLTAGE":         "off",
		"POLARIZATION":    "H",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	for _, property := range properties {
		if property.Command == dtvVoltage && property.Value != 2 {
			t.Errorf("the frontend was told voltage %d, want the supply off", property.Value)
		}
	}
}

// With no such request the polarization still decides, as it always did.
func TestThePolarizationStillDecidesTheVoltage(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "POLARIZATION": "H",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	found := false
	for _, property := range properties {
		if property.Command == dtvVoltage {
			found = true
			if property.Value != 1 {
				t.Errorf("horizontal became voltage %d, want 1", property.Value)
			}
		}
	}
	if !found {
		t.Error("the polarization sent no voltage")
	}
}

// A DVBv5 channel file may carry any of four polarizations as whole words, and
// other tools write the single letters; both spellings are read here. The
// circular pair is what a C-band feed and a Japanese satellite feed carry, and
// refusing them made a valid file unreadable.
func TestPolarizationTakesEveryValueADVBv5FileMayCarry(t *testing.T) {
	for _, one := range []struct {
		written string
		want    uint32
	}{
		{"HORIZONTAL", 1}, {"H", 1}, {"LEFT", 1}, {"L", 1},
		{"VERTICAL", 0}, {"V", 0}, {"RIGHT", 0}, {"R", 0},
	} {
		properties, _, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": "DVBS2",
			"FREQUENCY":       "1049480",
			"POLARIZATION":    one.written,
		})
		if err != nil {
			t.Fatalf("POLARIZATION=%q: %v", one.written, err)
		}
		found := false
		for _, property := range properties {
			if property.Command == dtvVoltage {
				found = true
				if property.Value != one.want {
					t.Errorf("POLARIZATION=%q became voltage %d, want %d",
						one.written, property.Value, one.want)
				}
			}
		}
		if !found {
			t.Errorf("POLARIZATION=%q sent no voltage", one.written)
		}
	}
}

// "off" is a DVBv5 file saying it has no polarization to give. It is not a
// request for a level, so neither level is chosen -- and a satellite channel
// that chose neither said nothing about the lead, which gets the supply cut
// rather than left where it was.
func TestPolarizationOffChoosesNoLevelAndCutsTheSupply(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "POLARIZATION": "OFF",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	found := 0
	for _, property := range properties {
		if property.Command != dtvVoltage {
			continue
		}
		found++
		if property.Value != secVoltageOff {
			t.Errorf("a polarization of off sent voltage %d, wanted the supply cut", property.Value)
		}
	}
	if found != 1 {
		t.Errorf("the lead was set %d times, wanted once", found)
	}
}

// A satellite channel that says nothing at all gets the same treatment, and a
// terrestrial one is not given a lead setting it has no lead for.
func TestTheSupplyIsCutForASatelliteChannelThatSaidNothing(t *testing.T) {
	satellite, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "ISDBS", "FREQUENCY": "1049480",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	cut := false
	for _, property := range satellite {
		if property.Command == dtvVoltage && property.Value == secVoltageOff {
			cut = true
		}
	}
	if !cut {
		t.Error("a satellite channel that said nothing left the supply where it was")
	}

	terrestrial, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "ISDBT", "FREQUENCY": "473142857",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	for _, property := range terrestrial {
		if property.Command == dtvVoltage {
			t.Error("a terrestrial channel was given a low-noise block supply setting")
		}
	}
}

// A channel that did say gets what it said, and gets it once.
func TestAChannelThatNamedAVoltageIsNotOverruledByTheDefault(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "DVBS2", "FREQUENCY": "1049480", "POLARIZATION": "H",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	var values []uint32
	for _, property := range properties {
		if property.Command == dtvVoltage {
			values = append(values, property.Value)
		}
	}
	if len(values) != 1 || values[0] != 1 {
		t.Errorf("horizontal sent voltages %v, wanted the higher level once", values)
	}
}

// TestTheAmplifierBecomesDTVLNA checks the one backend where the setting is not
// this program's to apply: the kernel has a property, so the ends of the range
// are handed straight over. LNA_AUTO is ~0U rather than a small number, which
// is what is pinned here.
func TestTheAmplifierBecomesDTVLNA(t *testing.T) {
	for _, key := range []string{"LNA", "GAIN"} {
		testDTVLNA(t, key)
	}
}

func testDTVLNA(t *testing.T, key string) {
	t.Helper()
	for written, want := range map[string]uint32{
		"off":  0,
		"min":  0,
		"on":   1,
		"max":  1,
		"auto": ^uint32(0),
	} {
		properties, _, err := BuildTuneProperties(map[string]string{
			"DELIVERY_SYSTEM": "ISDBT",
			"FREQUENCY":       "473142857",
			key:               written,
		})
		if err != nil {
			t.Errorf("%s=%s: %v", key, written, err)
			continue
		}
		found := false
		for _, property := range properties {
			if property.Command != dtvLNA {
				continue
			}
			found = true
			if property.Value != want {
				t.Errorf("%s=%s became DTV_LNA %#x, want %#x", key, written, property.Value, want)
			}
		}
		if !found {
			t.Errorf("%s=%s sent no DTV_LNA property", key, written)
		}
	}
}

func TestAnEmptyAmplifierSettingSendsNothing(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"FREQUENCY":       "473142857",
		"LNA":             "",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	for _, property := range properties {
		if property.Command == dtvLNA {
			t.Fatalf("an empty LNA sent DTV_LNA %#x", property.Value)
		}
	}
}

// TestALevelIsRefusedByTheKernelsProperty is the one place the shared
// vocabulary is wider than the hardware: DTV_LNA is off, on or auto, and there
// is no way to hand a driver a number of decibels.
func TestALevelIsRefusedByTheKernelsProperty(t *testing.T) {
	if _, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"FREQUENCY":       "473142857",
		"GAIN":            "+13dB",
	}); err == nil {
		t.Error("GAIN=+13dB was accepted by the Linux backend")
	}
}

// TestTheTwoSpellingsAreNotSentTwice guards the seam where both keys reach one
// kernel property: a channel carrying only one of them must still send exactly
// one DTV_LNA.
func TestTheTwoSpellingsAreNotSentTwice(t *testing.T) {
	properties, _, err := BuildTuneProperties(map[string]string{
		"DELIVERY_SYSTEM": "ISDBT",
		"FREQUENCY":       "473142857",
		"GAIN":            "max",
	})
	if err != nil {
		t.Fatalf("BuildTuneProperties: %v", err)
	}
	seen := 0
	for _, property := range properties {
		if property.Command == dtvLNA {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("DTV_LNA was sent %d times, want once", seen)
	}
}

func TestDTVLNAIsTheKernelsNumber(t *testing.T) {
	if dtvLNA != 61 {
		t.Errorf("DTV_LNA = %d, want 61", dtvLNA)
	}
}
