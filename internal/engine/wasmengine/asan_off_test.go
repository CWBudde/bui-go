// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !asan

package wasmengine

// asanBuild is true under -asan, whose instrumentation allocates by itself.
const asanBuild = false
