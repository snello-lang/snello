// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

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

fn main() {
    let mut total = 0;
    for _ in 0..20 {
        let t = build(16);
        total += count(&t);
    }

    println!("{}", total);
}
