// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
	"github.com/Till0196/coax/pkg/winscard"
)

type statusCardSource struct{ fakeCards }

func (c *statusCardSource) Readers() ([]CardInfo, error) {
	return []CardInfo{{Reader: "test reader"}}, nil
}

func TestCardFailurePreservesPCSCStatusInHTTPDetail(t *testing.T) {
	server := New(nil, Options{Cards: &statusCardSource{}})
	for _, code := range []winscard.Code{
		winscard.ErrNoSmartcard, winscard.ErrNotTransacted, winscard.ErrReaderUnavailable,
		winscard.ErrResetCard, winscard.ErrRemovedCard,
	} {
		t.Run(code.String(), func(t *testing.T) {
			writer := httptest.NewRecorder()
			server.failCard(writer, "test reader", fmt.Errorf("reader operation: %w", code))
			if writer.Code != http.StatusBadGateway {
				t.Fatalf("HTTP status = %d", writer.Code)
			}
			var failure hostapi.Failure
			if err := json.Unmarshal(writer.Body.Bytes(), &failure); err != nil {
				t.Fatal(err)
			}
			if failure.Error.Code != hostapi.CodeCardError || failure.Error.Reason != fmt.Sprintf("scard:%08X", uint32(code)) {
				t.Fatalf("PC/SC status was lost: %+v", failure.Error)
			}
		})
	}
	if got := cardProblem(errors.New("transport failed")).Reason; got != "" {
		t.Fatalf("untyped error acquired a PC/SC status: %q", got)
	}
}
