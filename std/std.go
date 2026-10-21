// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package std embeds the standard library's Teak sources in the
// compiler, so that a build needs no library path; the compiler's
// importer loads them. Each package is one directory holding one file
// named after it, math/math.teak, written in Teak; a function it
// declares without a body is the runtime's, under the C name the
// backend gives the function (_docs/design/05-standard-library.md).
package std

import "embed"

// Sources holds every package's source file, by its path in this
// directory: math/math.teak.
//
//go:embed */*.teak
var Sources embed.FS
