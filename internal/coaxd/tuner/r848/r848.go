// Copyright (c) 2015 Luis Alves; Rafael Micro; TurboSight; 2026 Till0196
// SPDX-License-Identifier: GPL-2.0-or-later
//
// Ported from TBS linux_media (r848.c).
//
// Reads always start at register 0 and come back bit-reversed, so the driver
// shadows every register it writes rather than reading state back. Image
// rejection, crystal drive level, tracking-filter type and channel-filter code
// are all measured on the chip, so Initialize is slow and only runs once. Two
// bugs this port corrects are documented at their call sites; bugs_test.go pins
// both.
package r848

import (
	"fmt"

	"github.com/Till0196/coax/internal/coaxd/i2c"
)

// xtalKHz is the crystal as the register arithmetic wants it. Config.XtalHz
// exists separately because crystalCheck compares against it in hertz.
const xtalKHz = 16000

// tfTypeCount is R848_TF_SIZE, and tfBandSteps is the shared length of every
// R848_TF_Freq_*/R848_TF_Result_* row.
const (
	tfTypeCount   = 9
	tfBandSteps   = 8
	imrMemCount   = 5
	imrIFkHz      = 5300
	imrTrial      = 9
	ringPowerLow  = 115000
	ringPowerHigh = 450000
)

// Tracking-filter types, R848_TF_Type.
const (
	tfNarrow byte = iota
	tfBead
	tfNarrowLin
	tfNarrowATVLin
	tfBeadLin
	tfNarrowATSC
	tfBeadLinATSC
	tf82nBead
	tf82n270n
)

// Ultra-low-band inductor types, R848_UL_TF_Type.
const (
	ulUsingBead byte = iota
	ulUsing270nH
)

// Crystal drive levels, in the order R848_Xtal_Pwr counts them.
const (
	xtalSmallLowest byte = iota
	xtalSmallLow
	xtalSmallHigh
	xtalSmallHighest
	xtalLargeHighest
	xtalCheckSize
)

// standard is R848_Standard_Type. The whole list is ported, not just what the
// TBS5520 reaches, because Sys_Sel and SysFreq_Sel are shared tables and
// calibration selects entries (ATSC, stdSize) no channel ever tunes to.
type standard byte

const (
	stdDvbT6m standard = iota
	stdDvbT7m
	stdDvbT8m
	stdDvbT26m
	stdDvbT27m
	stdDvbT28m
	stdDvbT217m
	stdDvbT210m
	stdDvbC8m
	stdDvbC6m
	stdJ83b
	stdIsdbT
	stdIsdbT4570
	stdDtmb4570
	stdDtmb6000
	stdDtmb6mBwIf5m
	stdDtmb6mBwIf4500
	stdAtsc
	stdDvbS
	stdDvbT6mIf5m
	stdDvbT7mIf5m
	stdDvbT8mIf5m
	stdDvbT26mIf5m
	stdDvbT27mIf5m
	stdDvbT28mIf5m
	stdDvbT217mIf5m
	stdDvbC8mIf5m
	stdDvbC6mIf5m
	stdJ83bIf5m
	stdIsdbTIf5m
	stdDtmbIf5m
	stdAtscIf5m
	stdFm
	// stdSize doubles as the "calibration, not a real standard" marker, which
	// the PLL and mux setup branch on.
	stdSize
)

// sysInfo is R848_Sys_Info_Type: per-standard filter and IF settings.
type sysInfo struct {
	ifKHz         uint32
	filtCalIF     uint32
	bw            byte
	v17M          byte
	hpfCor        byte
	filtExtEna    byte
	filtExtWidest byte
	filtExtPoint  byte
	filtComp      byte
	filtCur       byte
	filt3dB       byte
	swbufCur      byte
	tfCur         byte
	swcapClk      byte
	naPwrDet      byte
}

// sysFreqInfo is R848_SysFreq_Info_Type: per-standard, per-frequency AGC
// thresholds and discharge settings.
type sysFreqInfo struct {
	lnaTop           byte
	lnaVthL          byte
	mixerTop         byte
	mixerVthL        byte
	rfTop            byte
	nrbTop           byte
	nrbBW            byte
	bypLPF           byte
	rfFastDischarge  byte
	rfSlowDischarge  byte
	rfpdPulseEnable  byte
	lnaFastDischarge byte
	lnaSlowDischarge byte
	lnapdPulseEnable byte
	agcClk           byte
}

// freqInfo is R848_Freq_Info_Type: the per-frequency mux settings.
type freqInfo struct {
	lnaBand  byte
	imrMem   byte
	rfPoly   byte
	lpfCap   byte
	lpfNotch byte
}

// imrPoint is r848_sect_type: one image-rejection calibration result.
type imrPoint struct {
	gainX  byte
	phaseY byte
	iqcap  byte
	value  byte
}

// filterCal caches a standard's channel-filter code, so a retune within the
// same standard skips the slow calibration.
type filterCal struct {
	measured bool
	code     byte
	bw       byte
}

// Config describes how the board wired the tuner.
type Config struct {
	// Address is the tuner's 8-bit I2C slave address.
	Address byte
	// XtalHz is the reference crystal frequency. Only crystalCheck reads it;
	// the PLL uses the compile-time xtalKHz constant.
	XtalHz uint32
	// SetTfType is the tracking-filter table in use. Initialize overwrites it
	// from the check's result, so a board only needs a sane starting value.
	SetTfType byte
	// DetectTfType and XtalPower are measured by Initialize; board code does
	// not set them.
	DetectTfType byte
	XtalPower    byte
}

