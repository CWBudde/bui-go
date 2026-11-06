/* SPDX-License-Identifier: GPL-2.0-or-later */
/*
 * Forced into every C and C++ file of the wasm build (scripts/build-wasm.sh),
 * so that the BUI tree compiles unmodified for wasm32-wasip1.
 */
#pragma once

/* WebAssembly has no floating-point exception flags, so wasi-libc's <fenv.h>
 * defines only FE_ALL_EXCEPT (0) and FE_TONEAREST. Blender's expression
 * evaluator (core/intern/expr_pylike_eval.cc) tests FE_DIVBYZERO and
 * FE_INVALID. As 0 they compile, and fetestexcept() keeps returning 0.
 * BUI_expr_eval rejects a non-finite result anyway, so "1/0" and "sqrt(-1)"
 * still fail; only an intermediate inf or nan that turns finite again, as in
 * "1/(1/0)", succeeds here where it fails natively. */
#include <fenv.h>
#ifndef FE_DIVBYZERO
#  define FE_DIVBYZERO 0
#endif
#ifndef FE_INVALID
#  define FE_INVALID 0
#endif
