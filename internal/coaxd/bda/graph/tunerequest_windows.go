// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
	"github.com/Till0196/coax/pkg/win32/directshow/tv"

	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
)

// tuningSpaceAndLocatorFor names the tuning space and locator classes a
// delivery system is built from.
func tuningSpaceAndLocatorFor(profile tuning.TuneProfile) (tuningSpaceCLSID, locatorCLSID win32.GUID) {
	tuningSpaceCLSID = clsidDVBTuningSpace
	locatorCLSID = clsidDVBTLocator
	if profile.Delivery == tuning.DeliverySatellite {
		tuningSpaceCLSID = clsidDVBSTuningSpace
		locatorCLSID = clsidDVBSLocator
		if profile.System == "ISDBS" || profile.System == "ISDBS3" {
			locatorCLSID = clsidISDBSLocator
		}
	} else if profile.Delivery == tuning.DeliveryCable {
		locatorCLSID = clsidDVBCLocator
	}
	return tuningSpaceCLSID, locatorCLSID
}

// primeTuningSpace builds the tuning space and hands the provider a locator-less
// tune request, before the tuner filter is connected to it. This order is
// mandatory: the Microsoft Network Provider validates a tune request against a
// tuning space it already has, so the real one cold answers E_INVALIDARG. The
// blank request establishes the tuning space and network type.
func primeTuningSpace(tuner *tv.ITuner, profile tuning.TuneProfile, networkType win32.GUID) (*tv.ITuningSpace, error) {
	tuningSpaceCLSID, _ := tuningSpaceAndLocatorFor(profile)
	space, err := createTuningSpace(tuningSpaceCLSID)
	if err != nil {
		return nil, fmt.Errorf("create tuning space: %w", err)
	}
	if err := space.Put__NetworkType(&networkType); err != nil {
		space.Release()
		return nil, fmt.Errorf("set the network type: %w", err)
	}
	if err := setDVBSystemType(space, profile); err != nil {
		space.Release()
		return nil, fmt.Errorf("set the DVB system type: %w", err)
	}
	name := win32.SysAllocString("coax " + profile.System)
	defer win32.SysFreeString(name)
	if err := space.Put_UniqueName(name); err != nil {
		space.Release()
		return nil, fmt.Errorf("name the tuning space: %w", err)
	}
	if err := space.Put_FriendlyName(name); err != nil {
		space.Release()
		return nil, fmt.Errorf("name the tuning space: %w", err)
	}

	blank, err := space.CreateTuneRequest()
	if err != nil {
		space.Release()
		return nil, fmt.Errorf("create the priming tune request: %w", err)
	}
	defer blank.Release()
	// Every triplet field left unset (-1) rather than zero, which is a valid ONID.
	_ = setDVBTriplet(blank, -1)

	if err := tuner.Put_TuningSpace(space); err != nil {
		space.Release()
		return nil, fmt.Errorf("hand the tuning space to the provider: %w", err)
	}
	if err := tuner.Put_TuneRequest(blank); err != nil {
		space.Release()
		return nil, fmt.Errorf("prime the tune request: %w", err)
	}
	return space, nil
}

// sendDiseqc puts the channel's message on the lead through the node the driver
// publishes for it. Only the message is handed over: the voltage and tone are
// the driver's to apply from the block and polarisation given through the
// tuning space, and BDA offers no way to work the lead directly.
//
// The tone burst is turned off: a burst addresses pre-DiSEqC two-lead switches,
// and asking for both on a driver that sends them in one exchange makes a
// switch hear two instructions.
func sendDiseqc(tuner *directshow.IBaseFilter, profile tuning.TuneProfile) error {
	command := findDiseqCommand(tuner)
	if command == nil {
		return fmt.Errorf("this driver publishes no DiSEqC node, so %s cannot be honoured on it",
			tuning.SatNumberPropertyName)
	}
	defer command.Release()

	if err := command.Put_EnableDiseqCommands(1); err != nil {
		return fmt.Errorf("enable DiSEqC: %w", err)
	}
	// Best effort, for the reason above.
	_ = command.Put_DiseqUseToneBurst(0)
	_ = command.Put_DiseqRepeats(0)

	message := profile.Diseqc.Message[:profile.Diseqc.Length]
	if err := command.Put_DiseqSendCommand(0, message); err != nil {
		return fmt.Errorf("send the DiSEqC command: %w", err)
	}
	// The call above only queued the message and woke the sending thread, with no
	// completion to wait on, so wait out the airtime: a DiSEqC byte is 22 bits of
	// 22 kHz keying, about 13.5 ms, plus the standard's settling gap.
	time.Sleep(diseqcAirtime(len(message)))
	return nil
}

