// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package userlock

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateBoundaryDescriptor = kernel32.NewProc("CreateBoundaryDescriptorW")
	procAddSIDToBoundary         = kernel32.NewProc("AddSIDToBoundaryDescriptor")
	procCreatePrivateNamespace   = kernel32.NewProc("CreatePrivateNamespaceW")
	procOpenPrivateNamespace     = kernel32.NewProc("OpenPrivateNamespaceW")
	procDeleteBoundaryDescriptor = kernel32.NewProc("DeleteBoundaryDescriptor")
	procCreateMutex              = kernel32.NewProc("CreateMutexW")
)

// errAlreadyExists is what CreateMutexW and CreatePrivateNamespaceW leave
// behind when the object was there before.
const errAlreadyExists = syscall.Errno(183) // ERROR_ALREADY_EXISTS

// namespaceAlias is the prefix the mutex names are written under.
const namespaceAlias = "coax"

// Create makes the named mutex, in a private namespace bounded by this user's
// SID, and reports whether it was there already. Nothing is written to disk,
// and the kernel takes the mutex away with the last handle to it, however the
// processes holding it end.
//
// The namespace is what keeps another user out: a name in the global namespace
// can be made first by anybody, while only a process carrying this user's SID
// can make or open this one. A private namespace is not a session's, so a
// process started from a remote shell in session 0 and one started by a
// viewer's software in theirs find the same mutex.
//
// Holding is having the mutex exist rather than owning it: a Windows mutex is
// owned by a thread, and goroutines move between threads.
func Create(name string) (handle syscall.Handle, existed bool, err error) {
	if _, err := privateNamespace(); err != nil {
		return 0, false, err
	}
	full, err := syscall.UTF16PtrFromString(namespaceAlias + `\` + name)
	if err != nil {
		return 0, false, err
	}
	created, _, callErr := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(full)))
	if created == 0 {
		return 0, false, fmt.Errorf("create the mutex %s: %w", name, callErr)
	}
	return syscall.Handle(created), callErr == errAlreadyExists, nil
}

// Exists reports whether another process holds the named mutex.
func Exists(name string) (bool, error) {
	handle, existed, err := Create(name)
	if err != nil {
		return false, err
	}
	_ = syscall.CloseHandle(handle)
	return existed, nil
}

var (
	namespaceOnce   sync.Once
	namespaceHandle uintptr
	namespaceErr    error
)

// privateNamespace makes this user's namespace, or opens it where another
// process made it first. A process may have one namespace of an alias open, so
// it is made once and kept for the life of the process.
func privateNamespace() (uintptr, error) {
	namespaceOnce.Do(func() { namespaceHandle, namespaceErr = openNamespace() })
	return namespaceHandle, namespaceErr
}

func openNamespace() (uintptr, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return 0, fmt.Errorf("open this process's token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return 0, fmt.Errorf("read this process's user: %w", err)
	}
	boundaryName, err := syscall.UTF16PtrFromString(namespaceAlias)
	if err != nil {
		return 0, err
	}
	boundary, _, callErr := procCreateBoundaryDescriptor.Call(uintptr(unsafe.Pointer(boundaryName)), 0)
	if boundary == 0 {
		return 0, fmt.Errorf("create a boundary descriptor: %w", callErr)
	}
	// The descriptor only describes the namespace, which keeps a copy. Read when
	// it is freed: adding a SID moves it.
	defer func() { procDeleteBoundaryDescriptor.Call(boundary) }()
	if ok, _, callErr := procAddSIDToBoundary.Call(uintptr(unsafe.Pointer(&boundary)),
		uintptr(unsafe.Pointer(user.User.Sid))); ok == 0 {
		return 0, fmt.Errorf("add this user to the boundary: %w", callErr)
	}
	alias, err := syscall.UTF16PtrFromString(namespaceAlias)
	if err != nil {
		return 0, err
	}
	namespace, _, callErr := procCreatePrivateNamespace.Call(0, boundary, uintptr(unsafe.Pointer(alias)))
	if namespace != 0 {
		return namespace, nil
	}
	if callErr != errAlreadyExists {
		return 0, fmt.Errorf("create the private namespace: %w", callErr)
	}
	namespace, _, callErr = procOpenPrivateNamespace.Call(boundary, uintptr(unsafe.Pointer(alias)))
	if namespace == 0 {
		return 0, fmt.Errorf("open the private namespace: %w", callErr)
	}
	return namespace, nil
}
