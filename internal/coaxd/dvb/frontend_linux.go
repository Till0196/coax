// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package dvb

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/tuning"
)

// Linux DVB UAPI constants. These values are part of the stable userspace ABI
// from linux/dvb/frontend.h and linux/dvb/dmx.h.
const (
	statusHasSignal  = uint32(0x01)
	statusHasCarrier = uint32(0x02)
	statusHasViterbi = uint32(0x04)
	statusHasSync    = uint32(0x08)
	statusHasLock    = uint32(0x10)

	dtvTune           = uint32(1)
	dtvClear          = uint32(2)
	dtvFrequency      = uint32(3)
	dtvModulation     = uint32(4)
	dtvBandwidthHz    = uint32(5)
	dtvInversion      = uint32(6)
	dtvSymbolRate     = uint32(8)
	dtvInnerFEC       = uint32(9)
	dtvVoltage        = uint32(10)
	dtvTone           = uint32(11)
	dtvPilot          = uint32(12)
	dtvRolloff        = uint32(13)
	dtvDeliverySystem = uint32(17)
	dtvCodeRateHP     = uint32(36)
	dtvCodeRateLP     = uint32(37)
	dtvGuardInterval  = uint32(38)
	dtvTransmitMode   = uint32(39)
	dtvHierarchy      = uint32(40)
	dtvStreamID       = uint32(42)
	// The scrambling sequence is its own property rather than bits inside the stream
	// identifier, which is how the drivers take it.
	dtvScramblingSequenceIndex = uint32(70)
	dtvEnumDelivery            = uint32(44)
	dtvLNA                     = uint32(61)
	dtvStatSignal              = uint32(62)
	dtvStatCNR                 = uint32(63)

	feGetInfoNR = uintptr(61)
	// The ones that work the lead rather than the demodulator. Ioctls, not
	// properties: the API has no DiSEqC message property, and the order they go
	// out in is the point.
	feDiseqcSendMasterNR = uintptr(63)
	feDiseqcSendBurstNR  = uintptr(65)
	feSetToneNR          = uintptr(66)
	feSetVoltageNR       = uintptr(67)
	feReadStatusNR       = uintptr(69)
	feSetPropertyNR      = uintptr(82)
	feGetPropertyNR      = uintptr(83)
	dmxStopNR            = uintptr(42)
	dmxSetPESNR          = uintptr(44)
	dvbIoctlType         = uintptr('o')

	iocWrite = uintptr(1)
	iocRead  = uintptr(2)

	dmxInputFrontend = int32(0)
	dmxOutputTSTap   = int32(2)
	dmxPESOther      = int32(20)
	dmxImmediate     = uint32(4)

	// lnaAuto is LNA_AUTO, the DVB API's ~0U: the driver decides.
	lnaAuto = ^uint32(0)
)

type TuneProperty struct {
	Command uint32
	Value   uint32
}

// CommandFrequency names the tuning property carrying the frequency.
const CommandFrequency = dtvFrequency

// dtvProperty matches the packed 76-byte struct dtv_property. The union is
// represented as raw bytes because DTV_ENUM_DELSYS uses its buffer member.
type dtvProperty struct {
	Command  uint32
	Reserved [3]uint32
	Union    [56]byte
	Result   int32
}

// value and setValue read and write the union's u.data member, which every
// property here uses apart from DTV_ENUM_DELSYS. Decoded rather than
// reinterpreted: pointing a uint32 at a byte array depends on alignment.
func (property *dtvProperty) value() uint32 {
	return binary.NativeEndian.Uint32(property.Union[:4])
}

func (property *dtvProperty) setValue(value uint32) {
	binary.NativeEndian.PutUint32(property.Union[:4], value)
}

// dtvProperties naturally has the same layout as struct dtv_properties on both
// 32-bit and 64-bit Linux (including alignment before the pointer).
type dtvProperties struct {
	Count      uint32
	Properties *dtvProperty
}

type dmxPESFilter struct {
	PID     uint16
	_       uint16
	Input   int32
	Output  int32
	PESType int32
	Flags   uint32
}

type Frontend struct {
	file *os.File
}

func OpenFrontend(path string) (*Frontend, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return &Frontend{file: f}, nil
}

// OpenFrontendReadOnly opens a frontend only to ask it questions. Only one
// opener may write to a frontend, but readers are not limited, so this does not
// keep a tuner from being opened to tune, and works while one is.
func OpenFrontendReadOnly(path string) (*Frontend, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return &Frontend{file: f}, nil
}

