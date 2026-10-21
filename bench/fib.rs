// Copyright 2026 The Teak Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

fn fib(n: i64) -> i64 {
    if n < 2 {
        return n;
    }

    fib(n - 1) + fib(n - 2)
}

fn main() {
    println!("{}", fib(35));
}
