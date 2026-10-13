// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package directshow is as much of DirectShow and BDA as this program uses.
//
// See the win32 package for why these are written out rather than taken from
// generated bindings, and for how a slot number becomes a method call.
package directshow

import (
	"runtime"
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/win32"
)

// How a signal is modulated, and how it is protected. These are BDA's own
// numbering, which is what the driver is told and what it answers with.
type (
	ModulationType            int32
	FECMethod                 int32
	BinaryConvolutionCodeRate int32
	Polarisation              int32
	DVBSystemType             int32
	PIN_DIRECTION             int32
)

const (
	BDA_MOD_NOT_SET     ModulationType = -1
	BDA_MOD_16QAM       ModulationType = 1
	BDA_MOD_32QAM       ModulationType = 2
	BDA_MOD_64QAM       ModulationType = 3
	BDA_MOD_128QAM      ModulationType = 7
	BDA_MOD_256QAM      ModulationType = 11
	BDA_MOD_QPSK        ModulationType = 20
	BDA_MOD_8VSB        ModulationType = 23
	BDA_MOD_16VSB       ModulationType = 24
	BDA_MOD_8PSK        ModulationType = 27
	BDA_MOD_16APSK      ModulationType = 29
	BDA_MOD_32APSK      ModulationType = 30
	BDA_MOD_ISDB_T_TMCC ModulationType = 34
	BDA_MOD_ISDB_S_TMCC ModulationType = 35
)

const (
	BDA_FEC_VITERBI    FECMethod = 1
	BDA_FEC_RS_204_188 FECMethod = 2
)

// The code rates, whose numbering has gaps in it: 4 and 7 are not the rates
// that would fall between these, they are simply not used.
const (
	BDA_BCC_RATE_1_2 BinaryConvolutionCodeRate = 1
	BDA_BCC_RATE_2_3 BinaryConvolutionCodeRate = 2
	BDA_BCC_RATE_3_4 BinaryConvolutionCodeRate = 3
	BDA_BCC_RATE_4_5 BinaryConvolutionCodeRate = 5
	BDA_BCC_RATE_5_6 BinaryConvolutionCodeRate = 6
	BDA_BCC_RATE_7_8 BinaryConvolutionCodeRate = 8
)

const (
	BDA_POLARISATION_LINEAR_H   Polarisation = 1
	BDA_POLARISATION_LINEAR_V   Polarisation = 2
	BDA_POLARISATION_CIRCULAR_L Polarisation = 3
	BDA_POLARISATION_CIRCULAR_R Polarisation = 4
)

// Which system a tuning space is for. The two ISDB ones are why this program
// can tune Japanese broadcasting through a Windows driver at all.
const (
	DVB_Cable        DVBSystemType = 0
	DVB_Terrestrial  DVBSystemType = 1
	DVB_Satellite    DVBSystemType = 2
	ISDB_Terrestrial DVBSystemType = 3
	ISDB_Satellite   DVBSystemType = 4
)

const (
	PINDIR_INPUT  PIN_DIRECTION = 0
	PINDIR_OUTPUT PIN_DIRECTION = 1
)

// What a filter and a pin say about themselves.
type FILTER_INFO struct {
	AchName [128]uint16
	PGraph  *IFilterGraph
}

type PIN_INFO struct {
	PFilter *IBaseFilter
	Dir     PIN_DIRECTION
	AchName [128]uint16
}

// AM_MEDIA_TYPE describes what flows down a connection.
type AM_MEDIA_TYPE struct {
	Majortype            win32.GUID
	Subtype              win32.GUID
	BFixedSizeSamples    int32
	BTemporalCompression int32
	LSampleSize          uint32
	Formattype           win32.GUID
	PUnk                 *win32.IUnknown
	CbFormat             uint32
	PbFormat             *byte
}

