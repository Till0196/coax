// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"fmt"
	"hash/fnv"
	"io"
	"strings"
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

// claim takes a mutex named after the socket, in a private namespace bounded
// by this user's SID. Nothing is written to disk, and the kernel takes the
// mutex away with the last handle to it, however this process ends.
//
// The namespace is what keeps another user out: a name in the global namespace
// can be made first by anybody, which would keep this daemon from starting,
// while only a process carrying this user's SID can make or open this one. A
// private namespace is not a session's, so a daemon started from a remote
// shell in session 0 and one started by a viewer's television software in
// theirs find the same mutex for the same socket.
//
// Holding is having the mutex exist rather than owning it: a Windows mutex is
// owned by a thread, and goroutines move between threads.
func claim(path string) (io.Closer, bool, error) {
	if _, err := privateNamespace(); err != nil {
		return nil, false, err
	}
	name, err := syscall.UTF16PtrFromString(namespaceAlias + `\coaxd-` + socketHash(path))
	if err != nil {
		return nil, false, err
	}
	handle, _, callErr := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, false, fmt.Errorf("create the daemon's mutex: %w", callErr)
	}
	if callErr == errAlreadyExists {
		_ = syscall.CloseHandle(syscall.Handle(handle))
		return nil, false, nil
	}
	return mutexHandle(handle), true, nil
}

// mutexHandle is the held mutex. Closing it is letting go.
type mutexHandle syscall.Handle

func (handle mutexHandle) Close() error { return syscall.CloseHandle(syscall.Handle(handle)) }

// socketHash names the mutex after the socket. A hash, because a path holds
// characters a kernel object's name may not; folded to lower case, because two
// spellings of one path on Windows are one socket.
func socketHash(path string) string {
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(strings.ToLower(path)))
	return fmt.Sprintf("%016x", sum.Sum64())
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
