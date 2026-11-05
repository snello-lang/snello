// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

use std::collections::HashMap;

fn main() {
    let mut counts: HashMap<String, i64> = HashMap::new();
    let mut seed: i64 = 42;
    for _ in 0..2000000 {
        seed = (seed * 1103515245 + 12345) % 2147483648;
        let word = format!("w{}", seed % 5000);
        *counts.entry(word).or_insert(0) += 1;
    }

    let best = counts.values().copied().max().unwrap_or(0);
    println!("{} {}", counts.len(), best);
}