// The identifiers this program asks for by name.
var (
	IID_IBaseFilter = win32.GUID{Data1: 0x56a86895, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IBDA_DigitalDemodulator = win32.GUID{Data1: 0xef30f379, Data2: 0x985b, Data3: 0x4d10,
		Data4: [8]byte{0xb6, 0x40, 0xa7, 0x9d, 0x5e, 0x04, 0xe1, 0xe0}}
	// F84E2AB0-3C6B-45E3-A0FC-8669D4B81F11, from bdaiface.h.
	IID_IBDA_DiseqCommand = win32.GUID{Data1: 0xf84e2ab0, Data2: 0x3c6b, Data3: 0x45e3,
		Data4: [8]byte{0xa0, 0xfc, 0x86, 0x69, 0xd4, 0xb8, 0x1f, 0x11}}
	IID_IBDA_SignalStatistics = win32.GUID{Data1: 0x1347d106, Data2: 0xcf3a, Data3: 0x428a,
		Data4: [8]byte{0xa5, 0xcb, 0xac, 0x0d, 0x9a, 0x2a, 0x43, 0x38}}
	IID_IBDA_Topology = win32.GUID{Data1: 0x79b56888, Data2: 0x7fea, Data3: 0x4690,
		Data4: [8]byte{0xb4, 0x5d, 0x38, 0xfd, 0x3c, 0x78, 0x49, 0xbe}}
	IID_ICreateDevEnum = win32.GUID{Data1: 0x29840822, Data2: 0x5b84, Data3: 0x11d0,
		Data4: [8]byte{0xbd, 0x3b, 0x00, 0xa0, 0xc9, 0x11, 0xce, 0x86}}
	IID_IEnumPins = win32.GUID{Data1: 0x56a86892, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IGraphBuilder = win32.GUID{Data1: 0x56a868a9, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IMediaControl = win32.GUID{Data1: 0x56a868b1, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IMediaEvent = win32.GUID{Data1: 0x56a868b6, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IMediaFilter = win32.GUID{Data1: 0x56a86899, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IMemInputPin = win32.GUID{Data1: 0x56a8689d, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
	IID_IPin = win32.GUID{Data1: 0x56a86891, Data2: 0x0ad4, Data3: 0x11ce,
		Data4: [8]byte{0xb0, 0x3a, 0x00, 0x20, 0xaf, 0x0b, 0xa7, 0x70}}
)

// The interfaces. Each is a table of function addresses and nothing else, so
// each is the base interface and a set of methods that call by slot.
type (
	IBaseFilter             struct{ win32.IUnknown }
	IBDA_DigitalDemodulator struct{ win32.IUnknown }
	IBDA_DiseqCommand       struct{ win32.IUnknown }
	IBDA_SignalStatistics   struct{ win32.IUnknown }
	IBDA_Topology           struct{ win32.IUnknown }
	ICreateDevEnum          struct{ win32.IUnknown }
	IEnumFilters            struct{ win32.IUnknown }
	IEnumPins               struct{ win32.IUnknown }
	IFilterGraph            struct{ win32.IUnknown }
	IGraphBuilder           struct{ IFilterGraph }
	IMediaControl           struct{ win32.IUnknown }
	IMediaEvent             struct{ win32.IUnknown }
	IMediaFilter            struct{ win32.IUnknown }
	IMediaSample            struct{ win32.IUnknown }
	IPin                    struct{ win32.IUnknown }
	IReferenceClock         struct{ win32.IUnknown }
)

// ICreateDevEnum makes the enumerator for a category of devices.
func (self *ICreateDevEnum) CreateClassEnumerator(clsidDeviceClass *win32.GUID, ppEnumMoniker **win32.IEnumMoniker, dwFlags uint32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(clsidDeviceClass)),
		uintptr(unsafe.Pointer(ppEnumMoniker)), uintptr(dwFlags))
	return win32.ErrIfFailed(int32(result))
}

// IBaseFilter is one box in a graph.
func (self *IBaseFilter) EnumPins(ppEnum **IEnumPins) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[10],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(ppEnum)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBaseFilter) QueryFilterInfo(pInfo *FILTER_INFO) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[12],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pInfo)))
	return win32.ErrIfFailed(int32(result))
}

// IEnumFilters and IEnumPins walk what a graph and a filter hold. Both answer
// a result as well as an error, because running out is a success.
func (self *IEnumFilters) Next(ppFilter []*IBaseFilter, pcFetched *uint32) (win32.HRESULT, error) {
	var first **IBaseFilter
	if len(ppFilter) > 0 {
		first = &ppFilter[0]
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(len(ppFilter)),
		uintptr(unsafe.Pointer(first)), uintptr(unsafe.Pointer(pcFetched)))
	return win32.HRESULT(result), win32.ErrIfFailed(int32(result))
}

