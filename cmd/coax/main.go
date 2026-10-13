// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Till0196/coax/pkg/cli"
)

func main() {
	if err := cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "coax:", err)
		os.Exit(1)
	}
}
