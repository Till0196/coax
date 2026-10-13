// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

// Package windriver reaches a PCI Express card whose Windows driver is a
// renamed WinDriver. Nothing here loads the vendor's user-mode library: see
// ioctl_windows.go for why, and for where the requests it sends instead came
// from.
package windriver

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// Library is one WinDriver installation: the kernel driver, the licence it was
// built with, and the name of the user-mode library that would have talked to
// it. A vendor's driver is a renamed copy, so all of them differ per card and
// the backend supplies them.
type Library struct {
	// DLL is the WinDriver user-mode library, "wdapi1200.dll" for version 12. It
	// is not loaded -- this package talks to the kernel driver directly -- and is
	// kept because its name is how a vendor's installation says which version of
	// WinDriver it is.
	DLL string
	// Driver is the kernel service name, which for a renamed driver is the
	// vendor's own: "windrvr1200_EARTHSOFT_PT3".
	Driver string
	// Licence is what the vendor's driver was registered with. Without it the
	// driver answers every call with WD_NO_LICENSE.
	Licence string
}

// Slot is where a card sits on the bus.
type Slot struct {
	Bus      uint32
	Slot     uint32
	Function uint32
}

func (s Slot) String() string { return fmt.Sprintf("%d:%d.%d", s.Bus, s.Slot, s.Function) }

// window is one memory window of a card as the driver mapped it: the window
// itself, seen as memory of this process.
type window []byte

// Device is one opened card, safe for concurrent use: the mapped windows are
// read and written where they stand, and nothing else is mutated after opening.
type Device struct {
	slot   Slot
	driver *driver
	// registration is the WD_CARD_REGISTER this card was opened with, kept
	// whole: it carries the handle the card is given back by, and the driver
	// wrote the mapped addresses into it.
	registration []byte
	// windows is each memory window by address space number. See Spaces.
	windows map[uint32]window
	spaces  []uint32
	// kernel is where the driver reaches each window, by address space number,
	// or zero where it was opened without a kernel mapping. See SetupCleanup.
	kernel map[uint32]uint64

	// mastering is whether bus mastering was on when the card was opened, which
	// is what a tidy Close leaves it as. untrusted is set once a transfer could
	// not be seen to stop; such a card is closed with bus mastering taken away.
	mastering bool
	untrusted bool

	mutex  sync.Mutex
	closed bool
	// cleanupSet is whether SetupCleanup has been called. The driver keeps one
	// set of accesses per card.
	cleanupSet bool
}

func (d *Device) Slot() Slot { return d.slot }

// ErrNotInstalled reports that the vendor's WinDriver kernel service is not on
// this system, which is what an uninstalled driver looks like.
var ErrNotInstalled = errors.New("the WinDriver driver for this card is not installed")

// Open claims the driver. It may be called more than once: the driver is opened
// on the first call and closed when the last holder releases it.
func (l Library) Open() (*Session, error) {
	if err := l.measured(); err != nil {
		return nil, err
	}
	held, err := holdDriver(l)
	if err != nil {
		return nil, err
	}
	return &Session{library: l, driver: held}, nil
}

// measured refuses an installation whose structures this package does not know,
// from the name of the library the vendor shipped. It is the cheap half of the
// check; driver.checkVersion is the half that asks the driver itself.
func (l Library) measured() error {
	version := measuredVersion(l.DLL)
	if version == measuredWinDriver {
		return nil
	}
	return fmt.Errorf("%s is WinDriver %s, and this program's structure layouts were measured against %s; "+
		"they have to be measured again before it can be used (see internal/coaxd/pcie/windriver/ioctl_windows.go)",
		l.DLL, describeVersion(version), describeVersion(measuredWinDriver))
}

// measuredWinDriver is the version the layouts hold for, as the library's own
// name spells it: WinDriver 12.0.0 is wdapi1200.dll.
const measuredWinDriver = "1200"

