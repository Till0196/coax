// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package graph

import (
	"fmt"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/bda/filters"
	"github.com/Till0196/coax/internal/coaxd/bda/tuning"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors"
	"github.com/Till0196/coax/internal/coaxd/bda/vendors/dd"
	"github.com/Till0196/coax/pkg/win32"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// ddNetworkProfile connects the published filter's network. Its acquisition
// standard and channel are set on the nodes after the graph starts.
func ddNetworkProfile(tuner filters.Moniker, profile tuning.TuneProfile) tuning.TuneProfile {
	if !dd.Is(tuner.Vendor()) {
		return profile
	}
	mode, known := dd.StandardOf(tuner.Vendor())
	if !known {
		return profile
	}
	switch mode {
	case dd.ModeDVBT, dd.ModeDVBT2, dd.ModeDVBC2:
		profile.System, profile.Delivery = "DVBT", tuning.DeliveryTerrestrial
	case dd.ModeISDBT:
		profile.System, profile.Delivery = "ISDBT", tuning.DeliveryTerrestrial
	case dd.ModeDVBC, dd.ModeISDBC, dd.ModeJ83B:
		profile.System, profile.Delivery = "DVBC", tuning.DeliveryCable
	case dd.ModeDVBS, dd.ModeDVBS2, dd.ModeDVBS2X:
		profile.System, profile.Delivery = "DVBS2", tuning.DeliverySatellite
	case dd.ModeISDBS:
		profile.System, profile.Delivery = "ISDBS", tuning.DeliverySatellite
	}
	return profile
}

func applyDDTuneProfile(filter *directshow.IBaseFilter, profile tuning.TuneProfile) error {
	mode, known := dd.ModeFor(profile.System)
	if !known || mode == dd.ModeISDBS3 {
		return fmt.Errorf("DD acquisition standard %q is unsupported", profile.System)
	}
	// Japanese cable multiplexes are captured whole; selection is performed
	// by the stream parser rather than the driver's TSMF selector.
	if mode == dd.ModeISDBC {
		mode = dd.ModeDVBC
	}
	root, err := vendors.Filter(filter)
	if err != nil {
		return err
	}
	standards, err := dd.ReadStandards(root)
	root.Release()
	if err != nil {
		return err
	}
	if standards.SupportedMask&(1<<uint32(mode)) == 0 {
		return fmt.Errorf("DD tuner does not support %s", dd.ModeName(mode))
	}

	pin := findPin(filter, directshow.PINDIR_OUTPUT)
	if pin == nil {
		return fmt.Errorf("DD transport output pin is absent")
	}
	defer pin.Release()
	output, err := vendors.PinControl(pin)
	if err != nil {
		return err
	}
	defer output.Release()
	var unknown *win32.IUnknown
	if err := filter.QueryInterface(&directshow.IID_IBDA_DeviceControl, &unknown); err != nil {
		return fmt.Errorf("DD change control: %w", err)
	}
	changes := (*directshow.IBDA_DeviceControl)(unsafe.Pointer(unknown))
	defer changes.Release()
	frequencyNode := findNode(filter, directshow.IID_IBDA_FrequencyFilter)
	if frequencyNode == nil {
		return fmt.Errorf("DD frequency node is absent")
	}
	frequency := (*directshow.IBDA_FrequencyFilter)(unsafe.Pointer(frequencyNode))
	defer frequency.Release()
	demod := findDemodulator(filter)
	if demod == nil {
		return fmt.Errorf("DD demodulator node is absent")
	}
	defer demod.Release()

	err = ddTransaction(changes, func() error {
		if err := dd.SelectStandard(output, mode); err != nil {
			return err
		}
		modulation := modulationFor(profile)
		if err := demod.Put_ModulationType(&modulation); err != nil {
			return fmt.Errorf("DD modulation: %w", err)
		}
		inversion := int32(2) // BDA_SPECTRAL_INVERSION_AUTOMATIC.
		if err := demod.Put_SpectralInversion(&inversion); err != nil {
			return fmt.Errorf("DD spectral inversion: %w", err)
		}
		rate := uint32(profile.SymbolRate)
		if err := demod.Put_SymbolRate(&rate); err != nil {
			return fmt.Errorf("DD symbol rate: %w", err)
		}
		if err := frequency.Put_FrequencyMultiplier(1000); err != nil {
			return err
		}
		if err := frequency.Put_Bandwidth(uint32(profile.BandwidthMHz)); err != nil {
			return err
		}
		if profile.Delivery == tuning.DeliverySatellite {
			lnbNode := findNode(filter, directshow.IID_IBDA_LNBInfo)
			if lnbNode == nil {
				return fmt.Errorf("DD LNB node is absent")
			}
			lnb := (*directshow.IBDA_LNBInfo)(unsafe.Pointer(lnbNode))
			defer lnb.Release()
			if err := lnb.Put_LocalOscillatorFrequencyLowBand(uint32(profile.LNB.LowOscillatorKHz)); err != nil {
				return err
			}
			if err := lnb.Put_LocalOscillatorFrequencyHighBand(uint32(profile.LNB.HighOscillatorKHz)); err != nil {
				return err
			}
			if err := lnb.Put_HighLowSwitchFrequency(uint32(profile.LNB.SwitchKHz)); err != nil {
				return err
			}
			pol := directshow.Polarisation(0)
			var err error
			if profile.Polarization != "" && profile.Polarization != "AUTO" && profile.Polarization != "OFF" {
				pol, err = polarizationFor(profile.Polarization)
			}
			if err != nil {
				return err
			}
			if err := frequency.Put_Polarity(pol); err != nil {
				return err
			}
			if profile.DiseqcWanted {
				if err := sendDiseqc(filter, profile); err != nil {
					return err
				}
			}
		}
		if err := frequency.Put_Frequency(uint32(profile.FrequencyKHz)); err != nil {
			return err
		}
		return dd.SelectStream(output, mode, profile.StreamID)
	})
	if err != nil {
		return err
	}
	// The DD driver starts acquisition when lock is queried after committing.
	statistics := findSignalStatistics(filter)
	if statistics == nil {
		return fmt.Errorf("DD signal statistics node is absent")
	}
	defer statistics.Release()
	var locked byte
	return statistics.Get_SignalLocked(&locked)
}

type ddChanges interface {
	StartChanges() error
	CheckChanges() error
	CommitChanges() error
}

func ddTransaction(changes ddChanges, write func() error) (err error) {
	if err = changes.StartChanges(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			// Starting again restores the last committed channel before discarding
			// the failed transaction.
			_ = changes.StartChanges()
			_ = changes.CommitChanges()
		}
	}()
	if err = write(); err != nil {
		return err
	}
	if err = changes.CheckChanges(); err != nil {
		return err
	}
	return changes.CommitChanges()
}
