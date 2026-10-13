// Copyright 2026 Till0196
// SPDX-License-Identifier: Apache-2.0 OR GPL-2.0-or-later

package native

import "fmt"

func errNoSuchTuner(board string, index int) error {
	return fmt.Errorf("%s carries one tuner and there is no tuner %d on it", board, index)
}