func measuredVersion(dll string) string {
	digits := strings.TrimSuffix(strings.TrimPrefix(strings.ToLower(dll), "wdapi"), ".dll")
	for _, letter := range digits {
		if letter < '0' || letter > '9' {
			return ""
		}
	}
	return digits
}

// describeVersion writes a library's digits the way its release notes do.
func describeVersion(digits string) string {
	if len(digits) < 3 {
		if digits == "" {
			return "of an unreadable version"
		}
		return digits
	}
	major := digits[:len(digits)-2]
	return fmt.Sprintf("%s.%c.%c", major, digits[len(digits)-2], digits[len(digits)-1])
}

// Session is a claim on an opened driver.
type Session struct {
	library Library
	driver  *driver
	closed  bool
}

// Close releases the claim, and the driver with it if this was the last one.
func (s *Session) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.driver.release()
}

// Version is what the driver says about itself, for a listing or a log.
func (s *Session) Version() string { return s.driver.version() }

// Scan lists the slots holding a card with this PCI identity.
func (s *Session) Scan(vendor, device uint16) ([]Slot, error) {
	request := make([]byte, scanSize)
	putU32(request, scanVendorOffset, uint32(vendor))
	putU32(request, scanDeviceOffset, uint32(device))
	if err := s.driver.control(ioctlPciScanCards, request); err != nil {
		return nil, fmt.Errorf("scan for %04x:%04x: %w", vendor, device, err)
	}
	// Compared unsigned: on a 32-bit build a large count would be negative
	// as an int and get past the bound.
	count := scanCards
	if reported := u32(request, scanCountOffset); reported < scanCards {
		count = int(reported)
	}
	slots := make([]Slot, 0, count)
	for index := range count {
		at := scanSlotOffset + index*slotSize
		slots = append(slots, Slot{
			Bus:      u32(request, at+0),
			Slot:     u32(request, at+4),
			Function: u32(request, at+8),
		})
	}
	return slots, nil
}

// OpenDevice claims the card in a slot.
func (s *Session) OpenDevice(slot Slot) (*Device, error) {
	// WD_PCI_CARD_INFO: the slot goes in, the driver fills in the memory
	// windows the card exposes and the interrupt it raises.
	info := make([]byte, cardInfoRoom)
	putU32(info, 0, slot.Bus)
	putU32(info, 4, slot.Slot)
	putU32(info, 8, slot.Function)
	// The request says how far the driver may write, which is the structure's
	// own length; the slack past it is there in case one writes further.
	if err := s.driver.control(ioctlPciGetCardInfo, info[:cardInfoSize]); err != nil {
		return nil, fmt.Errorf("read what the card at %s offers: %w", slot, err)
	}
	spaces, err := s.numberTheWindows(slot, info)
	if err != nil {
		return nil, fmt.Errorf("prepare the card at %s: %w", slot, err)
	}

	// With the windows mapped into the kernel as well, so the driver can write
	// to the card itself once this process is gone. See SetupCleanup. A card
	// that cannot be opened that way is opened the way it always was, and is
	// then refused by SetupCleanup rather than here.
	registration, err := s.register(info, true)
	if err != nil {
		registration, err = s.register(info, false)
	}
	if err != nil {
		return nil, fmt.Errorf("open the card at %s: %w", slot, err)
	}

	device := &Device{
		slot: slot, driver: s.driver,
		registration: registration,
		windows:      mappedWindows(registration),
		spaces:       spaces,
		kernel:       kernelWindows(registration),
	}
	for _, space := range spaces {
		if _, mapped := device.windows[space]; !mapped {
			device.Close()
			return nil, fmt.Errorf("open the card at %s: window %d was not mapped into this process", slot, space)
		}
	}
	if command, err := device.ReadConfig(configCommand); err == nil {
		device.mastering = command&configBusMaster != 0
	}
	return device, nil
}

