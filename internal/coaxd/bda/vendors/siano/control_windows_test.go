// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package siano

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestRegistrationUsesTheCallersPointerWidth(t *testing.T) {
	word := unsafe.Sizeof(uintptr(0))
	if unsafe.Sizeof(registration{}) != word+8 || unsafe.Offsetof(registration{}.client) != word ||
		unsafe.Offsetof(registration{}.dataType) != word+4 {
		t.Fatal("registration does not match the driver ABI")
	}
}

// A pipe read gives the I/O helper a real pending kernel request without tuner
// hardware, covering completion and cancellation with the same OVERLAPPED.
func TestPendingControlCompletionAndCancellation(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(fmt.Sprintf("complete=%v", complete), func(t *testing.T) {
			path, err := syscall.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\coax-siano-%d-%d`, os.Getpid(), time.Now().UnixNano()))
			if err != nil {
				t.Fatal(err)
			}
			handle, _, err := kernel32.NewProc("CreateNamedPipeW").Call(uintptr(unsafe.Pointer(path)),
				3|syscall.FILE_FLAG_OVERLAPPED, 0, 1, 512, 512, 0, 0)
			if handle == uintptr(syscall.InvalidHandle) {
				t.Fatal(err)
			}
			pipe := syscall.Handle(handle)
			defer syscall.CloseHandle(pipe)
			client, err := syscall.CreateFile(path, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
				0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer syscall.CloseHandle(client)
			data := make([]byte, 1)
			operation, err := startIO(pipe, data, func(_ *byte, returned *uint32, overlap *syscall.Overlapped) error {
				return syscall.ReadFile(pipe, data, returned, overlap)
			})
			if err != nil {
				t.Fatal(err)
			}
			defer operation.close()
			if !operation.pending {
				t.Fatal("read did not remain pending")
			}
			if complete {
				var written uint32
				dataToWrite := []byte{0x5a}
				write, err := startIO(client, dataToWrite, func(_ *byte, returned *uint32, overlap *syscall.Overlapped) error {
					return syscall.WriteFile(client, dataToWrite, returned, overlap)
				})
				if err != nil {
					t.Fatal(err)
				}
				defer write.close()
				written, err = write.wait(1000)
				if err != nil || written != 1 {
					t.Fatalf("write: count=%d, err=%v", written, err)
				}
				if count, err := operation.wait(1000); err != nil || operation.pending || count != 1 || data[0] != 0x5a {
					t.Fatalf("completed read: pending=%v, count=%d, data=%x, err=%v", operation.pending, count, data, err)
				}
			} else if _, err := operation.wait(0); err == nil || !operation.pending {
				t.Fatalf("pending read: pending=%v, err=%v", operation.pending, err)
			}
			operation.close()
			if operation.pending || operation.overlap.HEvent != 0 {
				t.Fatal("pending I/O or event survived close")
			}
		})
	}
}
