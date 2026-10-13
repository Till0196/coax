// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package tv is the tuning half of BDA: what a channel is asked for through.
//
// A tune is three objects. A tuning space says what kind of network this is and
// what the dish in front of it does; a locator says where on that network the
// channel sits; and a tune request joins them and names the stream wanted. The
// tuner is given the space, then the request.
//
// See the win32 package for why these are written out rather than taken from
// bindings.
package tv

import (
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
	"github.com/Till0196/coax/internal/coaxd/win32/directshow"
)

// The identifiers this program asks for by name.
var (
	IID_ITuner = win32.GUID{Data1: 0x28c52640, Data2: 0x018a, Data3: 0x11d3,
		Data4: [8]byte{0x9d, 0x8e, 0x00, 0xc0, 0x4f, 0x72, 0xd9, 0x80}}
	IID_ITuningSpace = win32.GUID{Data1: 0x061c6e30, Data2: 0xe622, Data3: 0x11d2,
		Data4: [8]byte{0x94, 0x93, 0x00, 0xc0, 0x4f, 0x72, 0xd9, 0x80}}
	IID_ILocator = win32.GUID{Data1: 0x286d7f89, Data2: 0x760c, Data3: 0x4f89,
		Data4: [8]byte{0x80, 0xc4, 0x66, 0x84, 0x1d, 0x25, 0x07, 0xaa}}
	IID_IDVBTuningSpace = win32.GUID{Data1: 0xada0b268, Data2: 0x3b19, Data3: 0x4e5b,
		Data4: [8]byte{0xac, 0xc4, 0x49, 0xf8, 0x52, 0xbe, 0x13, 0xba}}
	IID_IDVBSTuningSpace = win32.GUID{Data1: 0xcdf7be60, Data2: 0xd954, Data3: 0x42fd,
		Data4: [8]byte{0xa9, 0x72, 0x78, 0x97, 0x19, 0x58, 0xe4, 0x70}}
	IID_IDVBTuneRequest = win32.GUID{Data1: 0x0d6f567e, Data2: 0xa636, Data3: 0x42bb,
		Data4: [8]byte{0x83, 0xba, 0xce, 0x4c, 0x17, 0x04, 0xaf, 0xa2}}
	IID_IDVBTLocator = win32.GUID{Data1: 0x8664da16, Data2: 0xdda2, Data3: 0x42ac,
		Data4: [8]byte{0x92, 0x6a, 0xc1, 0x8f, 0x91, 0x27, 0xc3, 0x02}}
	IID_IDVBSLocator = win32.GUID{Data1: 0x3d7c353c, Data2: 0x0d04, Data3: 0x45f1,
		Data4: [8]byte{0xa7, 0x42, 0xf9, 0x7c, 0xc1, 0x18, 0x8d, 0xc8}}
)

// The interfaces.
//
// The DVB ones extend the plain ones, and the slot numbers below say so: a
// method of IDVBSTuningSpace sits past every method of ITuningSpace, because
// its table begins with them.
type (
	ITuner           struct{ win32.IUnknown }
	ITuningSpace     struct{ win32.IUnknown }
	ITuneRequest     struct{ win32.IUnknown }
	ILocator         struct{ win32.IUnknown }
	IDVBTuningSpace  struct{ ITuningSpace }
	IDVBSTuningSpace struct{ IDVBTuningSpace }
	IDVBTuneRequest  struct{ ITuneRequest }
	IDVBTLocator     struct{ ILocator }
	IDVBSLocator     struct{ ILocator }
)

// ITuner is the tuner filter's own interface: what a channel is finally asked
// for through.
func (self *ITuner) Put_TuningSpace(TuningSpace *ITuningSpace) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(TuningSpace)))
	return win32.ErrIfFailed(int32(result))
}

func (self *ITuner) Put_TuneRequest(TuneRequest *ITuneRequest) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[7],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(TuneRequest)))
	return win32.ErrIfFailed(int32(result))
}

// Get_SignalStrength is BDA's own quality figure rather than a level in
// decibels: what it means is the driver's business, which is why this program
// reports it as a percentage and not as a measurement.
func (self *ITuner) Get_SignalStrength() (int32, error) {
	var strength int32
	result, _, _ := syscall.SyscallN(self.LpVtbl[11],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(&strength)))
	return strength, win32.ErrIfFailed(int32(result))
}

// ITuningSpace says what kind of network is being tuned.
func (self *ITuningSpace) Put_UniqueName(Name win32.BSTR) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(Name))
	return win32.ErrIfFailed(int32(result))
}

func (self *ITuningSpace) Put_FriendlyName(Name win32.BSTR) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[10],
		uintptr(unsafe.Pointer(self)), uintptr(Name))
	return win32.ErrIfFailed(int32(result))
}