// register sends WD_CARD_REGISTER, which holds the same card as info followed
// by what the driver gives back: the handle, and each window's address in this
// process and, if kernel, in the kernel.
func (s *Session) register(info []byte, kernel bool) ([]byte, error) {
	registration := make([]byte, registerSize)
	copy(registration[registerCardOffset:], info[cardItemsOffset:cardInfoSize])
	items := int(u32(registration, registerCardOffset))
	for index := range items {
		at := registerItemOffset + index*itemSize
		if u32(registration, at) != itemMemory {
			continue
		}
		options := at + itemMemoryOptions
		if kernel {
			putU32(registration, options, u32(registration, options)&^itemDoNotMapKernel)
		} else {
			putU32(registration, options, u32(registration, options)|itemDoNotMapKernel)
		}
	}
	if err := s.driver.control(ioctlCardRegister, registration); err != nil {
		return nil, err
	}
	if u32(registration, registerHandleOffset) == 0 {
		return nil, errors.New("the driver reported success and no handle")
	}
	return registration, nil
}

// mappedWindows reads back where the driver put each of the card's memory
// windows. A window is keyed by the base address register it came from, which
// is what numberTheWindows corrected and what Spaces reports.
func mappedWindows(registration []byte) map[uint32]window {
	found := make(map[uint32]window)
	items := int(u32(registration, registerCardOffset))
	for index := range items {
		at := registerItemOffset + index*itemSize
		if at+itemSize > len(registration) || u32(registration, at) != itemMemory {
			continue
		}
		where := uptr(registration, at+itemMemoryUser)
		bytes := u64(registration, at+itemMemoryBytes)
		// A window longer than an int can count would come out with a
		// negative length on a 32-bit build.
		if where == 0 || bytes == 0 || bytes > uint64(^uint(0)>>1) {
			continue
		}
		found[u32(registration, at+itemMemoryBar)] = atAddress(where, bytes)
	}
	return found
}

// kernelWindows reads back where the driver reaches each memory window, keyed
// as mappedWindows keys them. A window opened without a kernel mapping reads
// zero.
func kernelWindows(registration []byte) map[uint32]uint64 {
	found := make(map[uint32]uint64)
	items := int(u32(registration, registerCardOffset))
	for index := range items {
		at := registerItemOffset + index*itemSize
		if at+itemSize > len(registration) || u32(registration, at) != itemMemory {
			continue
		}
		found[u32(registration, at+itemMemoryBar)] = u64(registration, at+itemMemoryTranslated)
	}
	return found
}

// at is the window a register is in and where it stands inside it. WinDriver
// does not check; a card asked for a register past the end of its window would
// read whatever the next mapping holds.
func (d *Device) at(space uint32, offset uint64, width uint64) (window, int, error) {
	found, mapped := d.windows[space]
	if !mapped {
		return nil, 0, fmt.Errorf("the card at %s has no window %d", d.slot, space)
	}
	if offset+width > uint64(len(found)) {
		return nil, 0, fmt.Errorf("%d:%#x is past the end of window %d, which is %d bytes",
			space, offset, space, len(found))
	}
	return found, int(offset), nil
}

// The card's registers are read and written where they stand: WinDriver maps a
// memory window into the process and its own WDC_ReadAddr32 does no more than
// this either.
//
// The four accessors below are marked so the compiler leaves them alone. A
// register is not memory -- reading one twice can give two answers, and writing
// one is what makes a card act -- so an inlined load could be hoisted out of a
// polling loop and spin on a value that never changes again.

//go:noinline
func loadU32(w window, at int) uint32 { return *(*uint32)(unsafe.Pointer(&w[at])) }

//go:noinline
func storeU32(w window, at int, value uint32) { *(*uint32)(unsafe.Pointer(&w[at])) = value }

//go:noinline
func loadU8(w window, at int) byte { return w[at] }

//go:noinline
func storeU8(w window, at int, value byte) { w[at] = value }

// ReadU32 reads a register. space is an address space number from Spaces.
func (d *Device) ReadU32(space uint32, offset uint64) (uint32, error) {
	found, at, err := d.at(space, offset, 4)
	if err != nil {
		return 0, err
	}
	return loadU32(found, at), nil
}

