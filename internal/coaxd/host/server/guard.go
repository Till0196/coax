// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package server

import (
	"mime"
	"net"
	"net/http"
	"strings"

	"github.com/Till0196/coax/internal/coaxd/host/hostapi"
)

// guard keeps web pages off a port. A page open in a browser on this machine
// can send requests to 127.0.0.1 as easily as any program can: a POST with a
// plain-text body needs no permission, and a name that resolves to the
// loopback lets the page read the answers too. None of the clients this daemon
// is for is a web page, so whatever a browser marks as coming from one is
// refused, and so is a request that names a host this port was not reached by.
//
// anyHost is for a port opened to the network on purpose, which is reached by
// whatever name the network gives the machine.
func guard(next http.Handler, anyHost bool) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		// A browser sends Origin with anything a script sends across origins and
		// with every POST; Sec-Fetch-Site says where a request came from, and only
		// "none" -- an address typed or opened by the person -- is not a page.
		if _, sent := request.Header["Origin"]; sent {
			fail(writer, http.StatusForbidden, hostapi.CodeInvalidParameter, "requests from web pages are not served")
			return
		}
		if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "none" {
			fail(writer, http.StatusForbidden, hostapi.CodeInvalidParameter, "requests from web pages are not served")
			return
		}
		if !anyHost && !loopbackHost(request.Host) {
			fail(writer, http.StatusForbidden, hostapi.CodeInvalidParameter,
				"the host "+request.Host+" is not this machine; reach the port as 127.0.0.1 or localhost")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

// loopbackHost reports whether a Host header names this machine by its
// loopback address or as localhost.
func loopbackHost(host string) bool {
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// jsonBody refuses a body that does not say it is JSON. A web page can send a
// plain-text body without asking anyone; it cannot send one marked as JSON
// without the browser asking this server first, which it never agrees to.
func jsonBody(writer http.ResponseWriter, request *http.Request) bool {
	media, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(writer, http.StatusUnsupportedMediaType, hostapi.CodeInvalidParameter,
			"the request body must be sent as application/json")
		return false
	}
	return true
}
