// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package types

import (
	"strings"
	"testing"
)

func TestCheckTimeLayout(t *testing.T) {
	for _, c := range []struct {
		layout string
		parsed bool
		want   string // a part of the error, or empty for a layout that passes
	}{
		{"", false, ""},
		{"no directive at all", true, ""},
		{"%Y-%m-%dT%H:%M:%S%:z", true, ""},
		{"%Y-%m-%dT%H:%M:%S%.f%:z", true, ""},
		{"%A, %-d %B %Y", true, ""},
		{"%a %b %e %T %Y %Z %z", true, ""},
		{"%-I:%M%p", true, ""},
		{"%D %r %F %R %X %x %c", true, ""},
		{"%H:%M:%S%.3f %.6f %.9f %f", true, ""},
		{"%_m %-m %_d %-H %_j %-y", true, ""},
		{"%j %y %h %P%I %n%t%%", true, ""},
		{"%C %G %g %V %U %W %u %w %s", false, ""}, // written, though not read
		{"100%% at %H", true, ""},
		{"%Q", false, "%Q is not a directive"},
		{"%Y-%m-%Q", true, "%Q is not a directive"},
		{"%", false, "a % at the end names no directive"},
		{"%Y %", true, "a % at the end names no directive"},
		{"%-", false, "a % at the end names no directive"},
		{"% Y", false, "%  is not a directive"},
		{"%-B", false, "%-B is not a directive: - goes before a number"},
		{"%_Z", false, "%_Z is not a directive: _ goes before a number"},
		{"%:H", false, "%:H is not a directive: a colon goes before z alone"},
		{"%-:z", false, "%-:z is not a directive"},
		{"%.2f", false, "%.2 is not a directive: the fraction of a second is"},
		{"%.", false, "%. is not a directive: the fraction of a second is"},
		{"%.3", false, "%.3 is not a directive: the fraction of a second is"},
		{"%-.3f", false, "is not a directive: the fraction of a second is"},
		{"%U", true, "%U does not say which day or time it is, and Parse does not read it"},
		{"%Y %-C", true, "%-C does not say which day or time it is"},
		{"%s", true, "%s does not say which day or time it is"},
		{"%I:%M", true, "%I is an hour of a half day, and the layout has no %p"},
		{"%I:%M", false, ""},
		{"%H:%M %p", true, "%p says which half of the day an hour is in, and the layout has no %I"},
		{"%H:%M %p", false, ""},
	} {
		err := CheckTimeLayout(c.layout, c.parsed)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("CheckTimeLayout(%q, %t) = %v, want no error", c.layout, c.parsed, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("CheckTimeLayout(%q, %t) = %v, want an error with %q", c.layout, c.parsed, err, c.want)
		}
	}
}
