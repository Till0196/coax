// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package tray

import (
	"github.com/Till0196/coax/internal/coaxd/build"

	"context"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClass    = user32.NewProc("RegisterClassExW")
	procCreateWindow     = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefWindowProc    = user32.NewProc("DefWindowProcW")
	procGetMessage       = user32.NewProc("GetMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessage  = user32.NewProc("DispatchMessageW")
	procPostMessage      = user32.NewProc("PostMessageW")
	procPostQuit         = user32.NewProc("PostQuitMessage")
	procLoadIcon         = user32.NewProc("LoadIconW")
	procCreatePopupMenu  = user32.NewProc("CreatePopupMenu")
	procDestroyMenu      = user32.NewProc("DestroyMenu")
	procAppendMenu       = user32.NewProc("AppendMenuW")
	procTrackPopupMenu   = user32.NewProc("TrackPopupMenu")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procMessageBox       = user32.NewProc("MessageBoxW")
	procShellNotifyIcon  = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandle  = kernel32.NewProc("GetModuleHandleW")
)

// The Win32 numbers this uses, from winuser.h and shellapi.h.
const (
	wmDestroy    = 0x0002
	wmClose      = 0x0010
	wmCommand    = 0x0111
	wmRightUp    = 0x0205
	wmLeftUp     = 0x0202
	wmTrayIcon   = 0x0400 + 1 // WM_APP + 1, this program's own callback
	wmAskToStop  = 0x0400 + 2 // posted to the loop when the daemon is ending
	nimAdd       = 0x00000000
	nimDelete    = 0x00000002
	nifMessage   = 0x00000001
	nifIcon      = 0x00000002
	nifTip       = 0x00000004
	nifInfo      = 0x00000010
	idiApp       = 32512
	mfString     = 0x00000000
	mfGrayed     = 0x00000001
	mfSeparator  = 0x00000800
	idYes        = 6
	tpmRightlick = 0x0002
	tpmReturnCmd = 0x0100
	cwUseDefault = ^uintptr(0) - 0x7FFFFFFF // 0x80000000 as a signed default
)

// The menu items, numbered so WM_COMMAND can tell them apart. The first is not
// an action -- it says which daemon this is -- and the second is the only
// action: what the daemon is busy with is a question for coax, and this is the
// way out.
const (
	menuWhich = 1
	menuStop  = 2
)

// notifyIconData is NOTIFYICONDATAW at the size every Windows since 2000
// accepts, which buys the 128-character tip.
type notifyIconData struct {
	Size            uint32
	Window          syscall.Handle
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            syscall.Handle
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Timeout         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
}

type windowClass struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   syscall.Handle
	Icon       syscall.Handle
	Cursor     syscall.Handle
	Background syscall.Handle
	MenuName   *uint16
	ClassName  *uint16
	SmallIcon  syscall.Handle
}

type point struct{ X, Y int32 }

type message struct {
	Window  syscall.Handle
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

// Run shows the icon and turns the message loop until the context is done or the
// person chooses Stop. The loop is pinned to its own thread: a window belongs to
// the thread that made it and only that thread may pump its messages, while a
// goroutine moves between threads freely.
func Run(ctx context.Context, options Options) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	window, icon, err := show(options)
	if err != nil {
		// A daemon with no icon is still a daemon, and no icon is often nothing
		// wrong: a session without its own desktop -- a service, a scheduled
		// task, an ssh login -- has no notification area, which Windows reports
		// as an unspecified error. Said once, as a fact rather than a failure.
		options.Report("serving without a notification-area icon; this session has none (" + err.Error() + ")")
		<-ctx.Done()
		return
	}
	defer hide(window, icon)

	// The context ending has to reach a thread inside GetMessage, which only a
	// posted message does.
	go func() {
		<-ctx.Done()
		procPostMessage.Call(uintptr(window), wmAskToStop, 0, 0)
	}()

	pump(window, options)
}

// tray is what the window procedure needs, kept aside because a Win32 callback
// carries no state. One daemon has one icon, so one is enough.
var current Options

