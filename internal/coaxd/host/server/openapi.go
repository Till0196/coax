// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package server is the daemon's HTTP interface.
//
// The description of that interface is generated from this package and the hostapi package
// beside it, and the prose below is where the whole-interface part of it is written. See
// hostapi.Specification.
//
// The generator is pinned to a release candidate as the only version that writes OpenAPI
// 3.1; its predecessor writes Swagger 2.0. What it wrote was checked against the 3.1
// specification by a validator that reads 3.1 -- go-swagger cannot.
//
//	@title			coaxd
//	@version		1.0.0
//	@description	The tuner daemon's interface. One process owns the hardware and hands out what it receives; this is how everything else asks.
//	@description	This number is the description's own: its last part moves when something is added that an existing client can ignore, and its first when a shape an existing client reads changes out from under it. It is not the daemon's version, which is what GET /version reports.
//	@description	A daemon that does not know a parameter refuses the whole request rather than ignoring it, so a client sends only what is described here.
//	@license.name	GPL-2.0-or-later
//	@tag.name		meta
//	@tag.description	What this daemon is.
//	@tag.name		tuners
//	@tag.description	What is attached, and what it is doing.
//	@tag.name		receiving
//	@tag.description	Tuning, and the transport stream that comes of it.
//	@tag.name		cards
//	@tag.description	The card slots. Served wherever this daemon listens.
//
//go:generate go run github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5 init --v3.1 --generalInfo openapi.go --dir .,../hostapi --output ../hostapi --outputTypes json --parseInternal
package server
