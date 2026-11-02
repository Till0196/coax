// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package session receives Windows session-ending messages without a console.
package session

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32            = syscall.NewLazyDLL("user32.dll")
	registerClass     = user32.NewProc("RegisterClassExW")
	createWindow      = user32.NewProc("CreateWindowExW")
	destroyWindow     = user32.NewProc("DestroyWindow")
	defaultWindowProc = user32.NewProc("DefWindowProcW")
	getMessage        = user32.NewProc("GetMessageW")
	dispatchMessage   = user32.NewProc("DispatchMessageW")
	postMessage       = user32.NewProc("PostMessageW")
	postQuitMessage   = user32.NewProc("PostQuitMessage")
	getModuleHandle   = syscall.NewLazyDLL("kernel32.dll").NewProc("GetModuleHandleW")
	classOnce         sync.Once
	classError        error
	windows           sync.Map
	callback          = syscall.NewCallback(windowProc)
)

const (
	wmDestroy         = 0x0002
	wmClose           = 0x0010
	wmQueryEndSession = 0x0011
	wmEndSession      = 0x0016
)

type windowClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClassExtra, WindowExtra            int32
	Instance, Icon, Cursor, Background syscall.Handle
	Menu, Name                         *uint16
	SmallIcon                          syscall.Handle
}

type message struct {
	Window         syscall.Handle
	ID             uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
}

type watcher struct {
	stop     func()
	stopping sync.Once
	finished chan struct{}
}

// Watch creates an invisible top-level window, also when no tray icon is used.
// Call finish only after resources have been released: WM_ENDSESSION waits for
// it before replying to Windows. A cancelled session query does not stop work.
func Watch(stop func()) (finish func(), err error) {
	ready := make(chan syscall.Handle, 1)
	exited := make(chan struct{})
	w := &watcher{stop: stop, finished: make(chan struct{})}
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(exited)
		instance, _, _ := getModuleHandle.Call(0)
		name, _ := syscall.UTF16PtrFromString("coax-session")
		classOnce.Do(func() {
			class := windowClass{Size: uint32(unsafe.Sizeof(windowClass{})), Proc: callback, Instance: syscall.Handle(instance), Name: name}
			if atom, _, callErr := registerClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
				classError = fmt.Errorf("register session window: %w", callErr)
			}
		})
		if classError != nil {
			ready <- 0
			return
		}
		// Parent zero makes this a top-level window; message-only windows do
		// not receive the session-ending broadcast.
		h, _, callErr := createWindow.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, 0, 0, instance, 0)
		if h == 0 {
			err = fmt.Errorf("create session window: %w", callErr)
			ready <- 0
			return
		}
		windows.Store(h, w)
		defer windows.Delete(h)
		ready <- syscall.Handle(h)
		var msg message
		for {
			n, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(n) <= 0 {
				return
			}
			dispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	h := <-ready
	if h == 0 {
		<-exited
		if err == nil {
			err = classError
		}
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(w.finished)
			postMessage.Call(uintptr(h), wmClose, 0, 0)
			<-exited
		})
	}, nil
}

func windowProc(h syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmQueryEndSession:
		return 1
	case wmEndSession:
		if wparam != 0 {
			if value, ok := windows.Load(uintptr(h)); ok {
				w := value.(*watcher)
				w.stopping.Do(func() { go w.stop() })
				<-w.finished
			}
		}
		return 0
	case wmClose:
		destroyWindow.Call(uintptr(h))
		return 0
	case wmDestroy:
		postQuitMessage.Call(0)
		return 0
	}
	r, _, _ := defaultWindowProc.Call(uintptr(h), uintptr(msg), wparam, lparam)
	return r
}
