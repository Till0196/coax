// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package native

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"

	"github.com/Till0196/coax/internal/coaxd/host/userlock"
	"github.com/Till0196/coax/internal/coaxd/trace"
)

var (
	pipeKernel       = syscall.NewLazyDLL("kernel32.dll")
	createNamedPipe  = pipeKernel.NewProc("CreateNamedPipeW")
	connectNamedPipe = pipeKernel.NewProc("ConnectNamedPipe")
	createPipeEvent  = pipeKernel.NewProc("CreateEventW")
	setPipeEvent     = pipeKernel.NewProc("SetEvent")
	overlappedResult = pipeKernel.NewProc("GetOverlappedResult")
	pipeServerPID    = pipeKernel.NewProc("GetNamedPipeServerProcessId")
	waitPipeObjects  = pipeKernel.NewProc("WaitForMultipleObjects")
	localFreePipe    = pipeKernel.NewProc("LocalFree")
	pipeSDDL         = syscall.NewLazyDLL("advapi32.dll").NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
)

func hostPipe(key string) string { return `\\.\pipe\coax-bda-` + key }

// eventHandle returns a manual-reset event used for completion or cancellation.
func eventHandle() (syscall.Handle, error) {
	h, _, err := createPipeEvent.Call(0, 1, 0, 0)
	if h == 0 {
		return 0, err
	}
	return syscall.Handle(h), nil
}

// waitHandle waits on kernel objects, including the caller's cancellation.
// Neither the process nor an outstanding I/O handle is closed under the wait.
func waitHandle(ctx context.Context, handle syscall.Handle) error {
	event, err := eventHandle()
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(event)
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { setPipeEvent.Call(uintptr(event)); close(finished) })
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	handles := [2]syscall.Handle{handle, event}
	result, _, callErr := waitPipeObjects.Call(2, uintptr(unsafe.Pointer(&handles[0])), 0, syscall.INFINITE)
	switch result {
	case 0:
		return nil
	case 1:
		return ctx.Err()
	default:
		return callErr
	}
}

// createHostPipe limits access to this user, across logon sessions, and rejects
// remote clients. The first instance also refuses an existing server's name.
func createHostPipe(key string, first bool) (syscall.Handle, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return 0, err
	}
	user, err := token.GetTokenUser()
	token.Close()
	if err != nil {
		return 0, err
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return 0, err
	}
	descriptor, err := syscall.UTF16PtrFromString("D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		return 0, err
	}
	var security uintptr
	ok, _, err := pipeSDDL.Call(uintptr(unsafe.Pointer(descriptor)), 1, uintptr(unsafe.Pointer(&security)), 0)
	if ok == 0 {
		return 0, err
	}
	defer localFreePipe.Call(security)
	attributes := syscall.SecurityAttributes{Length: uint32(unsafe.Sizeof(syscall.SecurityAttributes{})), SecurityDescriptor: security}
	name, err := syscall.UTF16PtrFromString(hostPipe(key))
	if err != nil {
		return 0, err
	}
	flags := uint32(0x00000003 | syscall.FILE_FLAG_OVERLAPPED) // PIPE_ACCESS_DUPLEX
	if first {
		flags |= 0x00080000
	} // FILE_FLAG_FIRST_PIPE_INSTANCE
	h, _, err := createNamedPipe.Call(uintptr(unsafe.Pointer(name)), uintptr(flags), 0x00000008, 255, 4096, 0, 0, uintptr(unsafe.Pointer(&attributes))) // PIPE_REJECT_REMOTE_CLIENTS
	if syscall.Handle(h) == syscall.InvalidHandle {
		return 0, err
	}
	return syscall.Handle(h), nil
}

func acceptHostPipe(ctx context.Context, h syscall.Handle) error {
	event, err := eventHandle()
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(event)
	operation := syscall.Overlapped{HEvent: event}
	ok, _, err := connectNamedPipe.Call(uintptr(h), uintptr(unsafe.Pointer(&operation)))
	if ok != 0 || err == syscall.Errno(535) {
		return nil
	} // ERROR_PIPE_CONNECTED
	if err != syscall.ERROR_IO_PENDING {
		return err
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = syscall.CancelIoEx(h, &operation); close(finished) })
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	if _, err := syscall.WaitForSingleObject(event, syscall.INFINITE); err != nil {
		return err
	}
	var count uint32
	ok, _, err = overlappedResult.Call(uintptr(h), uintptr(unsafe.Pointer(&operation)), uintptr(unsafe.Pointer(&count)), 0)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if ok == 0 {
		return err
	}
	return nil
}

func serveHostPipe(ctx context.Context, key string, state *hostState) error {
	first, err := createHostPipe(key, true)
	if err != nil {
		return err
	}
	go func() {
		h := first
		for {
			if err := acceptHostPipe(ctx, h); err != nil {
				syscall.CloseHandle(h)
				return
			}
			file := os.NewFile(uintptr(h), hostPipe(key))
			go func() {
				client, stop := context.WithCancel(ctx)
				defer stop()
				go func() { _, _ = io.Copy(io.Discard, file); stop() }()
				state.send(client, file)
			}()
			h, err = createHostPipe(key, false)
			if err != nil {
				trace.Printf("BDA host state pipe: %v", err)
				return
			}
		}
	}()
	return nil
}

// waitForEarlierHost reads events from the earlier host and waits for its
// process to release its handles. A driver still in a call remains pending;
// cancellation, a disconnected pipe, or process exit are facts, not timeouts.
func waitForEarlierHost(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if socketKey == "" {
		return nil
	}
	name, err := syscall.UTF16PtrFromString(hostPipe(socketKey))
	if err != nil {
		return err
	}
	h, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			running, check := userlock.Exists(hostLock(socketKey))
			if check != nil {
				return check
			}
			if !running {
				return nil
			}
		}
		return fmt.Errorf("reach the earlier BDA host's state pipe: %w", err)
	}
	var pid uint32
	ok, _, err := pipeServerPID.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	if ok == 0 {
		syscall.CloseHandle(h)
		return err
	}
	process, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, pid)
	if err != nil {
		syscall.CloseHandle(h)
		return err
	}
	defer syscall.CloseHandle(process)
	file := os.NewFile(uintptr(h), hostPipe(socketKey))
	defer file.Close()
	stop := context.AfterFunc(ctx, func() { _ = file.Close() })
	defer stop()
	lines := bufio.NewScanner(file)
	for lines.Scan() {
		state := lines.Text()
		trace.Printf("earlier BDA host: %s", state)
		if state == "closed" {
			return waitHandle(ctx, process)
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// A broken pipe alone does not grant access to hardware. Only the process
	// exiting proves that its handles have gone; the caller may cancel meanwhile.
	return waitHandle(ctx, process)
}
