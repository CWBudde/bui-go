// SPDX-License-Identifier: GPL-2.0-or-later

//go:build cgo && linux && amd64

package cgoengine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
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

// The kit defaults to X11 windows unless BUI_WINDOW_BACKEND was set, and Go's copy of the
// environment keeps what it had.
func TestWindowBackendDefault(t *testing.T) {
	if want, ok := os.LookupEnv("BUI_CGOENGINE_TEST_BACKEND"); ok {
		if got := windowBackend(); got != want {
			t.Fatalf("kit sees %s=%q, want %q", windowBackendVar, got, want)
		}

		if got, want := os.Getenv(windowBackendVar), os.Getenv("BUI_CGOENGINE_TEST_GOENV"); got != want {
			t.Fatalf("os.Getenv sees %s=%q, want %q", windowBackendVar, got, want)
		}

		return
	}

	var env []string

	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, windowBackendVar+"=") {
			env = append(env, kv)
		}
	}

	for _, tc := range []struct{ set, want string }{
		{"", "x11"},
		{"wayland", "wayland"},
		{"x11", "x11"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestWindowBackendDefault$", "-test.count=1")

		cmd.Env = slices.Concat(env, []string{"BUI_CGOENGINE_TEST_BACKEND=" + tc.want, "BUI_CGOENGINE_TEST_GOENV=" + tc.set})
		if tc.set != "" {
			cmd.Env = append(cmd.Env, windowBackendVar+"="+tc.set)
		}

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s=%q: %v\n%s", windowBackendVar, tc.set, err, out)
		}
	}
}
