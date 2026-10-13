// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	channelconfig "github.com/Till0196/coax/pkg/config"
	"github.com/Till0196/coax/pkg/host"
)

// twoTunerDaemon answers as a host carrying a satellite-only PT3 and a cable
// card, which is the arrangement that made a scan miss half of what was
// attached.
func twoTunerDaemon(t *testing.T) *host.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/version":
			_ = json.NewEncoder(writer).Encode(host.Version{Version: "test"})
		case "/boards", "/tuners":
			_ = json.NewEncoder(writer).Encode([]host.Board{
				{Name: "pt3:6-17-0-0", Backend: "pt3", Tuners: []host.Tuner{
					{ID: "pt3:6-17-0-0:0", Deliveries: []string{"ISDB-S"}},
				}},
				{Name: "tbs:abcd", Backend: "tbs", Tuners: []host.Tuner{
					{ID: "tbs:abcd:0", Deliveries: []string{"DVB-C/ANNEX_A", "ISDB-T"}},
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	client, err := host.Dial(t.Context(), host.Options{Address: server.URL, NoStart: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return client
}

// TestAScanWithNoTunerNamedCoversEveryTuner. Taking the first tuner the daemon
// hands out and staying on it left a cable band unscanned on a host that could
// receive it.
func TestAScanWithNoTunerNamedCoversEveryTuner(t *testing.T) {
	client := twoTunerDaemon(t)
	tuners, err := scanTuners(t.Context(), client, "")
	if err != nil {
		t.Fatalf("scanTuners: %v", err)
	}
	want := []string{"pt3:6-17-0-0:0", "tbs:abcd:0"}
	if len(tuners) != len(want) || tuners[0] != want[0] || tuners[1] != want[1] {
		t.Fatalf("tuners = %q, want %q", tuners, want)
	}
}

// TestANamedTunerIsTheOnlyOneUsed: naming one is how a caller says which, and
// the daemon is not asked for a listing it has no use for.
func TestANamedTunerIsTheOnlyOneUsed(t *testing.T) {
	tuners, err := scanTuners(t.Context(), nil, "tbs:abcd:0")
	if err != nil {
		t.Fatalf("scanTuners: %v", err)
	}
	if len(tuners) != 1 || tuners[0] != "tbs:abcd:0" {
		t.Errorf("tuners = %q, want the one named", tuners)
	}
}

func entryFor(name, system string) channelconfig.Entry {
	return channelconfig.Entry{Name: name, Properties: map[string]string{"DELIVERY_SYSTEM": system}}
}

// TestEveryChannelGoesToATunerThatReceivesIt is the point of running over all
// of them: a cable channel belongs to the cable card even when the satellite
// tuner was listed first.
func TestEveryChannelGoesToATunerThatReceivesIt(t *testing.T) {
	client := twoTunerDaemon(t)
	runs := planRuns(client, host.TuneOptions{},
		[]string{"pt3:6-17-0-0:0", "tbs:abcd:0"},
		[]channelconfig.Entry{
			entryFor("BS15_1", "ISDBS"),
			entryFor("C42", "DVBC/ANNEX_A"),
			entryFor("27", "ISDBT"),
			entryFor("BS15_2", "ISDBS"),
		})

	got := map[string][]string{}
	for _, run := range runs {
		for _, entry := range run.entries {
			got[run.tuner] = append(got[run.tuner], entry.Name)
		}
	}
	if names := got["pt3:6-17-0-0:0"]; len(names) != 2 || names[0] != "BS15_1" || names[1] != "BS15_2" {
		t.Errorf("the PT3 got %q, want the two satellite channels", names)
	}
	if names := got["tbs:abcd:0"]; len(names) != 2 || names[0] != "C42" || names[1] != "27" {
		t.Errorf("the TBS card got %q, want the cable and terrestrial channels", names)
	}
}

// TestAChannelNoTunerReceivesIsStillReported: dropping it without a word would
// leave a scan silently short, and asking a tuner costs nothing before it is
// tuned.
func TestAChannelNoTunerReceivesIsStillReported(t *testing.T) {
	client := twoTunerDaemon(t)
	runs := planRuns(client, host.TuneOptions{},
		[]string{"pt3:6-17-0-0:0", "tbs:abcd:0"},
		[]channelconfig.Entry{entryFor("JD5", "DVBS2")})

	total := 0
	for _, run := range runs {
		total += len(run.entries)
	}
	if total != 1 {
		t.Errorf("%d channels planned, want the one that was asked for", total)
	}
}

// stubDiscovery is a client whose boards say what each tuner receives, for the
// covered-systems bookkeeping below.
func satelliteAndCableDaemon(t *testing.T) *host.Client { return twoTunerDaemon(t) }

// TestASystemIsCoveredByWhatWasFoundNotByWhatWasAsked.
//
// The satellite bootstrap answers for a tuner and comes back empty when the
// lead is dead: it reports "no transponder answered with a network table" and
// discovery carries on to the bands it walks. Marking ISDB-S covered on the
// strength of having asked took the whole satellite band away from the tuner
// that could receive it, which is what happened on a host whose first-listed
// tuner had nothing on its satellite input.
func TestASystemIsCoveredByWhatWasFoundNotByWhatWasAsked(t *testing.T) {
	client := satelliteAndCableDaemon(t)
	// remainingSystems is the half of the bookkeeping that can be tested
	// without hardware: a system nothing has been found for is still wanted.
	device := client.Open(host.TuneOptions{Tuner: "pt3:6-17-0-0:0"})
	defer device.Close()

	covered := map[string]bool{}
	if wanted, err := remainingSystems(device, nil, covered); err != nil || len(wanted) != 1 || wanted[0] != "ISDBS" {
		t.Fatalf("a satellite tuner is wanted for %q (%v), want ISDBS alone", wanted, err)
	}
	covered["ISDBS"] = true
	if wanted, err := remainingSystems(device, nil, covered); err != nil || len(wanted) != 0 {
		t.Errorf("a covered system was asked for again: %q (%v)", wanted, err)
	}
}

// TestATunerTheDaemonDoesNotHaveIsSaidSo: a mistyped --tuner used to come back
// from --discover as "no channel candidates matched", which sends the reader to
// the --include pattern.
func TestATunerTheDaemonDoesNotHaveIsSaidSo(t *testing.T) {
	client := satelliteAndCableDaemon(t)
	device := client.Open(host.TuneOptions{Tuner: "px3:p9-9.9:1"})
	defer device.Close()

	wanted, err := remainingSystems(device, nil, map[string]bool{})
	if err == nil {
		t.Fatalf("a tuner the daemon does not have was taken for one carrying %q", wanted)
	}
}

// TestISDBS3IsNotWalkedUnlessItWasNamed: every satellite front end answers yes
// to it, and the walk is two dozen transponders long.
func TestISDBS3IsNotWalkedUnlessItWasNamed(t *testing.T) {
	client := satelliteAndCableDaemon(t)
	device := client.Open(host.TuneOptions{Tuner: "pt3:6-17-0-0:0"})
	defer device.Close()

	all, err := remainingSystems(device, nil, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	for _, system := range all {
		if system == "ISDBS3" {
			t.Fatal("ISDBS3 was walked without being named")
		}
	}
	named, err := remainingSystems(device, []string{"ISDBS3"}, map[string]bool{})
	if err != nil || len(named) != 1 || named[0] != "ISDBS3" {
		t.Errorf("naming it gave %q (%v), want ISDBS3", named, err)
	}
}
