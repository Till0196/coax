// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// Sockets are the files a running daemon listens on.
type Sockets struct {
	// Directory holds them both.
	Directory string
	// HTTP is where tuners and cards are asked for.
	HTTP string
	// Private reports whether the directory keeps other users out. When it does,
	// the sockets need no permissions of their own; when it does not -- the
	// shared /run directory, where a root daemon serves other users' programs --
	// they do.
	Private bool
}

// Environment is what the runtime directory is worked out from, so the rules can
// be tested without setting variables in the test's own process.
type Environment struct {
	GOOS string
	// UID is the user the daemon runs as. It is -1 where the system has no
	// such notion.
	UID int
	// Getenv reads a variable. Nil means the process's own environment.
	Getenv func(string) string
}

// Runtime works out where a daemon's sockets go. /run exists only on Linux and
// only root may write to it; everywhere else the platform's per-user runtime
// directory suits a daemon whose devices are the ones this user may open.
func Runtime(environment Environment) (Sockets, error) {
	getenv := environment.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if override := getenv("COAXD_SOCKET_DIR"); override != "" {
		return At(override, false), nil
	}

	switch environment.GOOS {
	case "windows":
		base := getenv("LOCALAPPDATA")
		if base == "" {
			base = getenv("TEMP")
		}
		if base == "" {
			return Sockets{}, fmt.Errorf("neither LOCALAPPDATA nor TEMP is set, so there is nowhere to put the sockets")
		}
		return At(filepath.Join(base, "coaxd"), true), nil

	case "darwin":
		// Every user's TMPDIR on macOS is already closed to everybody else, so
		// there is nothing to widen or guard.
		base := getenv("TMPDIR")
		if base == "" {
			base = "/tmp"
			return At(filepath.Join(base, "coaxd-"+strconv.Itoa(environment.UID)), true), nil
		}
		return At(filepath.Join(base, "coaxd"), true), nil

	default:
		if environment.UID == 0 {
			// The daemon serves other users' programs, so its sockets live where
			// they can see them.
			return At("/run/coaxd", false), nil
		}
		if base := getenv("XDG_RUNTIME_DIR"); base != "" {
			return At(filepath.Join(base, "coaxd"), true), nil
		}
		return At(filepath.Join(os.TempDir(), "coaxd-"+strconv.Itoa(environment.UID)), true), nil
	}
}

// At names the sockets inside a directory a caller chose.
func At(directory string, private bool) Sockets {
	return Sockets{
		Directory: directory,
		HTTP:      filepath.Join(directory, "http.sock"),
		Private:   private,
	}
}

// DefaultSockets is where this build puts its sockets.
func DefaultSockets() (Sockets, error) {
	return Runtime(Environment{GOOS: runtime.GOOS, UID: os.Getuid()})
}