func (f *Frontend) Close() error {
	return f.file.Close()
}

// diseqcMasterCmd matches struct dvb_diseqc_master_cmd: a message of up to six
// bytes, and how many of them are the message.
type diseqcMasterCmd struct {
	Message [6]byte
	Length  byte
}

// The frontend's own words for the lead, from enum fe_sec_voltage,
// fe_sec_tone_mode and fe_sec_mini_cmd. Not the same numbering as the
// like-named properties: the tone matches, the voltage does not.
const (
	secVoltage13  = uintptr(0)
	secVoltage18  = uintptr(1)
	secVoltageOFF = uintptr(2)

	secToneOn  = uintptr(0)
	secToneOff = uintptr(1)

	secMiniA = uintptr(0)
	secMiniB = uintptr(1)
)

// SendDiseqc puts a DiSEqC message on the lead, with the voltage and tone that
// go around it.
//
// The order is the standard's and is not a detail: the switch is powered from
// the lead, so the voltage must be up and settled before the message; the tone
// must be off while the message is sent, the message being that tone keyed; and
// the burst follows for switches that understand only a burst.
func (f *Frontend) SendDiseqc(command tuning.Diseqc) error {
	if err := f.setTone(secToneOff); err != nil {
		return err
	}
	if err := f.setVoltage(secVoltage(command)); err != nil {
		return err
	}
	time.Sleep(diseqcSettle)

	message := diseqcMasterCmd{Message: command.Message, Length: command.Length}
	request := ioctlRequest(iocWrite, dvbIoctlType, feDiseqcSendMasterNR, unsafe.Sizeof(message))
	if err := ioctlOf(f.file, request, &message); err != nil {
		return fmt.Errorf("FE_DISEQC_SEND_MASTER_CMD: %w", err)
	}
	time.Sleep(diseqcSettle)

	burst := secMiniA
	if command.BurstB {
		burst = secMiniB
	}
	if err := ioctlValue(f.file, ioctlRequest(0, dvbIoctlType, feDiseqcSendBurstNR, 0), burst); err != nil {
		return fmt.Errorf("FE_DISEQC_SEND_BURST: %w", err)
	}
	time.Sleep(diseqcSettle)

	tone := secToneOff
	if command.ToneOn {
		tone = secToneOn
	}
	return f.setTone(tone)
}

// secVoltage is the frontend's word for the supply a DiSEqC exchange asks for.
func secVoltage(command tuning.Diseqc) uintptr {
	switch {
	case !command.Supply:
		return secVoltageOFF
	case command.Vertical:
		return secVoltage13
	default:
		return secVoltage18
	}
}

// diseqcSettle is the fifteen milliseconds the standard asks for between the
// steps.
const diseqcSettle = 15 * time.Millisecond

func (f *Frontend) setTone(tone uintptr) error {
	if err := ioctlValue(f.file, ioctlRequest(0, dvbIoctlType, feSetToneNR, 0), tone); err != nil {
		return fmt.Errorf("FE_SET_TONE: %w", err)
	}
	return nil
}

func (f *Frontend) setVoltage(voltage uintptr) error {
	if err := ioctlValue(f.file, ioctlRequest(0, dvbIoctlType, feSetVoltageNR, 0), voltage); err != nil {
		return fmt.Errorf("FE_SET_VOLTAGE: %w", err)
	}
	return nil
}

func (f *Frontend) Tune(properties []TuneProperty) error {
	if len(properties) == 0 {
		return fmt.Errorf("no tuning properties")
	}
	clear := []dtvProperty{newDTVProperty(dtvClear, 0)}
	if err := setFrontendProperties(f.file, clear); err != nil {
		return fmt.Errorf("DTV_CLEAR: %w", err)
	}

	request := make([]dtvProperty, 0, len(properties)+1)
	for _, property := range properties {
		request = append(request, newDTVProperty(property.Command, property.Value))
	}
	request = append(request, newDTVProperty(dtvTune, 0))
	if err := setFrontendProperties(f.file, request); err != nil {
		return fmt.Errorf("FE_SET_PROPERTY: %w", err)
	}
	return nil
}

func (f *Frontend) Status() (uint32, error) {
	var status uint32
	request := ioctlRequest(iocRead, dvbIoctlType, feReadStatusNR, unsafe.Sizeof(status))
	if err := ioctlOf(f.file, request, &status); err != nil {
		return 0, fmt.Errorf("FE_READ_STATUS: %w", err)
	}
	return status, nil
}