func (self *IEnumPins) Next(ppPins []*IPin, pcFetched *uint32) (win32.HRESULT, error) {
	var first **IPin
	if len(ppPins) > 0 {
		first = &ppPins[0]
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(len(ppPins)),
		uintptr(unsafe.Pointer(first)), uintptr(unsafe.Pointer(pcFetched)))
	return win32.HRESULT(result), win32.ErrIfFailed(int32(result))
}

// IFilterGraph holds the filters, and IGraphBuilder is the same thing with
// the connecting added -- which is why the one embeds the other rather than
// repeating its slots.
func (self *IFilterGraph) AddFilter(pFilter *IBaseFilter, pName string) error {
	name, err := syscall.UTF16PtrFromString(pName)
	if err != nil {
		return err
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFilter)), uintptr(unsafe.Pointer(name)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IFilterGraph) RemoveFilter(pFilter *IBaseFilter) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFilter)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IFilterGraph) EnumFilters(ppEnum **IEnumFilters) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[5],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(ppEnum)))
	return win32.ErrIfFailed(int32(result))
}

// Connect asks the graph to join two pins, working out for itself what has to
// go between them.
func (self *IGraphBuilder) Connect(ppinOut *IPin, ppinIn *IPin) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[11],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(ppinOut)), uintptr(unsafe.Pointer(ppinIn)))
	return win32.ErrIfFailed(int32(result))
}

// IPin is one end of a connection.
func (self *IPin) Connect(pReceivePin *IPin, pmt *AM_MEDIA_TYPE) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pReceivePin)), uintptr(unsafe.Pointer(pmt)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IPin) ConnectedTo(pPin **IPin) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[6],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pPin)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IPin) QueryDirection(pPinDir *PIN_DIRECTION) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[9],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pPinDir)))
	return win32.ErrIfFailed(int32(result))
}

// IMediaControl starts and stops a graph; IMediaFilter does the same one
// filter at a time, which is what a graph with nothing to synchronise to
// wants.
func (self *IMediaControl) Run() error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[7], uintptr(unsafe.Pointer(self)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IMediaControl) Stop() error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[9], uintptr(unsafe.Pointer(self)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IMediaFilter) Stop() error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4], uintptr(unsafe.Pointer(self)))
	return win32.ErrIfFailed(int32(result))
}

// Run takes the time to start at, which is sixty-four bits wide on every
// architecture -- so it is passed as two words where a word is thirty-two bits
// and one where it is sixty-four.
func (self *IMediaFilter) Run(tStart int64) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[6], append([]uintptr{uintptr(unsafe.Pointer(self))}, wide(tStart)...)...)
	return win32.ErrIfFailed(int32(result))
}

// SetSyncSource is how a graph is told to run as fast as the data arrives
// rather than against a clock, by being given no clock at all.
func (self *IMediaFilter) SetSyncSource(pClock *IReferenceClock) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pClock)))
	return win32.ErrIfFailed(int32(result))
}

// IMediaEvent is where a graph says what went wrong.
func (self *IMediaEvent) GetEvent(lEventCode *int32, lParam1 *uintptr, lParam2 *uintptr, msTimeout int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(lEventCode)),
		uintptr(unsafe.Pointer(lParam1)), uintptr(unsafe.Pointer(lParam2)), uintptr(msTimeout))
	return win32.ErrIfFailed(int32(result))
}

// IMediaSample is one buffer arriving.
func (self *IMediaSample) GetPointer(ppBuffer **byte) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(ppBuffer)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IMediaSample) GetActualDataLength() int32 {
	result, _, _ := syscall.SyscallN(self.LpVtbl[11], uintptr(unsafe.Pointer(self)))
	return int32(result)
}

