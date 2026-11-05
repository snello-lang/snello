// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

use std::time::{Duration, Instant};

const ROUNDS: i64 = 2000;

struct Node {
    left: Option<Box<Node>>,
    right: Option<Box<Node>>,
}

fn build(depth: i64) -> Box<Node> {
    if depth == 0 {
        return Box::new(Node { left: None, right: None });
    }

    Box::new(Node { left: Some(build(depth - 1)), right: Some(build(depth - 1)) })
}

fn count(n: &Node) -> i64 {
    let mut total = 1;
    if let Some(l) = &n.left {
        total += count(l);
    }

    if let Some(r) = &n.right {
        total += count(r);
    }

    total
}

fn round() -> i64 {
    let t = build(12);
    count(&t)
}

fn main() {
    let live = build(20);
    let mut total = 0;
    let mut took = vec![Duration::ZERO; ROUNDS as usize];
    let all = Instant::now();
    for d in took.iter_mut() {
        let start = Instant::now();
        total += round();
        *d = start.elapsed();
    }

    let mean = all.elapsed() / ROUNDS as u32;
    let longest = took.iter().max().copied().unwrap_or(Duration::ZERO);
    let slow = took.iter().filter(|&&d| d > 2 * mean).count();
    println!("{} {}", count(&live), total);
    println!("round: mean {} us, longest {} us, {} over twice the mean", mean.as_micros(), longest.as_micros(), slow);
}