// Statistics are the DVBv5 signal measurements a frontend may offer. Each is
// optional: a driver that cannot measure answers with a scale of "not
// available".
type Statistics struct {
	// CNRdB is the carrier-to-noise ratio in dB.
	CNRdB  float64
	HasCNR bool
	// StrengthdBm is the input level in dBm.
	StrengthdBm float64
	HasStrength bool
}

// Scales a statistic can be reported on, from enum fecap_scale_params. Only the
// decibel scale is taken: a relative 0..65535 reading is the driver guessing
// from an AGC level, and dressing that up as dB invents precision.
const (
	scaleNotAvailable = 0
	scaleDecibel      = 1
)

// Statistics reads the frontend's signal measurements. A driver that offers
// neither is not an error: most older ones report nothing.
func (f *Frontend) Statistics() (Statistics, error) {
	properties := []dtvProperty{newDTVProperty(dtvStatCNR, 0), newDTVProperty(dtvStatSignal, 0)}
	set := dtvProperties{Count: uint32(len(properties)), Properties: &properties[0]}
	request := ioctlRequest(iocRead, dvbIoctlType, feGetPropertyNR, unsafe.Sizeof(set))
	if err := ioctlOf(f.file, request, &set); err != nil {
		return Statistics{}, fmt.Errorf("DTV_STAT_CNR/DTV_STAT_SIGNAL_STRENGTH: %w", err)
	}
	runtime.KeepAlive(properties)
	statistics := Statistics{}
	if value, ok := firstDecibelStat(&properties[0]); ok {
		statistics.CNRdB, statistics.HasCNR = value, true
	}
	if value, ok := firstDecibelStat(&properties[1]); ok {
		statistics.StrengthdBm, statistics.HasStrength = value, true
	}
	return statistics, nil
}

// firstDecibelStat reads stat[0] of a struct dtv_fe_stats out of a property's
// union and converts it from the API's thousandths of a dB. Walked by hand
// because the struct is packed: a length byte, then up to four scale-and-value
// entries.
func firstDecibelStat(property *dtvProperty) (float64, bool) {
	if property.Union[0] == 0 {
		return 0, false // len == 0: statistics are disabled for this property
	}
	if property.Union[1] != scaleDecibel {
		return 0, false // not available, or a relative scale we will not dress up
	}
	value := int64(0)
	for index := 7; index >= 0; index-- {
		value = value<<8 | int64(property.Union[2+index])
	}
	return float64(value) / 1000, true
}

// frontendInfo is struct dvb_frontend_info. Unread fields are named anyway so the
// layout stays checkable against the kernel header.
type frontendInfo struct {
	Name                [128]byte
	Type                uint32
	FrequencyMin        uint32
	FrequencyMax        uint32
	FrequencyStepSize   uint32
	FrequencyTolerance  uint32
	SymbolRateMin       uint32
	SymbolRateMax       uint32
	SymbolRateTolerance uint32
	NotifierDelay       uint32
	Caps                uint32
}

// FrequencyRange reports the frequencies the frontend accepts, in whatever unit
// the delivery system uses: kHz for satellite, Hz otherwise. A zero maximum
// means the driver did not say.
func (f *Frontend) FrequencyRange() (minimum, maximum uint32, err error) {
	info, err := f.Info()
	if err != nil {
		return 0, 0, err
	}
	return info.FrequencyMin, info.FrequencyMax, nil
}

// SelectDeliverySystem puts the frontend into one standard without tuning it.
//
// The frequency limits a frontend reports are those of the standard it is
// currently in, in that standard's units, so a frontend left in satellite mode
// answers a cable question with satellite numbers.
func (f *Frontend) SelectDeliverySystem(delivery uint32) error {
	properties := []dtvProperty{newDTVProperty(dtvDeliverySystem, delivery)}
	if err := setFrontendProperties(f.file, properties); err != nil {
		return fmt.Errorf("DTV_DELIVERY_SYSTEM: %w", err)
	}
	return nil
}

// DeliverySystem is the delivery system the frontend is set to, as a channel file
// spells it: a tuner that will not lock a channel it is capable of is often one
// pointed at the wrong standard. Empty rather than an error when the driver has
// nothing to say.
func (f *Frontend) DeliverySystem() (string, error) {
	properties := []dtvProperty{newDTVProperty(dtvDeliverySystem, 0)}
	set := dtvProperties{Count: uint32(len(properties)), Properties: &properties[0]}
	request := ioctlRequest(iocRead, dvbIoctlType, feGetPropertyNR, unsafe.Sizeof(set))
	if err := ioctlOf(f.file, request, &set); err != nil {
		return "", fmt.Errorf("DTV_DELIVERY_SYSTEM: %w", err)
	}
	runtime.KeepAlive(properties)
	return DeliveryName(properties[0].value()), nil
}

