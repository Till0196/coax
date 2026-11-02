// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import (
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestHostSessionChild(t *testing.T) {
	if os.Getenv("COAX_SESSION_HOST_CHILD") != "1" {
		return
	}
	if err := ServeHost(os.Stdin, os.Stdout, nil); err != nil {
		t.Fatal(err)
	}
}

func TestHostSessionEndClosesOpenInput(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestHostSessionChild$")
	cmd.Env = append(os.Environ(), "COAX_SESSION_HOST_CHILD=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x8}
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	user32 := syscall.NewLazyDLL("user32.dll")
	getPID := user32.NewProc("GetWindowThreadProcessId")
	getClass := user32.NewProc("GetClassNameW")
	var window uintptr
	callback := syscall.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		getPID.Call(h, uintptr(unsafe.Pointer(&pid)))
		if int(pid) != cmd.Process.Pid {
			return 1
		}
		var name [64]uint16
		getClass.Call(h, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
		if syscall.UTF16ToString(name[:]) == "coax-session" {
			window = h
			return 0
		}
		return 1
	})
	deadline := time.Now().Add(5 * time.Second)
	for window == 0 && time.Now().Before(deadline) {
		user32.NewProc("EnumWindows").Call(callback, 0)
		if window == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if window == 0 {
		t.Fatal("host did not create a session window")
	}
	if ok, _, err := user32.NewProc("SendMessageTimeoutW").Call(window, 0x0016, 1, 0, 2, 3000, 0); ok == 0 {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("host stayed alive with its RPC input open")
	}
}