func (d *Device) WriteU32(space uint32, offset uint64, value uint32) error {
	found, at, err := d.at(space, offset, 4)
	if err != nil {
		return err
	}
	storeU32(found, at, value)
	return nil
}

func (d *Device) ReadU8(space uint32, offset uint64) (byte, error) {
	found, at, err := d.at(space, offset, 1)
	if err != nil {
		return 0, err
	}
	return loadU8(found, at), nil
}

func (d *Device) WriteU8(space uint32, offset uint64, value byte) error {
	found, at, err := d.at(space, offset, 1)
	if err != nil {
		return err
	}
	storeU8(found, at, value)
	return nil
}

// ReadConfig reads a word of the card's PCI configuration space.
func (d *Device) ReadConfig(offset uint32) (uint32, error) {
	return d.driver.readConfig(d.slot, offset)
}

// WriteConfig writes a word of the card's PCI configuration space.
func (d *Device) WriteConfig(offset, value uint32) error {
	return d.driver.writeConfig(d.slot, offset, value)
}

// readConfig and writeConfig are on the driver rather than on a card because
// configuration space is reached by slot: it has to be read before the card is
// opened, to work out which register each window came from.
func (d *driver) readConfig(slot Slot, offset uint32) (uint32, error) {
	var value uint32
	if err := d.configDump(slot, offset, &value, true); err != nil {
		return 0, fmt.Errorf("read configuration %#x of the card at %s: %w", offset, slot, err)
	}
	return value, nil
}

func (d *driver) writeConfig(slot Slot, offset, value uint32) error {
	if err := d.configDump(slot, offset, &value, false); err != nil {
		return fmt.Errorf("write configuration %#x of the card at %s: %w", offset, slot, err)
	}
	return nil
}

// configDump is WD_PCI_CONFIG_DUMP, which carries the four bytes in the
// caller's own memory rather than in the request.
//
// That memory is a word of its own on the heap, pinned, never the caller's
// variable. Its address travels inside the request as a number, which the
// compiler cannot see as a pointer: a variable whose address is taken only for
// that stays on the goroutine's stack, and the stack is moved whenever it grows,
// which any call on the way to DeviceIoControl may make it do. The driver would
// then read the value to write, or write the value read, where the variable used
// to be. KeepAlive does not help; it keeps memory from being collected, not from
// being moved. The heap does not move, and the pin keeps the word alive.
func (d *driver) configDump(slot Slot, offset uint32, value *uint32, read bool) error {
	word := new(uint32)
	var pinner runtime.Pinner
	pinner.Pin(word)
	defer pinner.Unpin()
	*word = *value

	request := make([]byte, configDumpSize)
	putU32(request, 0, slot.Bus)
	putU32(request, 4, slot.Slot)
	putU32(request, 8, slot.Function)
	putUptr(request, configDumpBuffer, uint64(uintptr(unsafe.Pointer(word))))
	putU32(request, configDumpOffset, offset)
	putU32(request, configDumpBytes, 4)
	if read {
		putU32(request, configDumpIsRead, 1)
	}
	if err := d.control(ioctlPciConfigDump, request); err != nil {
		return err
	}
	*value = *word
	return nil
}

// Revision is the card's PCI revision, which tells one build of a board from
// another.
func (d *Device) Revision() (byte, error) {
	value, err := d.ReadConfig(configClassRevision)
	if err != nil {
		return 0, err
	}
	return byte(value), nil
}

// EnableBusMastering lets the card write to host memory. WinDriver does not do
// this itself the way Linux's pci_set_master does, and a card whose direct
// memory access is otherwise perfect never transfers anything until it is on.
func (d *Device) EnableBusMastering() error {
	command, err := d.ReadConfig(configCommand)
	if err != nil {
		return err
	}
	if command&configBusMaster != 0 {
		return nil
	}
	return d.WriteConfig(configCommand, command|configBusMaster)
}

