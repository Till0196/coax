// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Till0196/coax/internal/sockdir"
)

type Options struct {
	// Address is a Unix socket path or a URL. Empty is the usual place for this
	// platform.
	Address string
	// Command is the daemon to start when none is running; empty looks for it.
	Command string
	// NoStart forbids starting one, for a caller that would rather be told there
	// is none.
	NoStart bool
	// FirmwareDir is passed on to a daemon this program starts.
	FirmwareDir string
	// Report receives a line about anything worth saying.
	Report func(string)
	// Agent names this program in the User-Agent of every request, so that a
	// daemon's peer can be told from another. Empty leaves Go's own.
	Agent string
}

type Client struct {
	// http carries the lease-bearing tune and stream requests. It deliberately
	// has one connection, because the daemon ties a tuner to that connection.
	http *http.Client
	// control carries requests which do not take or reuse a lease. Keeping it
	// apart lets status be read while the lease connection is streaming.
	control *http.Client
	base    string
	options Options
}

// Dial reaches the daemon, starting one if none is listening. A process that
// has been created is not yet a daemon -- it may still be opening its sockets,
// and it may yet fail -- so what is waited for is an answered request.
func Dial(ctx context.Context, options Options) (*Client, error) {
	if options.Report == nil {
		options.Report = func(string) {}
	}
	address := options.Address
	if address == "" {
		address = DefaultAddress()
	}

	if isSocket(address) {
		if err := checkSocketPath(address); err != nil {
			return nil, err
		}
	}

	client, err := connect(ctx, address, options)
	if err == nil {
		return client, nil
	}
	if options.NoStart || !isSocket(address) {
		return nil, err
	}

	// What any daemon started here says on the way down, kept for the error
	// below: without it the caller is told only that nothing answered.
	heard := &complaints{}
	if err := start(options, address, heard); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(startDeadline)
	retry := time.Now().Add(startRetry)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(startPoll):
		}
		client, err := connect(ctx, address, options)
		if err == nil {
			return client, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("started a tuner daemon but it did not answer on %s within %s: %w%s",
				address, startDeadline, err, heard.quoted())
		}
		// Started again every so often: a daemon shutting down still holds its claim
		// on the socket until it has given the tuners up, so one started in that
		// moment finds the claim taken and exits at once.
		if time.Now().After(retry) {
			retry = time.Now().Add(startRetry)
			if err := start(options, address, heard); err != nil {
				return nil, err
			}
		}
	}
}

// startPoll and startDeadline are how a newly started daemon is waited for:
// often enough not to be noticeable, and bounded so a daemon which cannot start
// does not hang this program. startRetry is how often another is started
// meanwhile.
//
// A daemon of its own answers in milliseconds, whatever state the hardware is
// in. What the deadline has to cover is one already on its way down, holding
// its claim until it has given every tuner back to the vendor driver -- the
// hardware's pace, not ours.
const (
	startPoll     = 50 * time.Millisecond
	startRetry    = time.Second
	startDeadline = 20 * time.Second
)

// named puts the program's name on every request a transport carries.
func named(agent string, transport *http.Transport) http.RoundTripper {
	if agent == "" {
		return transport
	}
	return agentTransport{agent: agent, next: transport}
}

type agentTransport struct {
	agent string
	next  *http.Transport
}

func (t agentTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// A round tripper is not to change the request it was given.
	request = request.Clone(request.Context())
	request.Header.Set("User-Agent", t.agent)
	return t.next.RoundTrip(request)
}

// CloseIdleConnections is passed on: closing a connection is how a lease or a
// card is given back, and http.Client only reaches a transport that has it.
func (t agentTransport) CloseIdleConnections() {
	t.next.CloseIdleConnections()
}

