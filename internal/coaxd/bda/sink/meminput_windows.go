// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package sink

import (
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/device"
	"github.com/Till0196/coax/pkg/win32/directshow"
)

// --- IMemInputPin ----------------------------------------------------------

func memQueryInterface(this, riid, ppv uintptr) uintptr {
	return pinQueryInterface(this-memVtblOffset, riid, ppv)
}

func memAddRef(this uintptr) uintptr { return pinAddRef(this - memVtblOffset) }

func memRelease(this uintptr) uintptr { return pinRelease(this - memVtblOffset) }

// memGetAllocator offers none, which tells the upstream pin to bring its own.
// ksproxy has one already: its buffers are the frames the driver fills.
func memGetAllocator(this, ppAllocator uintptr) uintptr {
	if ppAllocator != 0 {
		writePointer(ppAllocator, 0)
	}
	return hrNoAllocator
}

func memNotifyAllocator(this, pAllocator, bReadOnly uintptr) uintptr { return hrOK }

func memGetAllocatorRequirements(this, pProps uintptr) uintptr { return hrNotImpl }

func memReceive(this, pSample uintptr) uintptr {
	defer device.HaltOnPanic()
	if pSample == 0 {
		return hrPointer
	}
	pin := pinFromMem(this)
	sample := (*directshow.IMediaSample)(comPointer(pSample))
	var buffer *byte
	if err := sample.GetPointer(&buffer); err != nil || buffer == nil {
		return hrOK
	}
	length := sample.GetActualDataLength()
	if length <= 0 {
		return hrOK
	}

	pin.mu.Lock()
	defer pin.mu.Unlock()
	if pin.out == nil {
		return hrOK
	}
	data := unsafe.Slice(buffer, int(length))
	if !pin.synced {
		offset := syncOffset(data)
		if offset < 0 {
			// Nothing here looks like a transport packet yet, which is what
			// an unlocked tuner delivers. Waiting costs one frame; writing
			// it would put the reader out of step for the whole recording.
			return hrOK
		}
		data = data[offset:]
		pin.synced = true
	}
	written, err := pin.out.Write(data)
	pin.written += int64(written)
	if err != nil {
		pin.failure = err
		return hrFail
	}
	return hrOK
}

func memReceiveMultiple(this, pSamples, nSamples, nSamplesProcessed uintptr) uintptr {
	defer device.HaltOnPanic()
	result := hrOK
	var done uintptr
	for ; done < nSamples; done++ {
		sample := *(*uintptr)(comPointer(pSamples + done*unsafe.Sizeof(uintptr(0))))
		if result = memReceive(this, sample); result != hrOK {
			break
		}
	}
	if nSamplesProcessed != 0 {
		*(*uint32)(comPointer(nSamplesProcessed)) = uint32(done)
	}
	return result
}

// memReceiveCanBlock answers S_FALSE: a write to the capture file is not
// something the upstream pin has to assume will stall.
func memReceiveCanBlock(this uintptr) uintptr { return hrFalse }