// DisableBusMastering takes the card's right to write to host memory away
// again. Whatever it was in the middle of goes nowhere from then on, which is
// the one thing that can be relied on to stop a card that will not stop when
// asked. It takes no lock, so a card can be stopped by it in a hurry.
func (d *Device) DisableBusMastering() error {
	command, err := d.ReadConfig(configCommand)
	if err != nil {
		return err
	}
	if command&configBusMaster == 0 {
		return nil
	}
	return d.WriteConfig(configCommand, command&^configBusMaster)
}

// Untrusted marks a card one of whose transfers could not be seen to stop. It
// is closed with bus mastering taken away rather than left as it was found.
func (d *Device) Untrusted() {
	d.mutex.Lock()
	d.untrusted = true
	d.mutex.Unlock()
}

// RegisterWrite is one 32-bit register write, by address space number and
// offset as WriteU32 takes them.
type RegisterWrite struct {
	Space  uint32
	Offset uint64
	Value  uint32
}

// SetupCleanup hands the driver register writes to make itself, in order,
// when the card is let go: on Close, and also when the driver handle is closed
// with the card still open, which is what happens when this process dies.
//
// That is what stops a card writing into memory that is no longer this
// process's. Its buffers are unlocked and given back to the system when the
// process goes, and a transfer still running writes on into whatever the
// pages are given to next. The driver makes these writes before it lets go
// of the buffers.
//
// The driver keeps one set per card, so this may be called once. It writes
// through the window's kernel mapping, and a window without one is refused:
// the driver would write to the bare offset instead.
func (d *Device) SetupCleanup(writes []RegisterWrite) error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.closed {
		return fmt.Errorf("the card at %s is closed", d.slot)
	}
	if d.cleanupSet {
		return fmt.Errorf("the card at %s already has its writes for closing", d.slot)
	}
	if len(writes) == 0 {
		return nil
	}

	// The accesses are a pinned allocation of their own, for the same reason
	// as in configDump: their address travels in the request as a number.
	commands := make([]byte, len(writes)*transferSize)
	var pinner runtime.Pinner
	pinner.Pin(&commands[0])
	defer pinner.Unpin()
	for index, write := range writes {
		if _, _, err := d.at(write.Space, write.Offset, 4); err != nil {
			return err
		}
		base := d.kernel[write.Space]
		if base == 0 {
			return fmt.Errorf("the driver did not map window %d of the card at %s into the kernel, "+
				"so it has nowhere to write it from once this process is gone", write.Space, d.slot)
		}
		at := index * transferSize
		binary.LittleEndian.PutUint64(commands[at+transferPort:], base+write.Offset)
		putU32(commands, at+transferCommand, transferWriteMemory32)
		putU32(commands, at+transferBytes, 4)
		putU32(commands, at+transferData, write.Value)
	}

	request := make([]byte, cleanupSize)
	putU32(request, cleanupCard, u32(d.registration, registerHandleOffset))
	putUptr(request, cleanupCommands, uint64(uintptr(unsafe.Pointer(&commands[0]))))
	putU32(request, cleanupCount, uint32(len(writes)))
	putU32(request, cleanupOptions, cleanupForce)
	if err := d.driver.control(ioctlCleanupSetup, request); err != nil {
		return fmt.Errorf("give the driver the writes for closing the card at %s: %w", d.slot, err)
	}
	d.cleanupSet = true
	return nil
}

// Close gives the card back; closing twice is harmless.
func (d *Device) Close() error {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	// A card whose transfers were seen to stop is given back with bus mastering
	// as it was found, so that whatever opens it next finds it as it expects.
	// One that may still be transferring has it taken away: it would write into
	// memory that is about to be someone else's.
	var busMastering error
	if d.untrusted || !d.mastering {
		busMastering = d.DisableBusMastering()
	} else {
		busMastering = d.EnableBusMastering()
	}
	d.windows = nil
	if err := d.driver.control(ioctlCardUnregister, d.registration); err != nil {
		return fmt.Errorf("close the card at %s: %w", d.slot, err)
	}
	if busMastering != nil {
		return fmt.Errorf("close the card at %s: set bus mastering back: %w", d.slot, busMastering)
	}
	return nil
}

