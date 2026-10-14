// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package native is the winscard provider that forwards to the resource manager
// Windows already runs. It keeps a user's existing reader working: the
// compatibility DLL is loaded in place of the system one by sitting next to an
// application, and everything not addressed to a built-in slot goes straight
// through to here.
//
// The system library is opened by absolute path. By name the search would find
// the compatibility DLL itself, and the process would load itself until it ran
// out of stack.
package native

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/Till0196/coax/pkg/winscard"
)

// system loads the real WinSCard.dll once, from the system directory.
var system struct {
	once    sync.Once
	library *syscall.LazyDLL
	err     error

	establishContext *syscall.LazyProc
	releaseContext   *syscall.LazyProc
	isValidContext   *syscall.LazyProc
	listReaders      *syscall.LazyProc
	listReaderGroups *syscall.LazyProc
	getStatusChange  *syscall.LazyProc
	cancel           *syscall.LazyProc
	connect          *syscall.LazyProc
	reconnect        *syscall.LazyProc
	disconnect       *syscall.LazyProc
	beginTransaction *syscall.LazyProc
	endTransaction   *syscall.LazyProc
	status           *syscall.LazyProc
	transmit         *syscall.LazyProc
	control          *syscall.LazyProc
	getAttrib        *syscall.LazyProc
	setAttrib        *syscall.LazyProc
}

// load resolves the system library and the entry points used here.
func load() error {
	system.once.Do(func() {
		directory, err := systemDirectory()
		if err != nil {
			system.err = err
			return
		}
		system.library = syscall.NewLazyDLL(filepath.Join(directory, "WinSCard.dll"))
		if err := system.library.Load(); err != nil {
			system.err = fmt.Errorf("load the system WinSCard.dll: %w", err)
			return
		}
		system.establishContext = system.library.NewProc("SCardEstablishContext")
		system.releaseContext = system.library.NewProc("SCardReleaseContext")
		system.isValidContext = system.library.NewProc("SCardIsValidContext")
		system.listReaders = system.library.NewProc("SCardListReadersW")
		system.listReaderGroups = system.library.NewProc("SCardListReaderGroupsW")
		system.getStatusChange = system.library.NewProc("SCardGetStatusChangeW")
		system.cancel = system.library.NewProc("SCardCancel")
		system.connect = system.library.NewProc("SCardConnectW")
		system.reconnect = system.library.NewProc("SCardReconnect")
		system.disconnect = system.library.NewProc("SCardDisconnect")
		system.beginTransaction = system.library.NewProc("SCardBeginTransaction")
		system.endTransaction = system.library.NewProc("SCardEndTransaction")
		system.status = system.library.NewProc("SCardStatusW")
		system.transmit = system.library.NewProc("SCardTransmit")
		system.control = system.library.NewProc("SCardControl")
		system.getAttrib = system.library.NewProc("SCardGetAttrib")
		system.setAttrib = system.library.NewProc("SCardSetAttrib")
	})
	return system.err
}

// systemDirectory asks Windows where its own libraries are, rather than
// assuming a drive letter or trusting an environment variable.
func systemDirectory() (string, error) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	getSystemDirectory := kernel.NewProc("GetSystemDirectoryW")
	buffer := make([]uint16, syscall.MAX_PATH)
	length, _, err := getSystemDirectory.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 || int(length) > len(buffer) {
		return "", fmt.Errorf("locate the system directory: %w", err)
	}
	return syscall.UTF16ToString(buffer[:length]), nil
}

// Provider is the winscard.Provider over the system resource manager.
type Provider struct{}

// New builds the provider. Nothing is loaded until a context is established,
// so a machine with no smart card service costs nothing here.
func New() *Provider { return &Provider{} }

// Name identifies the provider in diagnostics.
func (p *Provider) Name() string { return "native" }