// Tuner is an R848 reached over an I2C bus.
type Tuner struct {
	bus    i2c.Bus
	config Config

	registers [registerCount]byte
	imr       [imrMemCount]imrPoint
	filter    [stdSize + 1]filterCal

	standard    standard
	sys         sysInfo
	frequency   uint32 // kHz, terrestrial and satellite alike
	bandwidth   uint32 // kHz, satellite only
	initialized bool
}

// New addresses an R848 on the given bus.
func New(bus i2c.Bus, config Config) *Tuner {
	if config.XtalHz == 0 {
		config.XtalHz = xtalKHz * 1000
	}
	return &Tuner{bus: bus, config: config, standard: stdSize}
}

// read fetches length registers from register 0, undoing the bit reversal.
func (tuner *Tuner) read(length int) ([]byte, error) {
	data, err := tuner.bus.Read(tuner.config.Address, length)
	if err != nil {
		return nil, fmt.Errorf("R848 read: %w", err)
	}
	for i := range data {
		data[i] = reverseBits(data[i])
	}
	return data, nil
}

func reverseBits(value byte) byte {
	value = value&0x55<<1 | value&0xaa>>1
	value = value&0x33<<2 | value&0xcc>>2
	return value&0x0f<<4 | value&0xf0>>4
}

// write does not update the shadow copy; callers assign to tuner.registers.
func (tuner *Tuner) write(register, value byte) error {
	if err := tuner.bus.Write(tuner.config.Address, []byte{register, value}); err != nil {
		return fmt.Errorf("R848 write %#02x: %w", register, err)
	}
	return nil
}

func (tuner *Tuner) writeMulti(register byte, values []byte) error {
	if err := tuner.bus.Write(tuner.config.Address, append([]byte{register}, values...)); err != nil {
		return fmt.Errorf("R848 write %#02x: %w", register, err)
	}
	return nil
}

// shadow indexes the shadow register file by chip register address. The file
// starts at 0x08, which is where init_regs writes it.
func (tuner *Tuner) shadow(register byte) *byte { return &tuner.registers[register-0x08] }

func (tuner *Tuner) setBits(register, keepMask, value byte) error {
	cell := tuner.shadow(register)
	*cell = *cell&keepMask | value
	return tuner.write(register, *cell)
}

// initRegisters reloads the power-on register image; satellite uses a different
// image from terrestrial/cable.
func (tuner *Tuner) initRegisters(system standard) error {
	image := initRegistersHybrid
	if system == stdDvbS {
		image = initRegistersDVBS
	}
	tuner.registers = image
	return tuner.writeMulti(0x08, tuner.registers[:])
}

// PLLLocked reports whether the tuner's PLL has locked.
func (tuner *Tuner) PLLLocked() (bool, error) {
	data, err := tuner.read(3)
	if err != nil {
		return false, err
	}
	return data[2]&0x40 != 0, nil
}

// readIMR averages four readings of the image-rejection ADC, the value every
// calibration loop minimises.
func (tuner *Tuner) readIMR() (byte, error) {
	var total uint16
	for range 4 {
		data, err := tuner.read(2)
		if err != nil {
			return 0, err
		}
		total += uint16(data[1] & 0x3f)
	}
	return byte(total >> 2), nil
}

// Initialize runs the one-time self-calibration: tracking-filter inductor check,
// image rejection at five reference frequencies, crystal drive sweep. It takes
// seconds, and is skipped on later calls.
func (tuner *Tuner) Initialize() error {
	if tuner.initialized {
		return nil
	}
	for i := range tuner.filter {
		tuner.filter[i] = filterCal{}
	}

	if err := tuner.initRegisters(stdSize); err != nil {
		return err
	}
	if err := tuner.trackingFilterCheck(); err != nil {
		return fmt.Errorf("R848 tracking filter check: %w", err)
	}

	// Image rejection: nodes 3 and 4 are measured from scratch ("full K"); 0, 1
	// and 2 refine from node 3's result.
	if err := tuner.initRegisters(stdSize); err != nil {
		return err
	}
	if err := tuner.calibrationPrepare(calIMR); err != nil {
		return err
	}
	for _, step := range []struct {
		slot   byte
		refine bool
	}{{3, false}, {0, true}, {1, true}, {2, true}, {4, false}} {
		if err := tuner.imageRejection(step.slot, step.refine); err != nil {
			return fmt.Errorf("R848 image rejection at node %d: %w", step.slot, err)
		}
	}

	// Crystal drive: three sweeps, keeping the highest level any needed.
	if err := tuner.initRegisters(stdSize); err != nil {
		return err
	}
	tuner.config.XtalPower = xtalSmallLowest
	for range 3 {
		measured, err := tuner.crystalCheck()
		if err != nil {
			return fmt.Errorf("R848 crystal check: %w", err)
		}
		if measured > tuner.config.XtalPower {
			tuner.config.XtalPower = measured
		}
	}

	if err := tuner.initRegisters(stdSize); err != nil {
		return err
	}
	tuner.standard = stdSize
	tuner.initialized = true
	return nil
}
