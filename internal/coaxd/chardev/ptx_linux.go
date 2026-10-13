// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build linux

package chardev

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// System is one of the transmission standards a px4_drv character device can be
// put into, from enum ptx_system_type.
//
// A device that can do both starts out in neither, and PTX_SET_CHANNEL reads
// its frequency differently depending on which it is in, so the mode is always
// set first rather than left to the driver's guess.
type System uint32

const (
	SystemUnspecified System = 0x00000000
	SystemISDBT       System = 0x00000010
	SystemISDBS       System = 0x00000020
)

// Name is the delivery system as the rest of the program spells it.
func (system System) Name() string {
	switch system {
	case SystemISDBT:
		return "ISDB-T"
	case SystemISDBS:
		return "ISDB-S"
	default:
		return ""
	}
}

// LNB voltages PTX_ENABLE_LNB_POWER accepts. The driver takes a level rather
// than a voltage and turns it into one itself.
const (
	LNBOff = 0
	LNB11V = 1
	LNB15V = 2
)

// ptxFreq matches struct ptx_freq: the channel number, and a second field whose
// meaning depends on the standard. For ISDB-S it names the stream inside the
// multiplex; for ISDB-T the driver adds it to the frequency in kilohertz.
type ptxFreq struct {
	Number int32
	Slot   int32
}

const ptxMagic = 0x8d

const (
	iocNone  = uintptr(0)
	iocWrite = uintptr(1)
	iocRead  = uintptr(2)
)

// ioctlRequest is the asm-generic encoding Linux uses on every architecture this
// program builds for: dir:2, size:14, type:8, nr:8.
func ioctlRequest(direction, ioctlType, number, size uintptr) uintptr {
	return direction<<30 | size<<16 | ioctlType<<8 | number
}

var (
	setChannelRequest = ioctlRequest(iocWrite, ptxMagic, 0x01, unsafe.Sizeof(ptxFreq{}))
	startRequest      = ioctlRequest(iocNone, ptxMagic, 0x02, 0)
	stopRequest       = ioctlRequest(iocNone, ptxMagic, 0x03, 0)
	// PTX_GET_CNR is declared over "int *" rather than over int, so the size in the
	// request is a pointer's. The driver writes four bytes through it regardless.
	cnrRequest        = ioctlRequest(iocRead, ptxMagic, 0x04, unsafe.Sizeof(uintptr(0)))
	enableLNBRequest  = ioctlRequest(iocWrite, ptxMagic, 0x05, 4)
	disableLNBRequest = ioctlRequest(iocNone, ptxMagic, 0x06, 0)
	systemModeRequest = ioctlRequest(iocWrite, ptxMagic, 0x0b, 4)
)

// PT3 status codes, from the STATUS enum in pt3_com.h. The PT3 driver answers
// PTX_SET_CHANNEL with the negative of one of these rather than an errno, so
// what arrives as a system error number must not be read as one: 256 is the
// driver saying the demodulator never reported a valid channel.
const (
	PT3StatusGeneralError   = 0x100
	PT3StatusInvalidParam   = 0x102
	PT3StatusI2CError       = 0x308
	PT3StatusTunerIsSleep   = 0x309
	PT3StatusPLLOutOfRange  = 0x30a
	PT3StatusPLLLockTimeout = 0x30b
)

// Device is one open px4_drv character device, which is one tuner. Opening it
// is how it is claimed: the driver allows a single open per node and answers
// EALREADY to a second.
type Device struct {
	file *os.File
	path string
}