func show(options Options) (syscall.Handle, syscall.Handle, error) {
	instance, _, _ := procGetModuleHandle.Call(0)
	name, err := syscall.UTF16PtrFromString("coaxd-tray")
	if err != nil {
		return 0, 0, err
	}
	current = options
	class := windowClass{
		Size:      uint32(unsafe.Sizeof(windowClass{})),
		WndProc:   syscall.NewCallback(windowProc),
		Instance:  syscall.Handle(instance),
		ClassName: name,
	}
	if atom, _, callErr := procRegisterClass.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return 0, 0, callErr
	}
	window, _, callErr := procCreateWindow.Call(0, uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(name)),
		0, 0, 0, 0, 0, 0, 0, instance, 0)
	if window == 0 {
		return 0, 0, callErr
	}
	iconHandle, _, _ := procLoadIcon.Call(0, idiApp)

	data := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Window:          syscall.Handle(window),
		ID:              1,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayIcon,
		Icon:            syscall.Handle(iconHandle),
	}
	putUTF16(data.Tip[:], build.Describe(options.Title, options.Version, options.Commit))
	if ok, _, callErr := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&data))); ok == 0 {
		procDestroyWindow.Call(window)
		return 0, 0, callErr
	}
	return syscall.Handle(window), syscall.Handle(iconHandle), nil
}

func hide(window, icon syscall.Handle) {
	data := notifyIconData{
		Size:   uint32(unsafe.Sizeof(notifyIconData{})),
		Window: window,
		ID:     1,
	}
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	procDestroyWindow.Call(uintptr(window))
}

func pump(window syscall.Handle, options Options) {
	var msg message
	for {
		got, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(got) <= 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

// windowProc answers the two things this window is for: the icon being clicked,
// and the daemon saying it is on its way out.
func windowProc(window syscall.Handle, msg uint32, wparam, lparam uintptr) uintptr {
	switch msg {
	case wmTrayIcon:
		if uint32(lparam) == wmRightUp || uint32(lparam) == wmLeftUp {
			openMenu(window)
		}
		return 0
	case wmAskToStop, wmClose, wmDestroy:
		procPostQuit.Call(0)
		return 0
	case wmCommand:
		if uint16(wparam) == menuStop && current.Stop != nil && confirmed(window) {
			// The orderly stop, the same one a signal starts; the loop ends when
			// the daemon has finished with it.
			current.Stop()
		}
		return 0
	}
	result, _, _ := procDefWindowProc.Call(uintptr(window), uintptr(msg), wparam, lparam)
	return result
}

// openMenu shows what can be done, which is to stop it.
func openMenu(window syscall.Handle) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendItem(menu, mfString|mfGrayed, menuWhich, build.Describe(current.Title, current.Version, current.Commit))
	procAppendMenu.Call(menu, mfSeparator, 0, 0)
	appendItem(menu, mfString, menuStop, "Stop the tuner daemon")

	var where point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&where)))
	// Windows wants the window in front, or the menu will not close on a click
	// elsewhere.
	procSetForeground.Call(uintptr(window))
	chosen, _, _ := procTrackPopupMenu.Call(menu, tpmRightlick|tpmReturnCmd,
		uintptr(where.X), uintptr(where.Y), 0, uintptr(window), 0)
	if chosen != 0 {
		procPostMessage.Call(uintptr(window), wmCommand, chosen, 0)
	}
}

// confirmed asks whether the daemon really is to stop, and says what stopping
// would interrupt. Everything it holds goes with it -- a recording in progress,
// a card session mid-conversation -- and the icon sits beside a clock people
// click at without looking, so when something is attached it says how much,
// warns rather than enquires, and starts with No under the cursor.
func confirmed(window syscall.Handle) bool {
	count := -1
	if current.Busy != nil {
		count = current.Busy()
	}
	text, flags := confirmPrompt(count)
	body, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return false
	}
	title, err := syscall.UTF16PtrFromString("coaxd")
	if err != nil {
		return false
	}
	// The window has to be in front, or the box opens behind whatever was on
	// screen and the click seems to do nothing.
	procSetForeground.Call(uintptr(window))
	answer, _, _ := procMessageBox.Call(uintptr(window),
		uintptr(unsafe.Pointer(body)), uintptr(unsafe.Pointer(title)), flags)
	return answer == idYes
}

func appendItem(menu uintptr, flags uint32, id uint32, text string) {
	wide, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	procAppendMenu.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(wide)))
}

// putUTF16 writes a string into a fixed field, leaving room for the NUL Windows
// reads it up to.
func putUTF16(field []uint16, text string) {
	wide := syscall.StringToUTF16(text)
	if len(wide) > len(field) {
		wide = wide[:len(field)]
		wide[len(wide)-1] = 0
	}
	copy(field, wide)
}
