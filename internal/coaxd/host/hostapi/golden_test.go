// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package hostapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// goldenSum is the checksum of testdata/wire.json. The same file sits byte for
// byte in the client's host package with the same checksum in the same test.
// Neither program imports the other, so nothing else catches one side renaming a
// field: changing the shapes means changing this constant on both sides.
const goldenSum = "5e9c2a94055a7fa98923df9b029ae4d6887bd9aa3493a53d5072998cd9798f3a"

// TestTheGoldenFileIsTheOneTheClientHas fails if this copy has drifted from the
// client's, which would make every test below agree with nothing.
func TestTheGoldenFileIsTheOneTheClientHas(t *testing.T) {
	raw, err := os.ReadFile("testdata/wire.json")
	if err != nil {
		t.Fatalf("read the golden file: %v", err)
	}
	sum := sha256.Sum256(raw)
	if got := hex.EncodeToString(sum[:]); got != goldenSum {
		t.Fatalf("testdata/wire.json is %s, want %s: it and the daemon's copy must be the same file", got, goldenSum)
	}
}

// TestGoldenRoundTrip decodes each recorded answer into the shape this program
// declares and encodes it again, which catches a field renamed, retyped, dropped
// or reordered. The client's identical test does the same on its side.
func TestGoldenRoundTrip(t *testing.T) {
	var recorded map[string]json.RawMessage
	raw, err := os.ReadFile("testdata/wire.json")
	if err != nil {
		t.Fatalf("read the golden file: %v", err)
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("the golden file is not JSON: %v", err)
	}

	cases := map[string]any{
		"version":    &Version{},
		"board":      &Board{},
		"tuneResult": &TuneResult{},
		// The same shape from a tuner whose driver names a satellite stream by its
		// place on the transponder. A second sample because the tuner of the one
		// above takes an identifier and reports no places.
		"tuneResultRelative": &TuneResult{},
		"card":               &Card{},
		"cardSession":        &CardSession{},
		"cardRequest":        &CardRequest{},
		"failure":            &Failure{},
	}
	for name, into := range cases {
		t.Run(name, func(t *testing.T) {
			sample, ok := recorded[name]
			if !ok {
				t.Fatalf("the golden file has no %q", name)
			}
			decoder := json.NewDecoder(bytes.NewReader(sample))
			// A field the client expects and this program does not know is a
			// divergence, not something to skip quietly.
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(into); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}

			encoded, err := json.Marshal(into)
			if err != nil {
				t.Fatalf("encode %s: %v", name, err)
			}
			var want bytes.Buffer
			if err := json.Compact(&want, sample); err != nil {
				t.Fatalf("compact %s: %v", name, err)
			}
			if !bytes.Equal(encoded, want.Bytes()) {
				t.Errorf("%s came back different:\n got %s\nwant %s", name, encoded, want.Bytes())
			}
		})
	}
}

// TestGoldenCodesAreTheOnesThisProgramKnows: a code is a string in a field, so
// a renamed or dropped one round-trips perfectly and still leaves every client
// unable to tell a busy tuner from a broken one.
func TestGoldenCodesAreTheOnesThisProgramKnows(t *testing.T) {
	var recorded struct {
		Codes   []string `json:"codes"`
		Reasons []string `json:"reasons"`
	}
	raw, err := os.ReadFile("testdata/wire.json")
	if err != nil {
		t.Fatalf("read the golden file: %v", err)
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("the golden file is not JSON: %v", err)
	}

	codes := []string{
		CodeInvalidParameter, CodeUnknownTuner, CodeTunerBusy, CodeUnsupported,
		CodeTuneFailed, CodeNoLock, CodeDeviceGone, CodeNoCardReader,
		CodeCardError, CodeNoDevices, CodeNoFirmware, CodeNoAccess, CodeInternal,
	}
	if !slices.Equal(codes, recorded.Codes) {
		t.Errorf("this program knows %v, the protocol has %v", codes, recorded.Codes)
	}
	reasons := []string{ReasonDeliverySystem, ReasonOutOfRange, ReasonLNBSupply}
	if !slices.Equal(reasons, recorded.Reasons) {
		t.Errorf("this program knows the reasons %v, the protocol has %v", reasons, recorded.Reasons)
	}
}
