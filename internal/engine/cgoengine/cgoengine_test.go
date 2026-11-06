// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package cgoengine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// A panic in the wake callback must end the process even if a caller above the
// abandoned C frames recovers. Package bui's TestPanicAcrossCFramesExits covers the
// emit callback, which it can reach through the kit.
func TestWakePanicExits(t *testing.T) {
	if os.Getenv("BUI_CGOENGINE_TEST_CHILD") == "1" {
		wakeFunc = func() { panic("boom") }

		func() {
			defer func() { _ = recover() }()

			buiGoWake()
		}()
		fmt.Println("SURVIVED")

		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestWakePanicExits$", "-test.count=1")

	cmd.Env = append(os.Environ(), "BUI_CGOENGINE_TEST_CHILD=1")
	out, err := cmd.CombinedOutput()

	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Errorf("child ended with %v, want exit status 2", err)
	}

	if strings.Contains(string(out), "SURVIVED") {
		t.Errorf("child survived:\n%s", out)
	}

	if !strings.Contains(string(out), "boom") || !strings.Contains(string(out), "goroutine ") {
		t.Errorf("output lacks the panic and a stack:\n%s", out)
	}
}