// connect opens a connection and asks the daemon for its version.
func connect(ctx context.Context, address string, options Options) (*Client, error) {
	client := &Client{options: options}
	if isSocket(address) {
		socket := address
		// A socket in a directory somebody else could have made may be theirs.
		if err := sockdir.Check(filepath.Dir(socket)); err != nil {
			return nil, err
		}
		client.base = "http://coaxd"
		newClient := func() *http.Client {
			return &http.Client{Transport: named(options.Agent, &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var dialer net.Dialer
					return dialer.DialContext(ctx, "unix", socket)
				},
				// One connection, because a lease belongs to the connection
				// it was taken on: a scan working down a list has to stay on
				// the one holding its tuner.
				MaxConnsPerHost:     1,
				MaxIdleConnsPerHost: 1,
			})}
		}
		client.http = newClient()
		client.control = newClient()
	} else {
		base := address
		if !strings.Contains(base, "://") {
			base = "http://" + base
		}
		if _, err := url.Parse(base); err != nil {
			return nil, fmt.Errorf("%q is neither a socket path nor an address: %w", address, err)
		}
		client.base = strings.TrimSuffix(base, "/")
		newClient := func() *http.Client {
			return &http.Client{Transport: named(options.Agent, &http.Transport{
				MaxConnsPerHost:     1,
				MaxIdleConnsPerHost: 1,
			})}
		}
		client.http = newClient()
		client.control = newClient()
	}

	// Asked once, so a daemon that is not answering is found here rather than
	// in the middle of a recording.
	var version Version
	if err := client.get(ctx, "/version", nil, &version); err != nil {
		return nil, err
	}
	return client, nil
}

func isSocket(address string) bool {
	return strings.HasPrefix(address, "/") ||
		strings.HasPrefix(address, "./") ||
		strings.HasPrefix(address, `\\`) ||
		(len(address) > 2 && address[1] == ':' && (address[2] == '\\' || address[2] == '/'))
}

// start launches a daemon and does not wait for it: the daemon outlives this
// program, and whether it worked is settled by connecting to it. Its standard
// error is read into heard all the same, because a daemon which fails to start
// says why once, on its way out.
func start(options Options, address string, heard *complaints) error {
	command, err := locate(options.Command)
	if err != nil {
		return err
	}
	arguments := []string{"--socket", address}
	if options.FirmwareDir != "" {
		arguments = append(arguments, "--firmware-dir", options.FirmwareDir)
	}

	daemon := exec.Command(command, arguments...)
	daemon.Stdin = nil
	daemon.Stdout = nil
	daemon.Stderr = nil
	// A pipe of this program's own rather than one exec would make: exec ties
	// closing its pipes to Wait, which is never called here. A daemon that
	// outlives this program is then left writing into a pipe with nobody at the
	// other end.
	if reader, writer, err := os.Pipe(); err == nil {
		daemon.Stderr = writer
		defer func() {
			// While this program's copy of the writing end is open, the read
			// below could never reach the end.
			_ = writer.Close()
			go func() {
				defer reader.Close()
				_, _ = io.Copy(heard, reader)
			}()
		}()
	}
	Detach(daemon)
	if err := daemon.Start(); err != nil {
		return fmt.Errorf("start %s: %w", command, err)
	}
	options.Report("started " + command)
	_ = daemon.Process.Release()
	return nil
}

// complaints is what daemons started here wrote to their standard error. It is
// bounded and keeps the beginning rather than the end: a daemon that cannot
// start says so in its first line, and retries would push that out with
// repetitions of it.
type complaints struct {
	mutex sync.Mutex
	text  []byte
}

// complaintRoom is enough for the handful of lines a daemon manages before it
// gives up, and little enough that one chattering into the pipe for hours costs
// nothing.
const complaintRoom = 4 << 10

func (c *complaints) Write(written []byte) (int, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if room := complaintRoom - len(c.text); room > 0 {
		c.text = append(c.text, written[:min(room, len(written))]...)
	}
	// The whole write is reported as taken so the copy into this runs to the
	// end of the pipe instead of stopping at the brim.
	return len(written), nil
}

// quoted renders what was heard for the end of an error, indented so a daemon's
// lines are not mistaken for this program's. A line is shown once however many
// times it was said.
func (c *complaints) quoted() string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	said := strings.TrimSpace(string(c.text))
	if said == "" {
		return ""
	}
	var quoted strings.Builder
	quoted.WriteString("\nthe daemon said:")
	seen := make(map[string]bool)
	for line := range strings.SplitSeq(said, "\n") {
		line = strings.TrimRight(line, "\r")
		if seen[line] {
			continue
		}
		seen[line] = true
		quoted.WriteString("\n  " + line)
	}
	return quoted.String()
}