// Open claims a character device.
func Open(path string) (*Device, error) {
	// No O_NONBLOCK: the driver has no poll method and ignores the flag. Reads
	// block, and are unblocked by stopping the stream. See Stream.
	//
	// The open is held off from signals like every other call into these drivers:
	// the PT3 driver wakes the tuner here and waits a hundred milliseconds for it
	// in one interruptible sleep. Cut short, the node is open on a tuner still
	// asleep.
	var file *os.File
	err := whileTheDriverWaits(func() error {
		var err error
		file, err = os.OpenFile(path, os.O_RDONLY, 0)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Device{file: file, path: path}, nil
}

// Path is the device node this was opened from.
func (device *Device) Path() string { return device.path }

// Close releases the device, held off from signals for the same reason the open
// is: the PT3 driver puts the tuner back to sleep and waits fifty milliseconds
// for it, interruptibly, and a release cut short leaves the tuner between the
// two states.
func (device *Device) Close() error {
	return whileTheDriverWaits(device.file.Close)
}

// SetSystem puts the device into one of the transmission standards. The driver
// refuses one the tuner cannot receive, which makes this the way to ask what a
// node is: no ioctl reports the capability.
func (device *Device) SetSystem(system System) error {
	if err := ioctlValue(device.file, systemModeRequest, uintptr(system)); err != nil {
		return fmt.Errorf("PTX_SET_SYSTEM_MODE: %w", err)
	}
	return nil
}

// ErrNoLock reports that the demodulator did not lock onto the channel. The
// driver polls for three seconds inside PTX_SET_CHANNEL and answers EAGAIN when
// the lock does not come, which is not a failure of the call.
var ErrNoLock = fmt.Errorf("the demodulator did not lock")

// SetChannel selects a channel by the driver's own channel number, and waits
// for the demodulator to lock.
func (device *Device) SetChannel(number, slot int) error {
	frequency := ptxFreq{Number: int32(number), Slot: int32(slot)}
	err := ioctlPointer(device.file, setChannelRequest, unsafe.Pointer(&frequency))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EAGAIN):
		return ErrNoLock
	default:
		return fmt.Errorf("PTX_SET_CHANNEL: %w", err)
	}
}

// StartStreaming tells the device to begin filling its ring buffer.
func (device *Device) StartStreaming() error {
	if err := ioctlValue(device.file, startRequest, 0); err != nil {
		return fmt.Errorf("PTX_START_STREAMING: %w", err)
	}
	return nil
}

// StopStreaming stops the capture and wakes anything blocked in a read, which
// is the half that matters: the driver's read has no timeout and no poll.
func (device *Device) StopStreaming() error {
	if err := ioctlValue(device.file, stopRequest, 0); err != nil {
		return fmt.Errorf("PTX_STOP_STREAMING: %w", err)
	}
	return nil
}

// CNRRaw reads the carrier-to-noise measurement as a raw demodulator counter
// rather than decibels. Every board the driver supports answers on the
// TC90522's scale, so one pair of curves turns all of them into a ratio. See
// tc90522.CNRFromRaw*.
func (device *Device) CNRRaw() (uint32, error) {
	var reading uint32
	if err := ioctlPointer(device.file, cnrRequest, unsafe.Pointer(&reading)); err != nil {
		return 0, fmt.Errorf("PTX_GET_CNR: %w", err)
	}
	return reading, nil
}

// SetLNBPower raises or drops the supply on the aerial lead. Dropping always
// goes through the disable call: on the PT3 driver, enabling with a level of
// nothing means "whatever the module was loaded with", which need not be off.
func (device *Device) SetLNBPower(level int) error {
	if level == LNBOff {
		if err := ioctlValue(device.file, disableLNBRequest, 0); err != nil {
			return fmt.Errorf("PTX_DISABLE_LNB_POWER: %w", err)
		}
		return nil
	}
	if err := ioctlValue(device.file, enableLNBRequest, uintptr(level)); err != nil {
		return fmt.Errorf("PTX_ENABLE_LNB_POWER: %w", err)
	}
	return nil
}

// Read takes transport stream bytes from the ring buffer, blocking until the
// buffer it is given is full, the stream is stopped, or a signal arrives.
func (device *Device) Read(buffer []byte) (int, error) {
	var read int
	err := whileTheDriverWaits(func() error {
		var err error
		read, err = device.file.Read(buffer)
		return err
	})
	return read, err
}

// ioctlValue runs an ioctl whose argument is the value itself. Two of these are
// declared _IOW over an int and yet read the argument as an immediate; passing a
// pointer comes back EINVAL, which is why the two forms are kept apart.
func ioctlValue(file *os.File, request uintptr, value uintptr) error {
	return ioctlRun(file, func(descriptor uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, descriptor, request, value)
		return errno
	})
}

// ioctlPointer runs an ioctl whose argument is a pointer to a structure.
//
// The pointer is turned into a number only inside the system call's own
// argument list, which is the one place the compiler keeps what it points at
// where it is. Converted any earlier, it is just a number on its way through the
// closure, the thread lock and the signal mask: a structure on the goroutine's
// stack can be moved in between when the stack grows, and the driver then reads
// a stale channel number from where the structure used to be, or writes the CNR
// into stack memory that is no longer this call's.
func ioctlPointer(file *os.File, request uintptr, value unsafe.Pointer) error {
	return ioctlRun(file, func(descriptor uintptr) syscall.Errno {
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, descriptor, request, uintptr(value))
		return errno
	})
}