// EstablishContext opens a context on the system resource manager.
func (p *Provider) EstablishContext(scope winscard.Scope) (winscard.ProviderContext, error) {
	if err := load(); err != nil {
		return nil, winscard.ErrNoService
	}
	var handle uintptr
	result, _, _ := system.establishContext.Call(uintptr(scope), 0, 0, uintptr(unsafe.Pointer(&handle)))
	if err := code(result); err != nil {
		return nil, err
	}
	return &providerContext{handle: handle}, nil
}

type providerContext struct {
	handle uintptr

	// waits counts the calls currently blocked in SCardGetStatusChange, so
	// SCardCancel is only sent while one is running: some resource managers
	// remember a cancel that arrived with nothing waiting and apply it to the
	// next call, turning one cancelled wait into a cancelled reader operation
	// later on.
	mutex sync.Mutex
	waits int
}

// Readers lists the readers the system knows.
func (c *providerContext) Readers(groups []string) ([]string, error) {
	var groupArgument uintptr
	if len(groups) > 0 {
		encoded, err := multiStringOf(groups)
		if err != nil {
			return nil, winscard.ErrInvalidParameter
		}
		defer runtime.KeepAlive(encoded)
		groupArgument = uintptr(unsafe.Pointer(&encoded[0]))
	}
	return sized(func(buffer *uint16, length *uint32) uintptr {
		result, _, _ := system.listReaders.Call(c.handle, groupArgument,
			uintptr(unsafe.Pointer(buffer)), uintptr(unsafe.Pointer(length)))
		return result
	})
}

// ReaderGroups lists the system's reader groups.
func (c *providerContext) ReaderGroups() ([]string, error) {
	return sized(func(buffer *uint16, length *uint32) uintptr {
		result, _, _ := system.listReaderGroups.Call(c.handle,
			uintptr(unsafe.Pointer(buffer)), uintptr(unsafe.Pointer(length)))
		return result
	})
}

// sized runs one of the listing calls twice, as the API is built to be used:
// once with no buffer to learn the length, once to fill it. An empty listing
// is not an error -- SCARD_E_NO_READERS_AVAILABLE means this provider has
// nothing to add, and failing the call for it would take the other provider's
// readers away too.
func sized(call func(buffer *uint16, length *uint32) uintptr) ([]string, error) {
	var length uint32
	if err := code(call(nil, &length)); err != nil {
		if errors.Is(err, winscard.ErrNoReadersAvailable) {
			return nil, nil
		}
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}
	buffer := make([]uint16, length)
	if err := code(call(&buffer[0], &length)); err != nil {
		return nil, err
	}
	if int(length) < len(buffer) {
		buffer = buffer[:length]
	}
	return splitMultiString(buffer), nil
}

