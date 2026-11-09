// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The C side of package mathx: the shim that calls the system's cbrt
// under the symbol mathx.snello.h declares.

#include "mathx.snello.h"

#include <math.h>

double snm_mathx_Cbrt(double x) {
    return cbrt(x);
}