// ioctlRun issues one ioctl, retrying an interrupted call. The interruption is
// almost always this program's own doing: the Go runtime preempts goroutines
// with SIGURG.
func ioctlRun(file *os.File, ioctl func(descriptor uintptr) syscall.Errno) error {
	// The descriptor is borrowed through the file rather than read out of it,
	// so it stays this file's for the length of the call: a Close arriving
	// meanwhile closes it only once the call is done, and a call after Close is
	// refused rather than run on whatever the next open took that number for.
	conn, err := file.SyscallConn()
	if err != nil {
		return err
	}
	err = whileTheDriverWaits(func() error {
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
	})
	runtime.KeepAlive(file)
	return err
}

// whileTheDriverWaits runs a call into one of these drivers with the runtime's
// own signals held off, and reports what the call reported.
//
// Retrying EINTR is not enough, because these drivers do not answer EINTR.
// Every wait inside them is a loop around schedule_timeout_interruptible(),
// which returns immediately while a signal is pending: a loop meant to wait six
// hundred milliseconds runs its twenty turns in microseconds and gives up,
// reporting a channel that would not lock or a read of nothing.
//
// The Go runtime sends SIGURG to preempt a goroutine, and its monitor sends one
// to any thread in a system call for more than a few tens of microseconds.
// Measured on a PT3, tuning through the daemon failed about one time in three
// where the same sequence from a C program succeeded twenty times out of
// twenty.
//
// A blocked signal does not count as pending to the kernel, so this is enough
// and the signal is delivered on the way out. The thread is locked because the
// mask belongs to the thread rather than the goroutine.
func whileTheDriverWaits(call func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	held := heldDuringDriverCalls
	var previous uint64
	if err := sigprocmask(sigBlock, &held, &previous); err != nil {
		// Nothing was masked, so there is nothing to put back.
		return call()
	}
	defer func() { _ = sigprocmask(sigSetMask, &previous, nil) }()
	return call()
}

// heldDuringDriverCalls is every signal held off for the duration of a call
// into one of these drivers, which is every signal that may be held off at all.
//
// SIGURG is the one that matters, but nothing about these drivers is specific
// to the preemption signal: a window resize, a SIGHUP asking the daemon to
// reload or a profile being taken breaks a tune in the same way and just as
// silently.
//
// The synchronous ones are left alone: SIGSEGV, SIGBUS, SIGFPE, SIGILL,
// SIGTRAP, SIGABRT and SIGSYS are raised by the running instruction, and
// blocking one makes the kernel reset the handler and kill the process. SIGKILL
// and SIGSTOP cannot be blocked and are left out to say so.
//
// The mask belongs to this thread, so a SIGTERM arriving mid-read is still
// taken by one of the others, and new threads start from the process's original
// mask.
var heldDuringDriverCalls = ^uint64(0) &^ (signalBit(syscall.SIGSEGV) |
	signalBit(syscall.SIGBUS) |
	signalBit(syscall.SIGFPE) |
	signalBit(syscall.SIGILL) |
	signalBit(syscall.SIGTRAP) |
	signalBit(syscall.SIGABRT) |
	signalBit(syscall.SIGSYS) |
	signalBit(syscall.SIGKILL) |
	signalBit(syscall.SIGSTOP))

// The two ways of changing a thread's signal mask that are wanted here, as the
// kernel's numbers rather than the C library's names.
const (
	sigBlock   = 0
	sigSetMask = 2
)

func signalBit(signal syscall.Signal) uint64 { return 1 << (uint(signal) - 1) }

// sigprocmask changes the calling thread's signal mask, as the raw system call
// rather than pthread_sigmask. RawSyscall is right because changing a mask does
// not block.
func sigprocmask(how int, set, previous *uint64) error {
	var previousArgument unsafe.Pointer
	if previous != nil {
		previousArgument = unsafe.Pointer(previous)
	}
	_, _, errno := syscall.RawSyscall6(syscall.SYS_RT_SIGPROCMASK,
		uintptr(how), uintptr(unsafe.Pointer(set)), uintptr(previousArgument),
		unsafe.Sizeof(*set), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}