// Spaces are the address space numbers of the card's memory windows, in the
// order the card declares them. They are not 0, 1, 2: a space is numbered by
// the base address register it came from, and a 64-bit register takes two of
// the six slots.
func (d *Device) Spaces() []uint32 { return d.spaces }

// numberTheWindows corrects the base address register each memory window says
// it came from, and reports the address space number of each.
//
// WinDriver numbers the windows 0, 1, 2 in the order it found them and the
// driver takes that as a BAR index. On a card whose first window is 64-bit the
// second window is BAR 2, not 1, and the driver maps a nonsense address. The
// real BAR is found by walking configuration space.
func (s *Session) numberTheWindows(slot Slot, info []byte) ([]uint32, error) {
	items := int(u32(info, cardItemsOffset))
	if items < 0 || items > cardItemsMax {
		return nil, fmt.Errorf("the card declared %d resources, and a card may declare %d",
			items, cardItemsMax)
	}
	bars, err := s.driver.memoryBars(slot)
	if err != nil {
		return nil, err
	}

	var spaces []uint32
	for index := range items {
		at := cardItemOffset + index*itemSize
		if u32(info, at) != itemMemory {
			continue
		}
		where := u64(info, at+itemMemoryPhysical)
		bar, known := bars[where]
		if !known {
			// Nothing in configuration space is at this address, so the
			// driver's number is no worse than a guess.
			bar = u32(info, at+itemMemoryBar)
		}
		putU32(info, at+itemMemoryBar, bar)
		spaces = append(spaces, bar)
	}
	return spaces, nil
}

// memoryBars maps each memory window's physical address to the base address
// register it came from. Read by slot rather than through an opened card,
// because this has to be known before the card can be opened.
func (d *driver) memoryBars(slot Slot) (map[uint64]uint32, error) {
	found := make(map[uint64]uint32)
	for index := uint32(0); index < configBars; index++ {
		low, err := d.readConfig(slot, configBar0+4*index)
		if err != nil {
			return nil, err
		}
		if low&barIsIO != 0 {
			continue
		}
		where := uint64(low & barAddrMask)
		if low&barTypeMask == barType64 {
			// The next slot holds the top half rather than a window of its own.
			index++
			if index >= configBars {
				break
			}
			high, err := d.readConfig(slot, configBar0+4*index)
			if err != nil {
				return nil, err
			}
			where |= uint64(high) << 32
			found[where] = index - 1
			continue
		}
		if where != 0 {
			found[where] = index
		}
	}
	return found, nil
}

func u32(b []byte, at int) uint32 { return binary.LittleEndian.Uint32(b[at:]) }

func u64(b []byte, at int) uint64 { return binary.LittleEndian.Uint64(b[at:]) }

func putU32(b []byte, at int, value uint32) { binary.LittleEndian.PutUint32(b[at:], value) }

// uptr and putUptr read and write a field the width of a pointer on this build.
// WinDriver's structures hold both: a kernel address is eight bytes on the
// 32-bit build as well, and a user address is not.
func uptr(b []byte, at int) uint64 {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		return u64(b, at)
	}
	return uint64(u32(b, at))
}

func putUptr(b []byte, at int, value uint64) {
	if unsafe.Sizeof(uintptr(0)) == 8 {
		binary.LittleEndian.PutUint64(b[at:], value)
		return
	}
	putU32(b, at, uint32(value))
}

// atAddress is memory this process did not allocate, seen as bytes: a card's
// register window, or a run the driver allocated and mapped. The slice is laid
// out by hand because there is no pointer to build it from, and the memory is
// not the collector's.
func atAddress(where uint64, bytes uint64) []byte {
	header := struct {
		data     uintptr
		length   int
		capacity int
	}{uintptr(where), int(bytes), int(bytes)}
	return *(*[]byte)(unsafe.Pointer(&header))
}

func cstring(b []byte) string {
	for index, letter := range b {
		if letter == 0 {
			return string(b[:index])
		}
	}
	return string(b)
}