// IBDA_Topology is how a driver's own nodes are reached, which is where the
// measurements and the demodulator settings live.
func (self *IBDA_Topology) GetNodeTypes(pulcNodeTypes *uint32, rgulNodeTypes []uint32) error {
	var first *uint32
	if len(rgulNodeTypes) > 0 {
		first = &rgulNodeTypes[0]
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pulcNodeTypes)),
		uintptr(len(rgulNodeTypes)), uintptr(unsafe.Pointer(first)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_Topology) GetNodeInterfaces(ulNodeType uint32, pulcInterfaces *uint32, rgguidInterfaces []win32.GUID) error {
	var first *win32.GUID
	if len(rgguidInterfaces) > 0 {
		first = &rgguidInterfaces[0]
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[5],
		uintptr(unsafe.Pointer(self)), uintptr(ulNodeType), uintptr(unsafe.Pointer(pulcInterfaces)),
		uintptr(len(rgguidInterfaces)), uintptr(unsafe.Pointer(first)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_Topology) GetControlNode(ulInputPinId uint32, ulOutputPinId uint32, ulNodeType uint32, ppControlNode **win32.IUnknown) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[13],
		uintptr(unsafe.Pointer(self)), uintptr(ulInputPinId), uintptr(ulOutputPinId),
		uintptr(ulNodeType), uintptr(unsafe.Pointer(ppControlNode)))
	return win32.ErrIfFailed(int32(result))
}

// IBDA_SignalStatistics is what a driver says about what it is receiving.
func (self *IBDA_SignalStatistics) Get_SignalStrength(plDbStrength *int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(plDbStrength)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_SignalStatistics) Get_SignalQuality(plPercentQuality *int32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[6],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(plPercentQuality)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_SignalStatistics) Get_SignalLocked(pfLocked *byte) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[10],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pfLocked)))
	return win32.ErrIfFailed(int32(result))
}

// IBDA_DiseqCommand is how a message is put on the aerial lead. The driver
// does the timing: where the Linux frontend takes a voltage, a message and a
// burst as three ioctls in an order that matters, here the bytes are handed
// over and the driver adds whatever the hardware needs around them -- so the
// two backends' DiSEqC support is shaped differently even though what reaches
// the switch is the same. The put_ spelling is the header's, not a choice
// made here.
func (self *IBDA_DiseqCommand) Put_EnableDiseqCommands(bEnable byte) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[3],
		uintptr(unsafe.Pointer(self)), uintptr(bEnable))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DiseqCommand) Put_DiseqLNBSource(ulLNBSource uint32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(ulLNBSource))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DiseqCommand) Put_DiseqUseToneBurst(bUseToneBurst byte) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[5],
		uintptr(unsafe.Pointer(self)), uintptr(bUseToneBurst))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DiseqCommand) Put_DiseqRepeats(ulRepeats uint32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[6],
		uintptr(unsafe.Pointer(self)), uintptr(ulRepeats))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DiseqCommand) Put_DiseqSendCommand(ulRequestId uint32, command []byte) error {
	if len(command) == 0 {
		return nil
	}
	result, _, _ := syscall.SyscallN(self.LpVtbl[7],
		uintptr(unsafe.Pointer(self)), uintptr(ulRequestId), uintptr(len(command)),
		uintptr(unsafe.Pointer(&command[0])))
	runtime.KeepAlive(command)
	return win32.ErrIfFailed(int32(result))
}

// IBDA_DigitalDemodulator is how the demodulator is set and asked.
func (self *IBDA_DigitalDemodulator) Get_ModulationType(pModulationType *ModulationType) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[4],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pModulationType)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Get_InnerFECMethod(pFECMethod *FECMethod) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[6],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFECMethod)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Get_InnerFECRate(pFECRate *BinaryConvolutionCodeRate) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[8],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFECRate)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Get_OuterFECMethod(pFECMethod *FECMethod) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[10],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFECMethod)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Get_OuterFECRate(pFECRate *BinaryConvolutionCodeRate) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[12],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pFECRate)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Put_SymbolRate(pSymbolRate *uint32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[13],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pSymbolRate)))
	return win32.ErrIfFailed(int32(result))
}

func (self *IBDA_DigitalDemodulator) Get_SymbolRate(pSymbolRate *uint32) error {
	result, _, _ := syscall.SyscallN(self.LpVtbl[14],
		uintptr(unsafe.Pointer(self)), uintptr(unsafe.Pointer(pSymbolRate)))
	return win32.ErrIfFailed(int32(result))
}

// wide splits a sixty-four bit argument into the words a call takes: two where
// a machine word is thirty-two bits, one where it is sixty-four. Nothing else
// in this program passes anything that wide.
func wide(value int64) []uintptr {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return []uintptr{uintptr(value)}
	}
	return []uintptr{uintptr(uint32(value)), uintptr(uint32(uint64(value) >> 32))}
}
