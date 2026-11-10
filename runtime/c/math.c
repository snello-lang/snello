// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The math package's functions, each the system's math library's, and
// the predeclared min and max of floats.

#include "internal.h"

#include <float.h>
#include <math.h>

// sn_f64_min and sn_f64_max are the predeclared min and max on floats,
// as Go's specification has them: NaN when either is NaN, and -0 below
// +0.
double sn_f64_min(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? a : b;
    }

    return a < b ? a : b;
}

double sn_f64_max(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? b : a;
    }

    return a > b ? a : b;
}

// The functions of package math, each one call of the math library's.
// A build that optimizes the program and the runtime as one unit
// writes the library's in its place, and the compiler knows the square
// root and the four roundings as instructions where the processor has
// them. Inf, NaN and the three tests are written here: they are a
// constant or a comparison.
double sn_math_Sqrt(double x) { return sqrt(x); }
double sn_math_Cbrt(double x) { return cbrt(x); }
double sn_math_Floor(double x) { return floor(x); }
double sn_math_Ceil(double x) { return ceil(x); }
double sn_math_Trunc(double x) { return trunc(x); }
double sn_math_Round(double x) { return round(x); }
double sn_math_Mod(double x, double y) { return fmod(x, y); }
double sn_math_Pow(double x, double y) { return pow(x, y); }
double sn_math_Exp(double x) { return exp(x); }
double sn_math_Log(double x) { return log(x); }
double sn_math_Log2(double x) { return log2(x); }
double sn_math_Log10(double x) { return log10(x); }
double sn_math_Sin(double x) { return sin(x); }
double sn_math_Cos(double x) { return cos(x); }
double sn_math_Tan(double x) { return tan(x); }
double sn_math_Asin(double x) { return asin(x); }
double sn_math_Acos(double x) { return acos(x); }
double sn_math_Atan(double x) { return atan(x); }
double sn_math_Atan2(double y, double x) { return atan2(y, x); }
double sn_math_Sinh(double x) { return sinh(x); }
double sn_math_Cosh(double x) { return cosh(x); }
double sn_math_Tanh(double x) { return tanh(x); }
double sn_math_Hypot(double p, double q) { return hypot(p, q); }
double sn_math_Inf(int64_t sign) { return sign >= 0 ? INFINITY : -INFINITY; }
double sn_math_NaN(void) { return NAN; }
bool sn_math_IsNaN(double x) { return x != x; }
bool sn_math_IsInf(double x, int64_t sign) { return (sign >= 0 && x > DBL_MAX) || (sign <= 0 && x < -DBL_MAX); }
bool sn_math_Signbit(double x) { return signbit(x) != 0; }
double sn_math_Copysign(double x, double sign) { return copysign(x, sign); }

// The functions of math/bits under the library's names, each the
// header's: a call the program writes is the instruction, written in
// its place, and these are what a value of one of them calls.
#define SN_BITS_NAMED(S, W, T)                                                              \
    int64_t sn_math__bits_LeadingZeros##S(T x) { return sn_bits_LeadingZeros##W(x); }        \
    int64_t sn_math__bits_TrailingZeros##S(T x) { return sn_bits_TrailingZeros##W(x); }      \
    int64_t sn_math__bits_OnesCount##S(T x) { return sn_bits_OnesCount##W(x); }              \
    int64_t sn_math__bits_Len##S(T x) { return sn_bits_Len##W(x); }                          \
    T sn_math__bits_Reverse##S(T x) { return sn_bits_Reverse##W(x); }                        \
    T sn_math__bits_RotateLeft##S(T x, int64_t k) { return sn_bits_RotateLeft##W(x, k); }

SN_BITS_NAMED(, 64, uint64_t)
SN_BITS_NAMED(8, 8, uint8_t)
SN_BITS_NAMED(16, 16, uint16_t)
SN_BITS_NAMED(32, 32, uint32_t)
SN_BITS_NAMED(64, 64, uint64_t)