func (f *Frontend) DeliverySystems() ([]uint32, error) {
	property := newDTVProperty(dtvEnumDelivery, 0)
	properties := dtvProperties{Count: 1, Properties: &property}
	request := ioctlRequest(iocRead, dvbIoctlType, feGetPropertyNR, unsafe.Sizeof(properties))
	if err := ioctlOf(f.file, request, &properties); err != nil {
		return nil, fmt.Errorf("DTV_ENUM_DELSYS: %w", err)
	}
	runtime.KeepAlive(property)
	// DTV_ENUM_DELSYS is the one property that uses the union's buffer member: up
	// to thirty-two delivery systems, one byte each, and the count in the word
	// after them.
	count := binary.NativeEndian.Uint32(property.Union[32:36])
	if count > 32 {
		count = 32
	}
	result := make([]uint32, count)
	for i := range result {
		result[i] = uint32(property.Union[i])
	}
	return result, nil
}

func setFrontendProperties(file *os.File, values []dtvProperty) error {
	properties := dtvProperties{Count: uint32(len(values)), Properties: &values[0]}
	request := ioctlRequest(iocWrite, dvbIoctlType, feSetPropertyNR, unsafe.Sizeof(properties))
	err := ioctlOf(file, request, &properties)
	runtime.KeepAlive(values)
	return err
}

func newDTVProperty(command uint32, value uint32) dtvProperty {
	property := dtvProperty{Command: command}
	property.setValue(value)
	return property
}

func StartDemux(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	filter := dmxPESFilter{
		PID:     0x2000,
		Input:   dmxInputFrontend,
		Output:  dmxOutputTSTap,
		PESType: dmxPESOther,
		Flags:   dmxImmediate,
	}
	request := ioctlRequest(iocWrite, dvbIoctlType, dmxSetPESNR, unsafe.Sizeof(filter))
	if err := ioctlOf(f, request, &filter); err != nil {
		f.Close()
		return nil, fmt.Errorf("DMX_SET_PES_FILTER: %w", err)
	}
	return f, nil
}

func StopDemux(f *os.File) {
	request := ioctlRequest(0, dvbIoctlType, dmxStopNR, 0)
	// Closing the file stops the demux anyway, so a failure changes nothing.
	_ = ioctlPointer(f, request, nil)
	_ = f.Close()
}

func ioctlRequest(direction uintptr, ioctlType uintptr, number uintptr, size uintptr) uintptr {
	// asm-generic ioctl encoding used by Linux on amd64, arm, arm64, riscv and other
	// common architectures: dir:2, size:14, type:8, nr:8.
	return direction<<30 | size<<16 | ioctlType<<8 | number
}

// ioctlOf runs one ioctl whose argument is a struct, taking it as a typed
// pointer so the conversion happens here rather than at every call.
func ioctlOf[Argument any](file *os.File, request uintptr, argument *Argument) error {
	return ioctlPointer(file, request, unsafe.Pointer(argument))
}

// ioctlValue runs an ioctl whose argument is the value itself rather than a
// pointer, which is how the frontend's three lead settings are written.
func ioctlValue(file *os.File, request uintptr, value uintptr) error {
	return ioctlRun(file, func(descriptor uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, descriptor, request, value)
		return errno
	})
}

// ioctlPointer runs one DVB ioctl whose argument is a pointer. The pointer is
// turned into a number only inside the system call's own argument list, the one
// place the compiler keeps what it points at where it is.
func ioctlPointer(file *os.File, request uintptr, value unsafe.Pointer) error {
	err := ioctlRun(file, func(descriptor uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, descriptor, request, uintptr(value))
		return errno
	})
	runtime.KeepAlive(value)
	return err
}