// Connect opens a card in one of the system's readers.
func (c *providerContext) Connect(reader string, share winscard.ShareMode, protocols winscard.Protocol) (winscard.Card, winscard.Protocol, error) {
	name, err := syscall.UTF16PtrFromString(reader)
	if err != nil {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	var handle uintptr
	var active uint32
	result, _, _ := system.connect.Call(c.handle, uintptr(unsafe.Pointer(name)),
		uintptr(share), uintptr(protocols),
		uintptr(unsafe.Pointer(&handle)), uintptr(unsafe.Pointer(&active)))
	if err := code(result); err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	return &card{handle: handle, reader: reader}, winscard.Protocol(active), nil
}

// Release closes the context.
func (c *providerContext) Release() error {
	result, _, _ := system.releaseContext.Call(c.handle)
	return code(result)
}

// readerState is SCARD_READERSTATEW. The layout is the C one: the two pointers
// are word sized and the ATR is a fixed 36-byte tail.
type readerState struct {
	reader       *uint16
	userData     uintptr
	currentState uint32
	eventState   uint32
	atrLength    uint32
	atr          [36]byte
}

// GetStatusChange waits on the system's readers. The wait is a blocking
// library call, so cancellation is a second goroutine calling SCardCancel when
// the context is done -- the only way to interrupt it, and why the counter
// above exists: the cancel must not outlive the wait it was meant for.
func (c *providerContext) GetStatusChange(ctx context.Context, timeout time.Duration, states []winscard.ReaderState) ([]winscard.ReaderState, error) {
	if len(states) == 0 {
		return states, nil
	}
	native := make([]readerState, len(states))
	for index, state := range states {
		name, err := syscall.UTF16PtrFromString(state.Reader)
		if err != nil {
			return nil, winscard.ErrInvalidParameter
		}
		native[index].reader = name
		native[index].currentState = uint32(state.Current)
	}

	c.mutex.Lock()
	c.waits++
	c.mutex.Unlock()
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			c.mutex.Lock()
			waiting := c.waits > 0
			c.mutex.Unlock()
			if waiting {
				_, _, _ = system.cancel.Call(c.handle)
			}
		case <-finished:
		}
	}()

	result, _, _ := system.getStatusChange.Call(c.handle, uintptr(milliseconds(timeout)),
		uintptr(unsafe.Pointer(&native[0])), uintptr(len(native)))
	close(finished)
	c.mutex.Lock()
	c.waits--
	c.mutex.Unlock()

	if err := code(result); err != nil {
		if ctx.Err() != nil {
			return nil, winscard.ErrCancelled
		}
		return nil, err
	}
	answered := make([]winscard.ReaderState, len(states))
	copy(answered, states)
	for index := range answered {
		answered[index].Event = winscard.State(native[index].eventState)
		length := int(native[index].atrLength)
		if length > len(native[index].atr) {
			length = len(native[index].atr)
		}
		answered[index].ATR = append([]byte(nil), native[index].atr[:length]...)
	}
	return answered, nil
}

type card struct {
	handle uintptr
	reader string
}

// ioRequest is SCARD_IO_REQUEST.
type ioRequest struct {
	protocol  uint32
	pciLength uint32
}

func (c *card) Reconnect(share winscard.ShareMode, protocols winscard.Protocol, initialization winscard.Disposition) (winscard.Protocol, error) {
	var active uint32
	result, _, _ := system.reconnect.Call(c.handle, uintptr(share), uintptr(protocols),
		uintptr(initialization), uintptr(unsafe.Pointer(&active)))
	if err := code(result); err != nil {
		return winscard.ProtocolUndefined, err
	}
	return winscard.Protocol(active), nil
}

func (c *card) Disconnect(disposition winscard.Disposition) error {
	result, _, _ := system.disconnect.Call(c.handle, uintptr(disposition))
	return code(result)
}

func (c *card) BeginTransaction() error {
	result, _, _ := system.beginTransaction.Call(c.handle)
	return code(result)
}

func (c *card) EndTransaction(disposition winscard.Disposition) error {
	result, _, _ := system.endTransaction.Call(c.handle, uintptr(disposition))
	return code(result)
}

func (c *card) Status() (winscard.Status, error) {
	var nameLength, state, protocol, atrLength uint32
	atr := make([]byte, 36)
	atrLength = uint32(len(atr))
	result, _, _ := system.status.Call(c.handle, 0, uintptr(unsafe.Pointer(&nameLength)),
		uintptr(unsafe.Pointer(&state)), uintptr(unsafe.Pointer(&protocol)),
		uintptr(unsafe.Pointer(&atr[0])), uintptr(unsafe.Pointer(&atrLength)))
	if err := code(result); err != nil {
		return winscard.Status{}, err
	}
	if int(atrLength) > len(atr) {
		atrLength = uint32(len(atr))
	}
	return winscard.Status{
		Readers:  []string{c.reader},
		State:    winscard.CardState(state),
		Protocol: winscard.Protocol(protocol),
		ATR:      append([]byte(nil), atr[:atrLength]...),
	}, nil
}

