// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package host

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestATunerThatListsNothingIsStillTried: Windows BDA offers no way to ask a
// tuner what it receives, and several Linux frontend drivers will not enumerate
// either, so the listing carries nothing for them. Reading that as "it cannot"
// made every channel of a TBS 6205SE come back "unsupported" from a card that
// receives them.
func TestATunerThatListsNothingIsStillTried(t *testing.T) {
	cases := []struct {
		name       string
		deliveries []string
		asked      string
		want       bool
	}{
		{name: "says nothing", deliveries: nil, asked: "ISDBT", want: true},
		{name: "says what it receives", deliveries: []string{"ISDB-T"}, asked: "ISDBT", want: true},
		{name: "says something else", deliveries: []string{"ISDB-S"}, asked: "ISDBT", want: false},
		{name: "spelt differently", deliveries: []string{"DVB-C/ANNEX_A"}, asked: "DVBC/ANNEX_A", want: true},
		// One demodulator receives ISDB-S and ISDB-S3 both, and Linux has no
		// delivery system number for the advanced one, so a front end that
		// locks it still lists ISDB-S. Either name has to answer for the
		// other, or an ISDB-S3 channel is passed over by hardware that receives it.
		{name: "the advanced satellite where only ISDB-S is listed",
			deliveries: []string{"ISDB-S"}, asked: "ISDBS3", want: true},
		{name: "the advanced satellite named as itself",
			deliveries: []string{"ISDBS3"}, asked: "ISDB-S3", want: true},
		{name: "the older satellite on a front end that lists the advanced one",
			deliveries: []string{"ISDBS3"}, asked: "ISDBS", want: true},
		{name: "and neither stands in for anything else",
			deliveries: []string{"ISDBS3"}, asked: "DVBS2", want: false},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/version":
					_ = json.NewEncoder(writer).Encode(Version{Version: "test"})
				case "/boards", "/tuners":
					_ = json.NewEncoder(writer).Encode([]Board{{
						Name:    "bda:abcd",
						Backend: "bda",
						Tuners:  []Tuner{{ID: "bda:abcd:0", Deliveries: one.deliveries}},
					}})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			client, err := Dial(t.Context(), Options{Address: server.URL, NoStart: true})
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			device := client.Open(TuneOptions{})
			supported, err := device.Supports(one.asked)
			if err != nil {
				t.Fatalf("ask about %s: %v", one.asked, err)
			}
			if supported != one.want {
				t.Errorf("asked whether it receives %s with %v listed: got %v, want %v",
					one.asked, one.deliveries, supported, one.want)
			}
		})
	}
}

// TestISDBSAndISDBS3AnswerForEachOtherWhoeverIsAsked pins the substitution the
// two Japanese satellite standards get. Neither the Linux DVB API nor Windows
// BDA can tell them apart; a backend driving the hardware itself does know, but
// reading its silence as a refusal would turn forgetting to list ISDB-S3 on a
// board into a scan that passes over the ISDB-S3 services.
//
// The board is not consulted at all, so the cases below differ in what the
// tuner lists.
func TestISDBSAndISDBS3AnswerForEachOtherWhoeverIsAsked(t *testing.T) {
	for _, test := range []struct {
		what       string
		deliveries []string
		asked      string
	}{
		{"a front end that lists only the older standard", []string{"ISDB-S"}, "ISDBS3"},
		{"and one that lists it beside terrestrial, so a board added without the name still scans",
			[]string{"ISDB-T", "ISDB-S"}, "ISDBS3"},
		{"the advanced standard's hardware receives the older one",
			[]string{"ISDB-S3"}, "ISDBS"},
	} {
		if !carries(Board{}, Tuner{Deliveries: test.deliveries}, test.asked) {
			t.Errorf("%s: listing %v did not answer for %s",
				test.what, test.deliveries, test.asked)
		}
	}
	// What it must not do is answer for something else entirely.
	if carries(Board{}, Tuner{Deliveries: []string{"ISDB-S"}}, "DVBS2") {
		t.Error("a satellite front end answered for a delivery system it does not carry")
	}
}

// TestSupportsNarrowsToTheTunerBeingHeld. A Device holds one tuner for the
// rest of its life, so once it has one the machine's other tuners cannot
// answer for it: a scan on a satellite-only PT3 walked a whole cable band on
// the strength of a TBS card in the same host, failing every channel.
func TestSupportsNarrowsToTheTunerBeingHeld(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/version":
			_ = json.NewEncoder(writer).Encode(Version{Version: "test"})
		case "/boards", "/tuners":
			_ = json.NewEncoder(writer).Encode([]Board{
				{Name: "pt3:6-17-0-0", Backend: "pt3", Tuners: []Tuner{
					{ID: "pt3:6-17-0-0:0", Deliveries: []string{"ISDB-S"}},
				}},
				{Name: "tbs:abcd", Backend: "tbs", Tuners: []Tuner{
					{ID: "tbs:abcd:0", Deliveries: []string{"DVB-C/ANNEX_A", "ISDB-T"}},
				}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := Dial(t.Context(), Options{Address: server.URL, NoStart: true})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	device := client.Open(TuneOptions{})

	// Nothing is held yet, so the question is about the machine.
	if supported, err := device.Supports("DVBC/ANNEX_A"); err != nil || !supported {
		t.Fatalf("before tuning: supported=%v err=%v, want true", supported, err)
	}

	// The daemon handed over the satellite tuner, which does not receive it.
	device.id = "pt3:6-17-0-0:0"
	if supported, err := device.Supports("DVBC/ANNEX_A"); err != nil || supported {
		t.Errorf("while holding the PT3: supported=%v err=%v, want false", supported, err)
	}
	if supported, err := device.Supports("ISDBS"); err != nil || !supported {
		t.Errorf("while holding the PT3: ISDB-S supported=%v err=%v, want true", supported, err)
	}
}