// diseqcAirtime is how long a message of this many bytes takes on the lead,
// with the standard's settling gap after it.
func diseqcAirtime(bytes int) time.Duration {
	const perByte = 14 * time.Millisecond
	const settle = 50 * time.Millisecond
	return time.Duration(bytes)*perByte + settle
}

// applyTuneProfile hands the provider the real channel.
func applyTuneProfile(tuner *tv.ITuner, space *tv.ITuningSpace, profile tuning.TuneProfile) error {
	_, locatorCLSID := tuningSpaceAndLocatorFor(profile)

	locator, err := createLocator(locatorCLSID)
	if err != nil {
		return fmt.Errorf("create locator: %w", err)
	}
	defer locator.Release()
	if err := locator.Put_CarrierFrequency(profile.FrequencyKHz); err != nil {
		return fmt.Errorf("set the frequency: %w", err)
	}
	if profile.SymbolRate > 0 {
		if err := locator.Put_SymbolRate(profile.SymbolRate); err != nil {
			return fmt.Errorf("set the symbol rate: %w", err)
		}
	}
	if modulation := modulationFor(profile); modulation != directshow.BDA_MOD_NOT_SET {
		if err := locator.Put_Modulation(modulation); err != nil {
			return err
		}
	}
	if err := setDeliverySpecificLocator(locator, profile); err != nil {
		return err
	}
	// Before the locator goes on: the driver reads the space when the request is
	// created, and the frequency above is a downlink that needs these to mean
	// anything.
	if err := setLNBInfo(space, profile); err != nil {
		return err
	}
	if err := space.Put_DefaultLocator(locator); err != nil {
		return fmt.Errorf("give the tuning space its locator: %w", err)
	}

	request, err := space.CreateTuneRequest()
	if err != nil {
		return fmt.Errorf("create the tune request: %w", err)
	}
	defer request.Release()
	if err := request.Put_Locator(locator); err != nil {
		return fmt.Errorf("attach the locator: %w", err)
	}
	if err := setDVBTriplet(request, profile.StreamID); err != nil {
		return fmt.Errorf("set the DVB triplet: %w", err)
	}
	if err := tuner.Put_TuneRequest(request); err != nil {
		return fmt.Errorf("hand the tune request to the provider: %w", err)
	}
	return nil
}

func createTuningSpace(clsid win32.GUID) (*tv.ITuningSpace, error) {
	var unknown *win32.IUnknown
	if err := win32.CoCreateInstance(&clsid, nil, win32.CLSCTX_INPROC_SERVER, &tv.IID_ITuningSpace, &unknown); err != nil {
		return nil, err
	}
	return (*tv.ITuningSpace)(unsafe.Pointer(unknown)), nil
}

func createLocator(clsid win32.GUID) (*tv.ILocator, error) {
	var unknown *win32.IUnknown
	if err := win32.CoCreateInstance(&clsid, nil, win32.CLSCTX_INPROC_SERVER, &tv.IID_ILocator, &unknown); err != nil {
		return nil, err
	}
	return (*tv.ILocator)(unsafe.Pointer(unknown)), nil
}

func setDVBSystemType(space *tv.ITuningSpace, profile tuning.TuneProfile) error {
	var unknown *win32.IUnknown
	if err := space.QueryInterface(&tv.IID_IDVBTuningSpace, &unknown); err != nil {
		return err
	}
	dvbSpace := (*tv.IDVBTuningSpace)(unsafe.Pointer(unknown))
	defer dvbSpace.Release()
	systemType := directshow.DVB_Terrestrial
	switch profile.Delivery {
	case tuning.DeliveryCable:
		systemType = directshow.DVB_Cable
	case tuning.DeliverySatellite:
		systemType = directshow.DVB_Satellite
	}
	if profile.System == "ISDBT" {
		systemType = directshow.ISDB_Terrestrial
	} else if profile.System == "ISDBS" || profile.System == "ISDBS3" {
		systemType = directshow.ISDB_Satellite
	}
	return dvbSpace.Put_SystemType(systemType)
}

// setLNBInfo tells the tuning space which block is on the dish. This is the only
// way to get the 22 kHz tone onto the lead: BDA has no "tone on" property, so
// the driver is given the two oscillators and the band-switch frequency and
// works out the band, and hence the tone, itself. A channel naming no block
// leaves whatever the provider put there.
func setLNBInfo(space *tv.ITuningSpace, profile tuning.TuneProfile) error {
	if profile.Delivery != tuning.DeliverySatellite || !profile.LNB.Known() {
		return nil
	}
	var unknown *win32.IUnknown
	if err := space.QueryInterface(&tv.IID_IDVBSTuningSpace, &unknown); err != nil {
		return nil
	}
	satellite := (*tv.IDVBSTuningSpace)(unsafe.Pointer(unknown))
	defer satellite.Release()
	if err := satellite.Put_LowOscillator(profile.LNB.LowOscillatorKHz); err != nil {
		return fmt.Errorf("set the LNB's low oscillator: %w", err)
	}
	if err := satellite.Put_HighOscillator(profile.LNB.HighOscillatorKHz); err != nil {
		return fmt.Errorf("set the LNB's high oscillator: %w", err)
	}
	if err := satellite.Put_LNBSwitch(profile.LNB.SwitchKHz); err != nil {
		return fmt.Errorf("set the LNB's switch frequency: %w", err)
	}
	return nil
}

