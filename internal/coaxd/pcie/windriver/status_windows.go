// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

//go:build windows

package windriver

import "fmt"

// WinDriver v12.0.0 status codes, as Stat2Str words them. Transcribed rather
// than called so nothing has to read a C string back through a raw address; an
// unknown code is reported as a bare number.
var statusText = map[uintptr]string{
	0x00000000: "success",
	0x20000000: "error",
	0x20000001: "invalid handle",
	0x20000002: "invalid pipe number",
	0x20000003: "conflict between read and write operations",
	0x20000004: "packet size is zero",
	0x20000005: "insufficient resources",
	0x20000006: "unknown pipe type",
	0x20000007: "internal system error",
	0x20000008: "data mismatch",
	0x20000009: "no valid license",
	0x2000000a: "function not implemented",
	0x2000000b: "kernel plug-in failure",
	0x2000000c: "failed enabling interrupt",
	0x2000000d: "interrupt not enabled",
	0x2000000e: "resource overlap",
	0x2000000f: "device not found",
	0x20000010: "wrong unique ID",
	0x20000011: "operation already done",
	0x20000012: "USB descriptor error",
	0x20000013: "set configuration operation failed",
	0x20000014: "cannot obtain PDO",
	0x20000015: "timeout expired",
	0x20000016: "IRP operation canceled",
	0x20000017: "failed to map in user space",
	0x20000018: "failed to map in kernel space",
	0x20000019: "no resources on the device",
	0x2000001a: "no events",
	0x2000001b: "invalid parameter",
	0x2000001c: "incorrect WinDriver version installed",
	0x2000001d: "try again",
	0x2000001e: "failed to open WinDriver",
	0x2000001f: "received an invalid IOCTL",
	0x20000020: "operation failed",
	0x20000021: "received an invalid 32-bit IOCTL",
	0x20000022: "no room to add handle",
	0x20000023: "driver not installed",
}

func describe(status uintptr) string {
	if text, known := statusText[status]; known {
		return fmt.Sprintf("%s (%#x)", text, status)
	}
	return fmt.Sprintf("status %#x", status)
}
