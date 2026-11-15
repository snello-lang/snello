// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Owner struct {
	Name string
	Age  int
}

type Record struct {
	ID     int
	Name   string
	Tags   []string
	Score  float64
	Active bool
	Owner  Owner
	Note   *string
}

func main() {
	const rounds = 200
	records := []Record{}
	for i := 0; i < 1000; i++ {
		var note *string
		if i%3 == 0 {
			s := "note " + strconv.Itoa(i)
			note = &s
		}

		records = append(records, Record{ID: i, Name: "record " + strconv.Itoa(i), Tags: []string{"a", "bb", "ccc"}, Score: float64(i) / 8.0, Active: i%2 == 0, Owner: Owner{Name: "owner", Age: i % 90}, Note: note})
	}

	raw, _ := json.Marshal(records)
	text := string(raw)
	start := time.Now()
	n := 0
	for r := 0; r < rounds; r++ {
		b, _ := json.Marshal(records)
		n += len(b)
	}

	encode := time.Since(start)
	start = time.Now()
	m := 0
	for r := 0; r < rounds; r++ {
		var back []Record
		_ = json.Unmarshal(raw, &back)
		m += len(back)
	}

	decode := time.Since(start)
	start = time.Now()
	sum := 0
	for r := 0; r < rounds; r++ { // untyped: the text read into any, each record's ID read back
		var doc any
		_ = json.Unmarshal(raw, &doc)
		for _, e := range doc.([]any) {
			sum += int(e.(map[string]any)["ID"].(float64))
		}
	}

	untyped := time.Since(start)
	start = time.Now()
	items, written := 0, 0
	for r := 0; r < rounds; r++ { // a tree: the text read into any and written back
		var v any
		_ = json.Unmarshal(raw, &v)
		items += len(v.([]any))
		b, _ := json.Marshal(v)
		written += len(b)
	}

	tree := time.Since(start)
	start = time.Now()
	laid := 0
	for r := 0; r < rounds; r++ { // laid out for a person to read
		b, _ := json.MarshalIndent(records, "", "  ")
		laid += len(b)
	}

	indented := time.Since(start)
	start = time.Now()
	streamed := 0
	for r := 0; r < rounds; r++ { // a stream: each record written by an Encoder, a line each, and read back by a Decoder
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for i := range records {
			if enc.Encode(&records[i]) != nil {
				break
			}
		}

		dec := json.NewDecoder(&buf)
		for {
			var back Record
			if dec.Decode(&back) != nil {
				break
			}

			streamed += back.ID % 2
		}
	}

	stream := time.Since(start)
	first := text[0:strings.Index(text, "},")]
	fmt.Println(len(text), n/rounds, m/rounds, first)
	fmt.Println(sum/rounds, items/rounds, written > 0, laid/rounds, streamed/rounds)
	per := int64(rounds * 1000)
	fmt.Println("encode", encode.Nanoseconds()/per, "decode", decode.Nanoseconds()/per, "untyped", untyped.Nanoseconds()/per, "tree", tree.Nanoseconds()/per, "indent", indented.Nanoseconds()/per, "stream", stream.Nanoseconds()/per)
}