func setDeliverySpecificLocator(locator *tv.ILocator, profile tuning.TuneProfile) error {
	if profile.Delivery == tuning.DeliveryTerrestrial && profile.BandwidthMHz > 0 {
		var unknown *win32.IUnknown
		if err := locator.QueryInterface(&tv.IID_IDVBTLocator, &unknown); err != nil {
			return err
		}
		terrestrial := (*tv.IDVBTLocator)(unsafe.Pointer(unknown))
		defer terrestrial.Release()
		return terrestrial.Put_Bandwidth(profile.BandwidthMHz)
	}
	if profile.Delivery == tuning.DeliverySatellite && profile.Polarization != "" && profile.Polarization != "AUTO" && profile.Polarization != "OFF" {
		var unknown *win32.IUnknown
		if err := locator.QueryInterface(&tv.IID_IDVBSLocator, &unknown); err != nil {
			return err
		}
		satellite := (*tv.IDVBSLocator)(unsafe.Pointer(unknown))
		defer satellite.Release()
		polarization, err := polarizationFor(profile.Polarization)
		if err != nil {
			return err
		}
		return satellite.Put_SignalPolarisation(polarization)
	}
	return nil
}

func setDVBTriplet(request *tv.ITuneRequest, streamID int32) error {
	var unknown *win32.IUnknown
	if err := request.QueryInterface(&tv.IID_IDVBTuneRequest, &unknown); err != nil {
		return err
	}
	dvbRequest := (*tv.IDVBTuneRequest)(unsafe.Pointer(unknown))
	defer dvbRequest.Release()
	if err := dvbRequest.Put_ONID(-1); err != nil {
		return err
	}
	if err := dvbRequest.Put_TSID(streamID); err != nil {
		return err
	}
	return dvbRequest.Put_SID(-1)
}

func modulationFor(profile tuning.TuneProfile) directshow.ModulationType {
	modulation := profile.Modulation
	if modulation == "" || modulation == "AUTO" {
		if profile.System == "ISDBT" {
			return directshow.BDA_MOD_ISDB_T_TMCC
		}
		if profile.System == "ISDBS" || profile.System == "ISDBS3" {
			return directshow.BDA_MOD_ISDB_S_TMCC
		}
		return directshow.BDA_MOD_NOT_SET
	}
	switch modulation {
	case "QPSK":
		return directshow.BDA_MOD_QPSK
	case "PSK8", "8PSK":
		return directshow.BDA_MOD_8PSK
	case "QAM16", "16QAM":
		return directshow.BDA_MOD_16QAM
	case "QAM32", "32QAM":
		return directshow.BDA_MOD_32QAM
	case "QAM64", "64QAM":
		return directshow.BDA_MOD_64QAM
	case "QAM128", "128QAM":
		return directshow.BDA_MOD_128QAM
	case "QAM256", "256QAM":
		return directshow.BDA_MOD_256QAM
	default:
		return directshow.BDA_MOD_NOT_SET
	}
}

func polarizationFor(value string) (directshow.Polarisation, error) {
	switch value {
	case "H", "HORIZONTAL":
		return directshow.BDA_POLARISATION_LINEAR_H, nil
	case "V", "VERTICAL":
		return directshow.BDA_POLARISATION_LINEAR_V, nil
	case "L", "LEFT", "CIRCULARLEFT":
		return directshow.BDA_POLARISATION_CIRCULAR_L, nil
	case "R", "RIGHT", "CIRCULARRIGHT":
		return directshow.BDA_POLARISATION_CIRCULAR_R, nil
	default:
		return 0, fmt.Errorf("unsupported BDA polarization %q", value)
	}
}

// networkTypeFor is the network type a tuning space carries for a delivery
// system. BDA uses the old per-network provider CLSIDs as network type
// identifiers, which is why these are the same values.
func networkTypeFor(delivery tuning.Delivery) win32.GUID {
	switch delivery {
	case tuning.DeliveryCable:
		return clsidDVBCNetworkProvider
	case tuning.DeliverySatellite:
		return clsidDVBSNetworkProvider
	default:
		return clsidDVBTNetworkProvider
	}
}