// locate finds the daemon: beside this program first, because that is where the
// two are unpacked together, and only then on the path -- a program shipped as a
// pair should not find some other installed version of its other half.
func locate(named string) (string, error) {
	if named != "" {
		return named, nil
	}
	if named := os.Getenv("COAXD"); named != "" {
		return named, nil
	}
	name := "coaxd"
	if runtime.GOOS == "windows" {
		name = "coaxd.exe"
	}
	if self, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(self), name)
		if info, err := os.Stat(beside); err == nil && !info.IsDir() {
			return beside, nil
		}
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("no tuner daemon is running and %s could not be found beside this program or on the path; set COAXD to where it is", name)
	}
	return found, nil
}

// leaseGet asks on the one connection that owns this client's tuner, at the
// priority the caller's work is worth: taking a tuner is the one thing a
// priority bears on, so it goes on the requests that take one and nowhere else.
func (c *Client) leaseGet(ctx context.Context, path string, query url.Values, priority int, into any) error {
	return c.getWith(c.http, ctx, path, query, header(priority), into)
}

func (c *Client) get(ctx context.Context, path string, query url.Values, into any) error {
	return c.getWith(c.control, ctx, path, query, nil, into)
}

// header renders what a lease-taking request carries beside its query. Nothing
// is sent for the priority every request has had since before priorities
// existed, so a daemon too old to know the header never sees one.
func header(priority int) http.Header {
	if priority == DefaultPriority {
		return nil
	}
	return http.Header{PriorityHeader: []string{strconv.Itoa(priority)}}
}

func (c *Client) getWith(client *http.Client, ctx context.Context, path string, query url.Values, headers http.Header, into any) error {
	target := c.base + path
	if len(query) != 0 {
		target += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	for name, values := range headers {
		request.Header[name] = values
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decode(response, into)
}

func (c *Client) post(ctx context.Context, path string, body, into any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.control.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	return decode(response, into)
}

// decode reads an answer, turning a failure into the problem it describes so a
// caller can branch on the code rather than the text.
func decode(response *http.Response, into any) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return err
	}
	if response.StatusCode >= 400 {
		return problemFrom(response, body)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("the tuner daemon sent something this program could not read: %w", err)
	}
	return nil
}

// problemFrom reads a failure, falling back to the status line when the body
// is not one.
func problemFrom(response *http.Response, body []byte) error {
	var failure Failure
	if err := json.Unmarshal(body, &failure); err == nil && failure.Error.Code != "" {
		if failure.Status != nil {
			return &FailureError{Problem: failure.Error, Status: failure.Status}
		}
		return &FailureError{Problem: failure.Error}
	}
	return fmt.Errorf("the tuner daemon answered %s", response.Status)
}

const maxAnswer = 8 << 20

// FailureError is a problem the daemon reported, with whatever it measured
// first.
type FailureError struct {
	Problem Problem
	Status  *TuneResult
}

func (f *FailureError) Error() string { return f.Problem.Error() }

func (f *FailureError) Code() string { return f.Problem.Code }

func DefaultAddress() string {
	if named := os.Getenv("COAX_HOST"); named != "" {
		return named
	}
	if named := os.Getenv("COAXD_SOCKET"); named != "" {
		return named
	}
	return filepath.Join(runtimeDir(), "http.sock")
}

// runtimeDir mirrors where the daemon puts its sockets: the two work it out the
// same way rather than one telling the other, because a client has to find a
// daemon it has not spoken to yet.
func runtimeDir() string {
	if named := os.Getenv("COAXD_SOCKET_DIR"); named != "" {
		return named
	}
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base = os.Getenv("TEMP")
		}
		return filepath.Join(base, "coaxd")
	case "darwin":
		if base := os.Getenv("TMPDIR"); base != "" {
			return filepath.Join(base, "coaxd")
		}
		return filepath.Join(os.TempDir(), "coaxd-"+strconv.Itoa(os.Getuid()))
	default:
		if os.Getuid() == 0 {
			return "/run/coaxd"
		}
		if base := os.Getenv("XDG_RUNTIME_DIR"); base != "" {
			return filepath.Join(base, "coaxd")
		}
		return filepath.Join(os.TempDir(), "coaxd-"+strconv.Itoa(os.Getuid()))
	}
}
