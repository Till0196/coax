// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package daemon

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/host/pool"
)

// TestRuntimeDirPerPlatform pins where the sockets go: a client must find them
// unaided, and a daemon must create them without being root.
func TestRuntimeDirPerPlatform(t *testing.T) {
	cases := []struct {
		name        string
		environment Environment
		want        string
		private     bool
	}{
		{
			name:        "root on Linux serves everybody, so the sockets are where everybody can see them",
			environment: Environment{GOOS: "linux", UID: 0, Getenv: func(string) string { return "" }},
			want:        "/run/coaxd",
			private:     false,
		},
		{
			name: "an ordinary user on Linux gets the runtime directory the session already has",
			environment: Environment{GOOS: "linux", UID: 1000, Getenv: env(map[string]string{
				"XDG_RUNTIME_DIR": "/run/user/1000",
			})},
			want:    "/run/user/1000/coaxd",
			private: true,
		},
		{
			name:        "without one, a directory of this user's own",
			environment: Environment{GOOS: "linux", UID: 1000, Getenv: func(string) string { return "" }},
			want:        filepath.Join(os.TempDir(), "coaxd-1000"),
			private:     true,
		},
		{
			name: "macOS has a per-user temporary directory already",
			environment: Environment{GOOS: "darwin", UID: 501, Getenv: env(map[string]string{
				"TMPDIR": "/var/folders/ab/T/",
			})},
			want:    "/var/folders/ab/T/coaxd",
			private: true,
		},
		{
			name: "Windows keeps them with the user's application data",
			environment: Environment{GOOS: "windows", UID: -1, Getenv: env(map[string]string{
				"LOCALAPPDATA": `C:\Users\person\AppData\Local`,
			})},
			want:    filepath.Join(`C:\Users\person\AppData\Local`, "coaxd"),
			private: true,
		},
		{
			name: "an explicit directory wins everywhere",
			environment: Environment{GOOS: "linux", UID: 0, Getenv: env(map[string]string{
				"COAXD_SOCKET_DIR": "/somewhere/else",
			})},
			want:    "/somewhere/else",
			private: false,
		},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			sockets, err := Runtime(one.environment)
			if err != nil {
				t.Fatalf("work out the runtime directory: %v", err)
			}
			if sockets.Directory != one.want {
				t.Errorf("chose %q, want %q", sockets.Directory, one.want)
			}
			if sockets.Private != one.private {
				t.Errorf("said private=%v, want %v", sockets.Private, one.private)
			}
			if filepath.Dir(sockets.HTTP) != sockets.Directory {
				t.Errorf("put the socket outside the directory: %q", sockets.HTTP)
			}
		})
	}
}

func env(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// TestTheClaimDecidesWhoIsTheDaemon: the claim, not the socket, excludes a
// second daemon, and releasing it lets the next one in.
func TestTheClaimDecidesWhoIsTheDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.sock")

	claim, err := TakeClaim(path, true)
	if err != nil {
		t.Fatalf("the first daemon could not claim: %v", err)
	}
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := TakeClaim(path, true); err == nil {
			t.Fatal("a second daemon claimed a machine another one has")
		}
	}

	listener, err := Listen(path, true)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	if !Running(path) {
		t.Error("a socket being served does not look like it is running")
	}

	// Giving it up must let the next daemon in, with nothing to clean up.
	_ = listener.Close()
	if err := claim.Close(); err != nil {
		t.Fatalf("release: %v", err)
	}
	next, err := TakeClaim(path, true)
	if err != nil {
		t.Fatalf("the next daemon could not claim a machine nobody holds: %v", err)
	}
	_ = next.Close()
}

// TestTheSocketOfALiveDaemonSurvivesAnotherStarting is the race that made the
// socket unfit to be the lock: a second daemon used to remove the path and bind
// its own, leaving the first on an unlinked socket, holding tuners and reachable
// by nobody. The claim is taken first now, so the second never gets that far.
func TestTheSocketOfALiveDaemonSurvivesAnotherStarting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.sock")

	claim, err := TakeClaim(path, true)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	defer claim.Close()
	first, err := Listen(path, true)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer first.Close()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	for attempt := 0; attempt < 5; attempt++ {
		if _, err := TakeClaim(path, true); err == nil {
			t.Fatal("a second daemon started alongside the first")
		}
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the socket of the running daemon was taken away: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Error("the socket was replaced, so the running daemon is listening on nothing")
	}
	if !Running(path) {
		t.Error("the first daemon stopped answering")
	}
}

