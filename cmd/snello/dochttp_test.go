// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
)

// docPort is the port the tests' server says it listens on.
const docPort = 6060

// testServer returns a server of the module dir is in, as serveDoc
// makes one, said to listen on port.
func testServer(t *testing.T, dir string, port int) docServer {
	t.Helper()
	t.Chdir(dir)
	l, err := newDocLoader(false)
	if err != nil {
		t.Fatal(err)
	}

	return docServer{addr: defaultDocAddr, port: port, module: l.module, cwd: l.dir}
}

// TestDocRespond serves the index, a standard package's page, builtin's
// and a module's package's, and refuses what it must.
func TestDocRespond(t *testing.T) {
	mod := writeDocModule(t, docModule)
	for _, c := range []struct {
		method, host, target string
		status               int
		has                  string
	}{
		{methodGet, "localhost:6060", "/", http200, `<a class="topic" href="/pkg/strings">`},
		{methodGet, "localhost:6060", "/pkg/strings?tab=1", http200, `id="Builder"`},
		{methodGet, "127.0.0.1:6060", "/pkg/io", http200, `<a href="/pkg/strings#Reader"><code>strings.Reader</code></a>`},
		{methodGet, "localhost:6060", "/search.json", http200, `"t":"strings.Builder"`},
		{methodGet, "localhost:6060", "/text/strings", http200, "func (b mut Builder) WriteString(s ref string)"},
		{methodGet, "localhost:6060", "/text/nowhere", http404, "no such package"},
		{methodGet, "[::1]:6060", "/pkg/builtin", http200, `id="len"`},
		{methodGet, "localhost:6060", "/pkg/unicode%2Futf8", http200, `id="RuneLen"`},
		{methodGet, "localhost:6060", "/pkg/geo", http200, `Package geo measures.`},
		{methodGet, "localhost:6060", "/pkg/nowhere", http404, "no such package"},
		{methodGet, "localhost:6060", "/nowhere", http404, "no such package"},
		{methodGet, "localhost:6060", "/assets/style.css", http200, "--accent"},
		{methodGet, "localhost:6060", "/assets/nowhere.css", http404, "no such package"},
		{"POST", "localhost:6060", "/", http405, "GET and HEAD alone"},
		{methodGet, "evil.example:6060", "/", http403, "local machine's browser alone"},
		{methodGet, "localhost:7070", "/", http403, "local machine's browser alone"},
	} {
		r := testServer(t, mod, docPort).respond(c.method, c.target, c.host)
		if r.status != c.status || !strings.Contains(string(r.body), c.has) {
			t.Errorf("%s %s (Host %s): %d, want %d with %q:\n%.500s", c.method, c.target, c.host, r.status, c.status, c.has, r.body)
		}
	}
}

// TestDocConn answers a request read off a connection with a response a
// browser reads: the status line, the length, a close, and the page;
// and a request that does not read with 400.
func TestDocConn(t *testing.T) {
	s := testServer(t, t.TempDir(), docPort)
	for _, c := range []struct{ request, status, has string }{
		{"GET /pkg/strings HTTP/1.1\r\nHost: localhost:6060\r\n\r\n", "HTTP/1.1 200 OK", `id="Builder"`},
		{"HEAD /pkg/strings HTTP/1.1\r\nHost: localhost:6060\r\n\r\n", "HTTP/1.1 200 OK", "X-Content-Type-Options: nosniff"},
		{"GET /assets/script.js HTTP/1.1\r\nHost: localhost:6060\r\n\r\n", "HTTP/1.1 200 OK", "Content-Type: text/javascript"},
		{"GET / HTTP/1.1\r\nHost: localhost:6060\r\nHost: evil.example:6060\r\n\r\n", "HTTP/1.1 400 Bad Request", "does not read"},
		{"nonsense\r\n\r\n", "HTTP/1.1 400 Bad Request", "does not read"},
	} {
		client, server := net.Pipe()
		go s.serveConn(server)
		go io.WriteString(client, c.request)
		response, err := io.ReadAll(bufio.NewReader(client))
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasPrefix(string(response), c.status+"\r\n") || !strings.Contains(string(response), c.has) {
			t.Errorf("%q: got\n%.300s", c.request, response)
		}

		if strings.HasPrefix(c.request, methodHead) && strings.Contains(string(response), "<html") {
			t.Error("HEAD answered with a body")
		}
	}
}

// TestHTTPAddr takes -http alone as the default address and -http=ADDR
// as that address.
func TestHTTPAddr(t *testing.T) {
	for _, c := range []struct{ value, want string }{{httpFlagSet, defaultDocAddr}, {":7070", ":7070"}} {
		var a httpAddr
		if err := a.Set(c.value); err != nil || !a.set || a.addr != c.want {
			t.Errorf("Set(%q): %v, %+v, want %q", c.value, err, a, c.want)
		}
	}

	if !isLoopback("localhost") || !isLoopback("127.0.0.1") || !isLoopback("::1") || isLoopback("") || isLoopback("0.0.0.0") {
		t.Error("a loopback host refused, or another taken for one")
	}

	for host, want := range map[string]bool{"localhost": true, "[::1]": true, "evil.example": false} {
		if got := allowedHost(host, ":80", 80); got != want {
			t.Errorf("a Host of %s on port 80: %v, want %v", host, got, want)
		}
	}

	if got := displayAddr(":6060"); got != "localhost:6060" {
		t.Errorf("displayAddr(:6060) = %q", got)
	}
}

// TestHTTPTakesNoPackage refuses -http with a package, which the pages
// would not show alone.
func TestHTTPTakesNoPackage(t *testing.T) {
	code, out, errs := runDoc(t, t.TempDir(), "-http", "strings")
	if code != exitUsage || out != "" || !strings.Contains(errs, "-http serves every package and takes no strings") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
}
