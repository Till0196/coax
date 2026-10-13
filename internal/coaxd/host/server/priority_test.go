// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// terrestrial is the one channel these tests tune; the test board has a single
// terrestrial tuner, so two clients asking for it are two clients wanting the
// same piece of hardware.
const terrestrial = "/tune?system=ISDBT&freq=473142857&lock_timeout=0s"

// asking sends a request at a priority, on a connection of its own so that the
// lease belongs to it and to nothing else. The client is handed back because
// closing it is what gives the tuner up.
func asking(t *testing.T, url, priority string) (*http.Client, *http.Response, []byte) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		MaxConnsPerHost:     1,
		MaxIdleConnsPerHost: 1,
		DisableKeepAlives:   false,
	}}
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	if priority != "" {
		request.Header.Set(hostapi.PriorityHeader, priority)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the answer: %v", err)
	}
	return client, response, body
}

// TestTheHeaderDecidesWhoKeepsTheTuner walks the whole contract over HTTP: two
// clients want the one terrestrial tuner, and the header alone settles it.
func TestTheHeaderDecidesWhoKeepsTheTuner(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	// A viewer that says nothing is worth the default, and takes it because
	// nothing else holds it.
	viewer, answer, body := asking(t, listener.URL+terrestrial, "")
	defer viewer.CloseIdleConnections()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the first request answered %d: %s", answer.StatusCode, body)
	}

	// Somebody at the same rank is refused, which is the rule that was here
	// before priorities were.
	equal, answer, _ := asking(t, listener.URL+terrestrial, "0")
	equal.CloseIdleConnections()
	if answer.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("an equal rank answered %d, want the tuner refused", answer.StatusCode)
	}

	// Somebody below is refused too.
	below, answer, _ := asking(t, listener.URL+terrestrial, "-1")
	below.CloseIdleConnections()
	if answer.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("a lower rank answered %d, want the tuner refused", answer.StatusCode)
	}

	// And a recorder above takes it.
	recorder, answer, body := asking(t, listener.URL+terrestrial, "5")
	defer recorder.CloseIdleConnections()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("a higher rank answered %d: %s", answer.StatusCode, body)
	}
}

// TestAListingSaysWhatRankHoldsATuner: an operator asking why their recording
// could not start can see who has the tuner and at what.
func TestAListingSaysWhatRankHoldsATuner(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	holder, answer, body := asking(t, listener.URL+terrestrial, "5")
	defer holder.CloseIdleConnections()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("take the tuner: %d %s", answer.StatusCode, body)
	}
	var tuned hostapi.TuneResult
	if err := json.Unmarshal(body, &tuned); err != nil {
		t.Fatalf("read the tuning: %v", err)
	}

	_, answer, body = asking(t, listener.URL+"/tuners", "")
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("list the tuners: %d", answer.StatusCode)
	}
	var boards []hostapi.Board
	if err := json.Unmarshal(body, &boards); err != nil {
		t.Fatalf("read the listing: %v", err)
	}
	for _, board := range boards {
		for _, one := range board.Tuners {
			switch {
			case one.ID == tuned.Tuner:
				if one.Priority == nil {
					t.Fatal("the tuner in use reports no priority")
				}
				if *one.Priority != 5 {
					t.Errorf("the tuner in use is held at %d, want 5", *one.Priority)
				}
			case one.Leased:
				t.Errorf("%s is reported leased and nothing took it", one.ID)
			case one.Priority != nil:
				// Zero is a real priority, so a free tuner reporting one
				// would read as work nobody can see holding it.
				t.Errorf("%s is free and reports a priority of %d", one.ID, *one.Priority)
			}
		}
	}
}

// TestAStreamIsToldWhatRankItGot: the answer says which tuner and at what, so a
// client that sent nothing knows what defends it rather than having to assume.
func TestAStreamIsToldWhatRankItGot(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	client := &http.Client{Timeout: 10 * time.Second}
	request, err := http.NewRequest(http.MethodGet,
		listener.URL+"/stream?system=ISDBT&freq=473142857&bytes=1880", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	request.Header.Set(hostapi.PriorityHeader, "3")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("open the stream: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("the stream answered %d", response.StatusCode)
	}
	if got := response.Header.Get(hostapi.PriorityHeader); got != "3" {
		t.Errorf("the stream says it is held at %q, want 3", got)
	}
	_, _ = io.Copy(io.Discard, response.Body)
}

// TestAPriorityThatIsNotANumberIsRefused: taken as zero, a header the daemon
// could not read would quietly leave a recording defenceless.
func TestAPriorityThatIsNotANumberIsRefused(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	for _, path := range []string{terrestrial, "/stream?system=ISDBT&freq=473142857"} {
		client, answer, body := asking(t, listener.URL+path, "high")
		client.CloseIdleConnections()
		if answer.StatusCode != http.StatusBadRequest {
			t.Errorf("%s answered %d to an unreadable priority, want 400: %s", path, answer.StatusCode, body)
			continue
		}
		var failure hostapi.Failure
		if err := json.Unmarshal(body, &failure); err != nil {
			t.Errorf("%s: the refusal is not a problem: %v", path, err)
			continue
		}
		if failure.Error.Code != hostapi.CodeInvalidParameter {
			t.Errorf("%s refused with %q, want %q", path, failure.Error.Code, hostapi.CodeInvalidParameter)
		}
	}
}

// TestARunningStreamIsCutWhenSomethingOutranksIt is what the whole feature is
// for: a viewer is watching, a recording starts, and the one tuner goes to the
// recording. The viewer's response ends rather than hanging, since a stream of
// transport packets has no way to say anything else once it has begun.
func TestARunningStreamIsCutWhenSomethingOutranksIt(t *testing.T) {
	listener, _, _ := newTestServer(t, nil)

	viewer := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1}}
	defer viewer.CloseIdleConnections()
	watching, err := viewer.Get(listener.URL + "/stream?system=ISDBT&freq=473142857")
	if err != nil {
		t.Fatalf("start watching: %v", err)
	}
	defer watching.Body.Close()
	if watching.StatusCode != http.StatusOK {
		t.Fatalf("the viewer's stream answered %d", watching.StatusCode)
	}
	// Read enough that the stream is really running before anything takes it.
	if _, err := io.ReadFull(watching.Body, make([]byte, 188*8)); err != nil {
		t.Fatalf("read the viewer's stream: %v", err)
	}

	recorder, answer, body := asking(t, listener.URL+terrestrial, "10")
	defer recorder.CloseIdleConnections()
	if answer.StatusCode != http.StatusOK {
		t.Fatalf("the recording could not take the tuner: %d %s", answer.StatusCode, body)
	}

	// The viewer's body ends. It is drained rather than merely waited on,
	// because what the daemon had already written is still on its way.
	ended := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, watching.Body)
		ended <- err
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the viewer's stream is still running after its tuner was taken")
	}
}
