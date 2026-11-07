// SPDX-License-Identifier: GPL-2.0-or-later

package enginetest

import (
	"slices"
	"testing"
)

const comparePixelCount = 10000

func flat(c uint32) []uint32 {
	px := make([]uint32, comparePixelCount)
	for i := range px {
		px[i] = c
	}

	return px
}

// offBy returns a copy of a with count pixels' one channel raised by delta.
func offBy(a []uint32, shift, delta, count int) []uint32 {
	b := slices.Clone(a)
	for i := range count {
		b[i*3] += uint32(delta) << shift
	}

	return b
}

func TestCompareExact(t *testing.T) {
	a := flat(0x00102030)

	d := comparePixels(a, slices.Clone(a))
	if !d.within() || d.Exact != d.Total || d.Total != comparePixelCount || d.MaxChannel != 0 {
		t.Errorf("equal rasters: %v", d)
	}
}

// One pixel off by 3 in any one channel is out, however few pixels that is.
func TestCompareDetectsThreeInOneChannel(t *testing.T) {
	a := flat(0x00102030)

	for _, shift := range []int{0, 8, 16} {
		d := comparePixels(a, offBy(a, shift, 3, 1))
		if d.within() {
			t.Errorf("shift %d: accepted %v", shift, d)
		}

		if d.Exact != comparePixelCount-1 || d.OverTol != 1 || d.MaxChannel != 3 {
			t.Errorf("shift %d: counted %v", shift, d)
		}
	}
}

// A difference of 2 is allowed on at most 0.1% of the pixels, and no more.
func TestCompareTwoOnFewOrMany(t *testing.T) {
	a := flat(0x00102030)
	limit := comparePixelCount / tolFraction

	for _, shift := range []int{0, 8, 16} {
		few := comparePixels(a, offBy(a, shift, 2, limit))
		if !few.within() || few.Exact != comparePixelCount-limit || few.OverTol != 0 || few.MaxChannel != 2 {
			t.Errorf("shift %d: %d pixels off by 2: %v, within = %v, want accepted", shift, limit, few, few.within())
		}

		many := comparePixels(a, offBy(a, shift, 2, limit+1))
		if many.within() {
			t.Errorf("shift %d: %d pixels off by 2 accepted: %v", shift, limit+1, many)
		}
	}
}

// A lower raster can be the larger one, and the difference counts either way.
func TestCompareIsSymmetric(t *testing.T) {
	a := flat(0x00102030)
	b := offBy(a, 8, 3, 1)

	if ab, ba := comparePixels(a, b), comparePixels(b, a); ab != ba {
		t.Errorf("%v against %v", ab, ba)
	}
}

// The top byte is no channel: it counts in HighBits only.
func TestCompareMasksTopByte(t *testing.T) {
	a := flat(0x00102030)
	b := flat(0xFF102030)

	d := comparePixels(a, b)
	if !d.within() || d.Exact != d.Total || d.MaxChannel != 0 || d.HighBits != d.Total {
		t.Errorf("top byte only: %v", d)
	}
}

func TestCompareSizeMismatch(t *testing.T) {
	if d := comparePixels(flat(0), flat(0)[1:]); d.within() || !d.Mismatch {
		t.Errorf("sizes differ: %v, within = %v", d, d.within())
	}
}

func TestChangedPixelsSkipsRect(t *testing.T) {
	a := make([]uint32, 12) // 4 x 3
	b := slices.Clone(a)
	b[0], b[5], b[11] = 1, 1, 1 // (0,0), (1,1), (3,2)

	if got := changedPixels(a, b, 4, rect{}); got != 3 {
		t.Errorf("no skip: %d, want 3", got)
	}

	if got := changedPixels(a, b, 4, rect{x: 0, y: 0, w: 2, h: 2}); got != 1 {
		t.Errorf("skipping the top left: %d, want 1", got)
	}
}