// Put__NetworkType is spelled with two underscores because the interface has
// both a string form and a GUID form of the same property, and this is the
// GUID one.
func (self *ITuningSpace) Put__NetworkType(NetworkTypeGuid *win32.GUID) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[15],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(NetworkTypeGuid)))
	return win32.ErrIfFailed(int32(result))
}

func (self *ITuningSpace) CreateTuneRequest() (*ITuneRequest, error) {
	var request *ITuneRequest
	result, _, _ := syscall.SyscallN(self.LpVtbl[16],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(&request)))
	return request, win32.ErrIfFailed(int32(result))
}

func (self *ITuningSpace) Put_DefaultLocator(LocatorVal *ILocator) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[24],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(LocatorVal)))
	return win32.ErrIfFailed(int32(result))
}

// IDVBTuningSpace adds which of the DVB and ISDB systems this is.
func (self *IDVBTuningSpace) Put_SystemType(SysType directshow.DVBSystemType) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[27],
		uintptr(unsafe.Pointer(self)), uintptr(SysType))
	return win32.ErrIfFailed(int32(result))
}

// IDVBSTuningSpace adds what the block on the dish does to the frequencies.
func (self *IDVBSTuningSpace) Put_LowOscillator(LowOscillator int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[31],
		uintptr(unsafe.Pointer(self)), uintptr(LowOscillator))
	return win32.ErrIfFailed(int32(result))
}

func (self *IDVBSTuningSpace) Put_HighOscillator(HighOscillator int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[33],
		uintptr(unsafe.Pointer(self)), uintptr(HighOscillator))
	return win32.ErrIfFailed(int32(result))
}

func (self *IDVBSTuningSpace) Put_LNBSwitch(LNBSwitch int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[35],
		uintptr(unsafe.Pointer(self)), uintptr(LNBSwitch))
	return win32.ErrIfFailed(int32(result))
}

// ITuneRequest joins a locator to a tuning space.
func (self *ITuneRequest) Put_Locator(Locator *ILocator) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[11],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(Locator)))
	return win32.ErrIfFailed(int32(result))
}

// IDVBTuneRequest names the stream wanted inside the multiplex.
func (self *IDVBTuneRequest) Put_ONID(ONID int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[13],
		uintptr(unsafe.Pointer(self)), uintptr(ONID))
	return win32.ErrIfFailed(int32(result))
}

func (self *IDVBTuneRequest) Put_TSID(TSID int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[15],
		uintptr(unsafe.Pointer(self)), uintptr(TSID))
	return win32.ErrIfFailed(int32(result))
}

func (self *IDVBTuneRequest) Put_SID(SID int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[17],
		uintptr(unsafe.Pointer(self)), uintptr(SID))
	return win32.ErrIfFailed(int32(result))
}

// ILocator is where on the network the channel sits.
func (self *ILocator) Put_CarrierFrequency(Frequency int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(Frequency))
	return win32.ErrIfFailed(int32(result))
}

func (self *ILocator) Get_InnerFECRate() (directshow.BinaryConvolutionCodeRate, error) {
	var rate directshow.BinaryConvolutionCodeRate
	result, _, _ := syscall.SyscallN(self.LpVtbl[11],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(&rate)))
	return rate, win32.ErrIfFailed(int32(result))
}

func (self *ILocator) Get_OuterFECRate() (directshow.BinaryConvolutionCodeRate, error) {
	var rate directshow.BinaryConvolutionCodeRate
	result, _, _ := syscall.SyscallN(self.LpVtbl[15],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(&rate)))
	return rate, win32.ErrIfFailed(int32(result))
}

func (self *ILocator) Put_Modulation(Modulation directshow.ModulationType) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[18],
		uintptr(unsafe.Pointer(self)), uintptr(Modulation))
	return win32.ErrIfFailed(int32(result))
}

func (self *ILocator) Get_SymbolRate() (int32, error) {
	var rate int32
	result, _, _ := syscall.SyscallN(self.LpVtbl[19],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(&rate)))
	return rate, win32.ErrIfFailed(int32(result))
}

func (self *ILocator) Put_SymbolRate(Rate int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[20],
		uintptr(unsafe.Pointer(self)), uintptr(Rate))
	return win32.ErrIfFailed(int32(result))
}

// IDVBTLocator adds the channel width, and IDVBSLocator the polarisation.
func (self *IDVBTLocator) Put_Bandwidth(BandwidthVal int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[23],
		uintptr(unsafe.Pointer(self)), uintptr(BandwidthVal))
	return win32.ErrIfFailed(int32(result))
}

func (self *IDVBSLocator) Put_SignalPolarisation(PolarisationVal directshow.Polarisation) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[23],
		uintptr(unsafe.Pointer(self)), uintptr(PolarisationVal))
	return win32.ErrIfFailed(int32(result))
}
