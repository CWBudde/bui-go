// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// The tolerance from Phase 0's go criterion: exact, or a per-channel difference of
// at most tolChannel on at most 1/tolFraction of the pixels. Phase 0 got exact
// matches on every tree, so this should not be needed.
const (
	tolChannel  = 2
	tolFraction = 1000
)

// rgbMask keeps the channels of a 0x00RRGGBB pixel. The seam promises zero in the top
// byte, which is no channel to compare: HighBits counts where that promise is broken.
const rgbMask = 0x00FFFFFF

// pixelDiff summarizes two rasters.
type pixelDiff struct {
	Total      int // pixels compared
	Exact      int // pixels with equal channels
	OverTol    int // pixels with a channel off by more than tolChannel
	MaxChannel int // the largest difference in any channel
	HighBits   int // pixels with a non-zero top byte on either side
	Mismatch   bool
}

func (d pixelDiff) String() string {
	if d.Mismatch {
		return "raster sizes differ"
	}

	pct := 100.0
	if d.Total > 0 {
		pct = 100 * float64(d.Exact) / float64(d.Total)
	}

	return fmt.Sprintf("exact %d/%d (%.4f%%), %d over %d, largest channel difference %d, %d with a non-zero top byte",
		d.Exact, d.Total, pct, d.OverTol, tolChannel, d.MaxChannel, d.HighBits)
}

// within reports whether d meets the tolerance.
func (d pixelDiff) within() bool {
	if d.Mismatch || d.HighBits > 0 {
		return false
	}

	if d.Exact == d.Total {
		return true
	}

	return d.MaxChannel <= tolChannel && (d.Total-d.Exact)*tolFraction <= d.Total
}

// comparePixels compares the channels of a and b, masking out the top byte.
func comparePixels(a, b []uint32) pixelDiff {
	if len(a) != len(b) {
		return pixelDiff{Mismatch: true}
	}

	d := pixelDiff{Total: len(a)}

	for i := range a {
		if (a[i]|b[i])&^rgbMask != 0 {
			d.HighBits++
		}

		worst := channelDiff(a[i], b[i])
		if worst == 0 {
			d.Exact++

			continue
		}

		d.MaxChannel = max(d.MaxChannel, worst)
		if worst > tolChannel {
			d.OverTol++
		}
	}

	return d
}

// channelDiff returns the largest difference in any channel of two pixels.
func channelDiff(a, b uint32) int {
	worst := 0

	for shift := 0; shift < 24; shift += 8 {
		ca, cb := int(a>>shift&0xff), int(b>>shift&0xff)
		worst = max(worst, ca-cb, cb-ca)
	}

	return worst
}

// changedPixels counts the pixels that differ between a and b outside skip. It
// compares two renders of one engine, to prove that an action did something.
func changedPixels(a, b []uint32, w int, skip rect) int {
	n := 0

	for i := range a {
		if a[i]&rgbMask != b[i]&rgbMask && !skip.contains(i%w, i/w) {
			n++
		}
	}

	return n
}

// writeFailure stores both sides and a diff for a human to look at, and logs where.
func writeFailure(t *testing.T, name string, a, b []uint32, w, h int) {
	t.Helper()

	if len(a) != w*h || len(b) != w*h {
		t.Logf("no PNGs for %s: %d and %d pixels at %d x %d", name, len(a), len(b), w, h)

		return
	}

	dir, err := failureDir()
	if err != nil {
		t.Logf("no PNGs for %s: %v", name, err)

		return
	}

	diff := make([]uint32, len(a))
	for i := range a {
		diff[i] = amplifiedDiff(a[i], b[i])
	}

	for suffix, px := range map[string][]uint32{"ref": a, "other": b, "diff": diff} {
		path := filepath.Join(dir, name+"-"+suffix+".png")
		if err := writePNG(path, px, w, h); err != nil {
			t.Logf("no PNG: %v", err)

			continue
		}

		t.Logf("wrote %s", path)
	}
}

// failureDir is build/enginetest in the repository, which survives the test unlike
// t.TempDir, or a new temporary directory when the source tree is not at hand.
func failureDir() (string, error) {
	if _, file, _, ok := runtime.Caller(0); ok {
		repo := filepath.Join(filepath.Dir(file), "..", "..")
		dir := filepath.Join(repo, "build", "enginetest")

		if _, err := os.Stat(filepath.Join(repo, "go.mod")); err == nil && os.MkdirAll(dir, 0o750) == nil {
			return dir, nil
		}
	}

	dir, err := os.MkdirTemp("", "enginetest-*")
	if err != nil {
		return "", fmt.Errorf("make a directory for PNGs: %w", err)
	}

	return dir, nil
}

// amplifiedDiff shows a difference of even one step in a channel.
func amplifiedDiff(a, b uint32) uint32 {
	var out uint32

	for shift := 0; shift < 24; shift += 8 {
		ca, cb := int(a>>shift&0xff), int(b>>shift&0xff)
		out |= uint32(min(max(ca-cb, cb-ca)*32, 255)) << shift //nolint:gosec // limited to 0..255
	}

	return out
}

// writePNG stores 0x00RRGGBB pixels.
func writePNG(path string, px []uint32, w, h int) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	for i, p := range px {
		c := color.RGBA{R: uint8(p >> 16), G: uint8(p >> 8), B: uint8(p), A: 255} //nolint:gosec // bytes on purpose
		img.SetRGBA(i%w, i/w, c)
	}

	f, err := os.Create(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("create PNG: %w", err)
	}

	if err := png.Encode(f, img); err != nil {
		_ = f.Close()

		return fmt.Errorf("encode %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}

	return nil
}

func itoa(i int) string     { return strconv.Itoa(i) }
func quote(s string) string { return strconv.Quote(s) }
