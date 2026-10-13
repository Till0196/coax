// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

// Package sockdir makes and checks the directory the sockets live in.
//
// It is shared because both ends depend on it: the daemon and `coax pcsc`
// listen there, and `coax` connects there. Where no per-user runtime directory
// is set the directory sits in a temporary directory every user may write to,
// so whoever makes it first owns it. A directory made by somebody else would
// let them hold the lock that keeps the daemon from starting, or put their own
// socket where `coax` connects.
package sockdir