func (c *card) Transmit(send []byte, sendProtocol winscard.Protocol) ([]byte, winscard.Protocol, error) {
	if len(send) == 0 {
		return nil, winscard.ProtocolUndefined, winscard.ErrInvalidParameter
	}
	request := ioRequest{protocol: uint32(sendProtocol), pciLength: uint32(unsafe.Sizeof(ioRequest{}))}
	answer := ioRequest{}
	// The largest a short APDU answer can be, plus room for an extended one:
	// the system copies at most what it is given and says how much it wanted.
	receive := make([]byte, 65538)
	length := uint32(len(receive))
	result, _, _ := system.transmit.Call(c.handle, uintptr(unsafe.Pointer(&request)),
		uintptr(unsafe.Pointer(&send[0])), uintptr(len(send)),
		uintptr(unsafe.Pointer(&answer)),
		uintptr(unsafe.Pointer(&receive[0])), uintptr(unsafe.Pointer(&length)))
	if err := code(result); err != nil {
		return nil, winscard.ProtocolUndefined, err
	}
	if int(length) > len(receive) {
		length = uint32(len(receive))
	}
	return append([]byte(nil), receive[:length]...), winscard.Protocol(answer.protocol), nil
}

func (c *card) Control(controlCode uint32, send []byte) ([]byte, error) {
	receive := make([]byte, 4096)
	var returned uint32
	var sendPointer uintptr
	if len(send) > 0 {
		sendPointer = uintptr(unsafe.Pointer(&send[0]))
	}
	result, _, _ := system.control.Call(c.handle, uintptr(controlCode),
		sendPointer, uintptr(len(send)),
		uintptr(unsafe.Pointer(&receive[0])), uintptr(len(receive)),
		uintptr(unsafe.Pointer(&returned)))
	if err := code(result); err != nil {
		return nil, err
	}
	if int(returned) > len(receive) {
		returned = uint32(len(receive))
	}
	return append([]byte(nil), receive[:returned]...), nil
}

func (c *card) GetAttrib(id winscard.Attribute) ([]byte, error) {
	var length uint32
	result, _, _ := system.getAttrib.Call(c.handle, uintptr(id), 0, uintptr(unsafe.Pointer(&length)))
	if err := code(result); err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}
	value := make([]byte, length)
	result, _, _ = system.getAttrib.Call(c.handle, uintptr(id),
		uintptr(unsafe.Pointer(&value[0])), uintptr(unsafe.Pointer(&length)))
	if err := code(result); err != nil {
		return nil, err
	}
	if int(length) > len(value) {
		length = uint32(len(value))
	}
	return value[:length], nil
}

func (c *card) SetAttrib(id winscard.Attribute, value []byte) error {
	var pointer uintptr
	if len(value) > 0 {
		pointer = uintptr(unsafe.Pointer(&value[0]))
	}
	result, _, _ := system.setAttrib.Call(c.handle, uintptr(id), pointer, uintptr(len(value)))
	return code(result)
}

// code turns a LONG status into this package's error convention.
func code(result uintptr) error {
	return winscard.Code(uint32(result)).OrNil()
}

// milliseconds turns a timeout into the DWORD the API takes, where the
// infinite wait is all ones.
func milliseconds(timeout time.Duration) uint32 {
	if timeout < 0 {
		return 0xFFFFFFFF
	}
	return uint32(timeout / time.Millisecond)
}

// multiStringOf encodes a list of strings as the double-terminated block the
// API takes.
func multiStringOf(values []string) ([]uint16, error) {
	var encoded []uint16
	for _, value := range values {
		part, err := syscall.UTF16FromString(value)
		if err != nil {
			return nil, err
		}
		encoded = append(encoded, part...)
	}
	return append(encoded, 0), nil
}

func splitMultiString(buffer []uint16) []string {
	var values []string
	start := 0
	for index, unit := range buffer {
		if unit != 0 {
			continue
		}
		if index == start {
			break
		}
		values = append(values, syscall.UTF16ToString(buffer[start:index]))
		start = index + 1
	}
	return values
}
