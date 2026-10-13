// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"net/http"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// handleOpenAPI hands over the description of this interface.
//
//	@Summary		This interface, described
//	@Description	The OpenAPI 3.1 document for everything here, generated from the handlers and the shapes they answer with.
//	@Tags			meta
//	@Produce		json
//	@Success		200	{object}	object	"the OpenAPI document"
//	@Router			/openapi.json [get]
func (s *Server) handleOpenAPI(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	// The document does not change while the program runs, and a client that
	// has read it once has no reason to read it again.
	writer.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = writer.Write(hostapi.Specification())
}