// ioctlRun issues one ioctl on a file. The descriptor is borrowed through the
// file rather than read out of it, so it stays this file's for the length of
// the call: a Close arriving meanwhile closes it only once the call is done,
// and a call after Close is refused rather than run on whatever the next open
// took that number for.
//
// EINTR is retried rather than reported: the Go runtime preempts goroutines
// with SIGURG, so any ioctl can come back interrupted. Seen on real hardware as
// "DMX_SET_PES_FILTER: interrupted system call".
func ioctlRun(file *os.File, ioctl func(descriptor uintptr) syscall.Errno) error {
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var errno syscall.Errno
	if err := conn.Control(func(descriptor uintptr) {
		for {
			errno = ioctl(descriptor)
			if errno != syscall.EINTR {
				return
			}
		}
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

var deliverySystemValues = map[string]uint32{
	"DVBC/ANNEX_A":  1,
	"DVB-C/ANNEX_A": 1,
	"DVBC/ANNEX_B":  2,
	"DVB-C/ANNEX_B": 2,
	"DVBT":          3,
	"DVB-T":         3,
	"DVBS":          5,
	"DVB-S":         5,
	"DVBS2":         6,
	"DVB-S2":        6,
	"ISDBT":         8,
	"ISDB-T":        8,
	"ISDBS":         9,
	"ISDB-S":        9,
	// ISDB-S3 is tuned as ISDB-S, because the Linux DVB API numbers ISDB-S and
	// nothing beyond it. One demodulator receives both and what separates them is
	// the stream identifier, which the driver reads out of the transmission
	// control block. The Windows backends name ISDB-S3 outright, so a channel that
	// says so has to reach either of them unchanged.
	"ISDBS3":  9,
	"ISDB-S3": 9,
	// ISDB-C is ITU-T J.83 Annex C and has to be tuned as that. SYS_ISDBC exists
	// in the API's enum but no frontend driver declares it, so sending it would be
	// refused by every device that can receive the signal.
	"ISDBC":         18,
	"ISDB-C":        18,
	"ATSC":          11,
	"DVBT2":         16,
	"DVB-T2":        16,
	"DVBC/ANNEX_C":  18,
	"DVB-C/ANNEX_C": 18,
	"DVBC2":         19,
	"DVB-C2":        19,
}

// deliverySystemNames is the reverse of the table above, one canonical name per
// value.
var deliverySystemNames = map[uint32]string{
	1:  "DVB-C/ANNEX_A",
	2:  "DVB-C/ANNEX_B",
	3:  "DVB-T",
	5:  "DVB-S",
	6:  "DVB-S2",
	8:  "ISDB-T",
	9:  "ISDB-S",
	11: "ATSC",
	16: "DVB-T2",
	18: "DVB-C/ANNEX_C",
	19: "DVB-C2",
}

// DeliveryName names a delivery system the kernel reported, or empty for one this
// program has no name for.
func DeliveryName(system uint32) string { return deliverySystemNames[system] }

// modulationValues are the frontend's own modulations, keyed by the folded
// spelling tuning.NormalizeModulation produces, so one entry answers "QAM/256",
// "QAM256" and "qam-256" alike.
//
// Both vocabularies are here because the two sides spell them differently: the
// kernel's fe_modulation enumeration writes the count first -- 8PSK, 16APSK --
// while libdvbv5 writes it last, so a file from dvbv5-scan says PSK/8 where the
// kernel says 8PSK. A file that works with dvbv5-zap has to work here.
var modulationValues = map[string]uint32{
	"QPSK": 0, "QAM16": 1, "QAM32": 2, "QAM64": 3, "QAM128": 4,
	"QAM256": 5, "QAMAUTO": 6, "DQPSK": 12,
	"8VSB": 7, "VSB8": 7,
	"16VSB": 8, "VSB16": 8,
	"8PSK": 9, "PSK8": 9,
	"16APSK": 10, "APSK16": 10,
	"32APSK": 11, "APSK32": 11,
}

var fecValues = map[string]uint32{
	"NONE": 0, "1/2": 1, "2/3": 2, "3/4": 3, "4/5": 4, "5/6": 5,
	"6/7": 6, "7/8": 7, "8/9": 8, "AUTO": 9, "3/5": 10, "9/10": 11,
	"2/5": 12, "1/3": 13, "1/4": 14,
}

var transmissionModeValues = map[string]uint32{
	"2K": 0, "8K": 1, "AUTO": 2, "4K": 3, "1K": 4, "16K": 5,
	"32K": 6, "C1": 7, "C3780": 8,
}

var guardIntervalValues = map[string]uint32{
	"1/32": 0, "1/16": 1, "1/8": 2, "1/4": 3, "AUTO": 4,
	"1/128": 5, "19/128": 6, "19/256": 7,
}

var hierarchyValues = map[string]uint32{
	"NONE": 0, "1": 1, "2": 2, "4": 3, "AUTO": 4,
}

// The settings that have no channel-file spelling to reuse, because nothing
// tunes by them: they are only ever read back.
var (
	rolloffNames   = map[uint32]string{0: "35", 1: "20", 2: "25", 3: "AUTO", 4: "15", 5: "10", 6: "5"}
	pilotNames     = map[uint32]string{0: "ON", 1: "OFF", 2: "AUTO"}
	inversionNames = map[uint32]string{0: "OFF", 1: "ON", 2: "AUTO"}
)

// named reverses one of the tables above, so a value read back is shown with
// the word a channel file would have used to ask for it.
func named(values map[string]uint32, value uint32) string {
	for name, candidate := range values {
		if candidate == value {
			return name
		}
	}
	return ""
}

// Parameter is one of a frontend's settings, as a name and what it is set to.
type Parameter struct {
	Name  string
	Value string
}

// Parameters reads back everything the frontend will say about how it is
// currently tuned, as dvbv5-fe-tool prints. A driver fills in what it measured
// rather than what it was asked for. A setting the driver did not fill in is
// left out.
func (f *Frontend) Parameters() ([]Parameter, error) {
	wanted := []struct {
		command uint32
		name    string
		render  func(uint32) string
	}{
		{dtvDeliverySystem, "DELIVERY_SYSTEM", DeliveryName},
		{dtvFrequency, "FREQUENCY", number},
		{dtvBandwidthHz, "BANDWIDTH_HZ", number},
		{dtvSymbolRate, "SYMBOL_RATE", number},
		{dtvModulation, "MODULATION", func(value uint32) string { return named(modulationValues, value) }},
		{dtvInnerFEC, "INNER_FEC", func(value uint32) string { return named(fecValues, value) }},
		{dtvCodeRateHP, "CODE_RATE_HP", func(value uint32) string { return named(fecValues, value) }},
		{dtvCodeRateLP, "CODE_RATE_LP", func(value uint32) string { return named(fecValues, value) }},
		{dtvTransmitMode, "TRANSMISSION_MODE", func(value uint32) string { return named(transmissionModeValues, value) }},
		{dtvGuardInterval, "GUARD_INTERVAL", func(value uint32) string { return named(guardIntervalValues, value) }},
		{dtvHierarchy, "HIERARCHY", func(value uint32) string { return named(hierarchyValues, value) }},
		{dtvRolloff, "ROLLOFF", func(value uint32) string { return rolloffNames[value] }},
		{dtvPilot, "PILOT", func(value uint32) string { return pilotNames[value] }},
		{dtvInversion, "INVERSION", func(value uint32) string { return inversionNames[value] }},
		{dtvStreamID, "STREAM_ID", number},
		{dtvScramblingSequenceIndex, tuning.ScramblingProperty, number},
	}

	properties := make([]dtvProperty, 0, len(wanted))
	for _, one := range wanted {
		properties = append(properties, newDTVProperty(one.command, 0))
	}
	set := dtvProperties{Count: uint32(len(properties)), Properties: &properties[0]}
	request := ioctlRequest(iocRead, dvbIoctlType, feGetPropertyNR, unsafe.Sizeof(set))
	if err := ioctlOf(f.file, request, &set); err != nil {
		return nil, fmt.Errorf("FE_GET_PROPERTY: %w", err)
	}
	runtime.KeepAlive(properties)

	answer := make([]Parameter, 0, len(wanted))
	for index, one := range wanted {
		value := properties[index].value()
		rendered := one.render(value)
		if rendered == "" {
			continue
		}
		answer = append(answer, Parameter{Name: one.name, Value: rendered})
	}
	return answer, nil
}

// number renders a count, and nothing at all for zero: a frequency or symbol
// rate of zero is a driver that has not been told one.
func number(value uint32) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(value), 10)
}

func HasLock(status uint32) bool {
	return status&statusHasLock != 0
}

// StatusNames lists the frontend status flags that are set. Capitals, as every
// other backend reports them: they end up side by side in one field.
func StatusNames(status uint32) []string {
	var names []string
	for _, item := range []struct {
		bit  uint32
		name string
	}{
		{statusHasSignal, "SIGNAL"},
		{statusHasCarrier, "CARRIER"},
		{statusHasViterbi, "VITERBI"},
		{statusHasSync, "SYNC"},
		{statusHasLock, "LOCK"},
	} {
		if status&item.bit != 0 {
			names = append(names, item.name)
		}
	}
	return names
}
