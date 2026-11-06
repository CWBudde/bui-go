// SPDX-License-Identifier: GPL-2.0-or-later

//go:build !(cgo && linux && amd64)

package wasmspike

// NativeAvailable reports whether this build has the cgo kit.
const NativeAvailable = false

// NativeKit is absent without cgo on Linux amd64.
type NativeKit struct{ Kit }

// NewNativeKit returns nil: the committed libraries are Linux amd64 only.
func NewNativeKit() *NativeKit { return nil }
