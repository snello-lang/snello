// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The math package's square root and the predeclared min and max of
// floats.

#include "internal.h"

#include <math.h>

// tk_f64_min and tk_f64_max are the predeclared min and max on floats,
// as Go's specification has them: NaN when either is NaN, and -0 below
// +0.
double tk_f64_min(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? a : b;
    }

    return a < b ? a : b;
}

double tk_f64_max(double a, double b) {
    if (a != a || b != b) {
        return a != a ? a : b;
    }

    if (a == b) {
        return signbit(a) ? b : a;
    }

    return a > b ? a : b;
}

double tk_math_Sqrt(double x) { return sqrt(x); }