// TestASocketLeftBehindIsCleanedUp covers a killed daemon: the file remains but
// nothing answers, and a new daemon must be able to take over.
func TestASocketLeftBehindIsCleanedUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "http.sock")

	dead, err := Listen(path, true)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Closing the listener without removing the file is what a kill leaves.
	_ = dead.Close()
	_ = os.WriteFile(path, nil, 0o600)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the test could not leave a socket behind: %v", err)
	}

	again, err := Listen(path, true)
	if err != nil {
		t.Fatalf("a new daemon could not take over a socket nobody was serving: %v", err)
	}
	defer again.Close()
	if !Running(path) {
		t.Error("the new daemon is not answering")
	}
}

func TestPrivateSocketsKeepOthersOut(t *testing.T) {
	if os.Getuid() == -1 {
		t.Skip("this platform has no permission bits to check")
	}
	directory := t.TempDir()

	private := filepath.Join(directory, "private.sock")
	listener, err := Listen(private, true)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	info, err := os.Stat(private)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("a socket in a directory of this user's own is %o, want 600", mode)
	}

	shared := filepath.Join(directory, "shared.sock")
	other, err := Listen(shared, false)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer other.Close()
	info, err = os.Stat(shared)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o666 {
		t.Errorf("a socket in a shared directory is %o, want 666: its clients are run by other users", mode)
	}
}

func TestPublicAddressIsCheckedBeforeItIsOpened(t *testing.T) {
	cases := []struct {
		address     string
		allowRemote bool
		ok          bool
	}{
		{"127.0.0.1:40772", false, true},
		{"[::1]:40772", false, true},
		{":40772", false, false},
		{"0.0.0.0:40772", false, false},
		{"192.168.1.10:40772", false, false},
		{"0.0.0.0:40772", true, true},
		{"nonsense", false, false},
	}
	for _, one := range cases {
		err := checkPublic(one.address, one.allowRemote)
		if one.ok && err != nil {
			t.Errorf("%q with allow-remote=%v was refused: %v", one.address, one.allowRemote, err)
		}
		if !one.ok && err == nil {
			t.Errorf("%q with allow-remote=%v was accepted; it would serve beyond this machine", one.address, one.allowRemote)
		}
	}
}

// TestASocketPathTooLongIsRefusedClearly: the refusal names the path and the way
// out, where the kernel's EINVAL reads as "invalid argument" and says nothing
// about sun_path.
func TestASocketPathTooLongIsRefusedClearly(t *testing.T) {
	directory := t.TempDir()
	long := filepath.Join(directory, strings.Repeat("d", maxSocketPath), "http.sock")
	_, err := Listen(long, true)
	if err == nil {
		t.Fatal("accepted a socket path longer than the system allows")
	}
	for _, want := range []string{"characters", "--socket-dir"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}
	// A path that fits must not be caught by the same check.
	if err := checkSocketPath(filepath.Join(directory, "http.sock")); err != nil {
		t.Errorf("a path that fits was refused: %v", err)
	}
}

// TestPublicClientsAreCounted: the idle rule stops the daemon once nothing is
// attached, and a client on the --http port is attached. Left out of the count,
// as it was, a daemon serving a stream over the port and nothing over the
// socket went quiet three times running and stopped in the middle of it.
func TestPublicClientsAreCounted(t *testing.T) {
	supervisor := pool.New(registry{}, pool.Options{})
	defer supervisor.Close()

	public, listener, err := listenPublic(supervisor, nil, Options{
		Public: "127.0.0.1:0",
		Report: func(string) {},
	})
	if err != nil {
		t.Fatalf("open the port: %v", err)
	}
	defer listener.Close()
	go func() { _ = public.Serve(listener) }()

	if got := public.connections(); got != 0 {
		t.Errorf("counted %d before anything connected", got)
	}

	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + listener.Addr().String() + "/version")
	if err != nil {
		t.Fatalf("GET /version: %v", err)
	}
	// Read to the end before closing: a body left unread is a connection the
	// transport drops instead of keeping, which is the thing being counted.
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()

	// The connection is counted from the moment it is accepted, which the
	// answer says has happened; the bookkeeping runs on the server's own
	// goroutine, so it is waited for rather than assumed.
	deadline := time.Now().Add(time.Second)
	for public.connections() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := public.connections(); got == 0 {
		t.Error("a client on the port is not counted; the idle rule would stop the daemon under it")
	}
}

// A daemon that was not asked for a port has nothing to count there, and the
// zero hosted must say so rather than panicking on its way to the idle rule.
func TestNoPortMeansNothingToCount(t *testing.T) {
	public, listener, err := listenPublic(nil, nil, Options{})
	if err != nil {
		t.Fatalf("no port asked for: %v", err)
	}
	if listener != nil {
		t.Fatal("opened a listener without --http")
	}
	if got := public.connections(); got != 0 {
		t.Errorf("counted %d clients on a port that was never opened", got)
	}
}
