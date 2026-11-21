// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snello-lang/snello/compiler/doc"
	"github.com/snello-lang/snello/compiler/importer"
	"github.com/snello-lang/snello/compiler/version"
)

const (
	// The flag that serves the pages, and the address it serves at
	// when it names none.
	httpFlag       = "http"
	httpUsage      = "serve the pages in a browser, at `HOST:PORT`, localhost:6060 when -http names none"
	defaultDocAddr = "localhost:6060"
	httpFlagSet    = "true" // what the flag package gives a flag written alone

	// The names of the local machine a request may name, and the routes:
	// the index, what the search looks through, and a page as text.
	localhostName = "localhost"
	indexRoute    = "/"
	searchRoute   = "/search.json"
	textRoute     = "/text/"

	// What the server says of a request it refuses.
	notLocal   = "snello doc serves the local machine's browser alone\n"
	notFound   = "no such package\n"
	notAllowed = "snello doc serves GET and HEAD alone\n"
	badRequest = "the request does not read\n"

	// What a request and a response are made of, HTTP/1.1's words, and
	// the most of a request read and the time a connection is given.
	httpVersion     = "HTTP/1.1"
	httpPrefix      = "HTTP/1."
	methodGet       = "GET"
	methodHead      = "HEAD"
	hostHeader      = "Host"
	htmlType        = "text/html; charset=utf-8"
	textType        = "text/plain; charset=utf-8"
	jsonType        = "application/json"
	requestMost     = 64 << 10
	connDeadline    = 30 * time.Second
	acceptPause     = 5 * time.Millisecond
	defaultHTTPPort = 80

	// The statuses the server answers with.
	http200 = 200
	http400 = 400
	http403 = 403
	http404 = 404
	http405 = 405
	http500 = 500
)

// statusText is each status's reason phrase.
var statusText = map[int]string{http200: "OK", http400: "Bad Request", http403: "Forbidden", http404: "Not Found", http405: "Method Not Allowed", http500: "Internal Server Error"}

// An httpAddr is -http's value: whether it was given, and the address,
// the default for -http written alone.
type httpAddr struct {
	set  bool
	addr string
}

func (a *httpAddr) String() string { return a.addr }

// Set takes -http=ADDR, or -http alone, which the flag package gives as
// true and which serves at the default address.
func (a *httpAddr) Set(s string) error {
	a.set = true
	a.addr = s
	if s == httpFlagSet {
		a.addr = defaultDocAddr
	}

	return nil
}

// IsBoolFlag lets -http be written alone.
func (a *httpAddr) IsBoolFlag() bool { return true }

// serveDoc serves the pages at addr until the program is ended, after
// printing where; it warns when addr is not the local machine's. Each
// connection is answered on a goroutine of its own.
func serveDoc(addr string, unexported bool, stdout, stderr io.Writer) int {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", program, docCommand, err)
		return exitFailure
	}

	host, _, _ := net.SplitHostPort(addr)
	if !isLoopback(host) {
		fmt.Fprintf(stderr, "%s %s: warning: serving at %s, which other machines may reach\n", program, docCommand, addr)
	}

	l, err := newDocLoader(unexported) // the module's dependencies resolved once, here
	if err != nil {
		fmt.Fprintf(stderr, "%s %s: %v\n", program, docCommand, err)
		return exitFailure
	}

	fmt.Fprintf(stdout, "Serving the documentation at http://%s/\n", displayAddr(addr))
	s := docServer{addr: addr, port: ln.Addr().(*net.TCPAddr).Port, unexported: unexported, module: l.module, cwd: l.dir}
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				fmt.Fprintf(stderr, "%s %s: %v\n", program, docCommand, err)
				return exitFailure
			}

			time.Sleep(acceptPause) // out of descriptors, or a connection lost before it was taken: the next may come
			continue
		}

		go s.serveConn(c)
	}
}

// A docServer answers a browser's requests for the pages: the address
// it serves, the port it listens on, whether its pages show the
// unexported names, and the module it serves, its dependencies resolved
// as it started, or none, with the directory imports are found from.
type docServer struct {
	addr       string
	port       int
	unexported bool
	module     *importer.Module
	cwd        string
}

// serveConn answers one request on c and closes it: the request's line
// and its header, read through net/textproto with a limit and a
// deadline, and a response that says its length and closes the
// connection, which is all a browser needs of a local page.
func (s docServer) serveConn(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(connDeadline))
	r := textproto.NewReader(bufio.NewReader(io.LimitReader(c, requestMost)))
	line, err := r.ReadLine()
	header, herr := r.ReadMIMEHeader()
	method, target, ok := parseRequestLine(line)
	res := text(http400, badRequest)
	if hosts := header.Values(hostHeader); err == nil && herr == nil && ok && len(hosts) <= 1 { // two Hosts are no request
		res = s.safeRespond(method, target, header.Get(hostHeader))
	}

	w := bufio.NewWriter(c)
	fmt.Fprintf(w, "%s %d %s\r\n", httpVersion, res.status, statusText[res.status])
	fmt.Fprintf(w, "Content-Type: %s\r\nContent-Length: %d\r\nX-Content-Type-Options: nosniff\r\nConnection: close\r\n\r\n", res.contentType, len(res.body))
	if method != methodHead {
		w.Write(res.body)
	}

	w.Flush()
}

// safeRespond is respond, answering 500 for a page whose making
// panicked, so that one page cannot end the server.
func (s docServer) safeRespond(method, target, host string) (r reply) {
	defer func() {
		if p := recover(); p != nil {
			r = text(http500, fmt.Sprintf("%v\n", p))
		}
	}()

	return s.respond(method, target, host)
}

// parseRequestLine splits a request's first line, GET /pkg/io HTTP/1.1,
// into its method and its target.
func parseRequestLine(line string) (method, target string, ok bool) {
	method, rest, ok1 := strings.Cut(line, " ")
	target, version, ok2 := strings.Cut(rest, " ")
	return method, target, ok1 && ok2 && strings.HasPrefix(version, httpPrefix)
}

// respond makes the response to a request: the index, a package's
// page, a file the pages load, what the filter looks through, or a
// package's page as text, each made when it is asked for from its
// packages loaded then, so that an edit shows on the next load. It answers only GET and HEAD,
// and only a request whose Host is the address it serves or a loopback
// name on its port, so that a page of another site cannot read a
// private module's source through a name rebound to this machine.
func (s docServer) respond(method, target, host string) reply {
	if method != methodGet && method != methodHead {
		return text(http405, notAllowed)
	}

	if !allowedHost(host, s.addr, s.port) {
		return text(http403, notLocal)
	}

	path, _, _ := strings.Cut(target, "?")
	path, err := url.PathUnescape(path)
	if err != nil {
		return text(http400, badRequest)
	}

	if name, ok := strings.CutPrefix(path, doc.AssetRoute); ok {
		content, contentType, ok := doc.Asset(name)
		if !ok {
			return text(http404, notFound)
		}

		return reply{http200, contentType, []byte(content)}
	}

	if path != indexRoute && path != searchRoute && !strings.HasPrefix(path, doc.PackageRoute) && !strings.HasPrefix(path, textRoute) {
		return text(http404, notFound)
	}

	l := loaderFor(s.module, s.cwd, s.unexported)
	var page bytes.Buffer
	textPkg, isText := strings.CutPrefix(path, textRoute)
	switch pkg, isPage := strings.CutPrefix(path, doc.PackageRoute); {
	case path == indexRoute:
		err = doc.WriteIndex(&page, l.nav(), l.entries())
	case path == searchRoute:
		if err := doc.WriteSearch(&page, l.entries()); err != nil {
			return text(http500, err.Error())
		}

		return reply{http200, jsonType, page.Bytes()}
	case isText && slices.Contains(l.servedPaths(), textPkg): // the page as the terminal prints it, every name with its comment
		p, perr := l.packageAt(textPkg)
		if perr != nil {
			return text(http500, perr.Error())
		}

		p.WritePackage(&page, true)
		return reply{http200, textType, page.Bytes()}
	case isPage && slices.Contains(l.servedPaths(), pkg):
		p, perr := l.packageAt(pkg)
		if perr != nil {
			return text(http500, perr.Error())
		}

		for _, t := range p.Types {
			if isTrait(t) {
				t.Implementors = doc.Implementors(p.Path, t.Name, l.everyPackage(), s.unexported)
			}
		}

		err = p.WriteHTML(&page, l.nav())
	default:
		return text(http404, notFound)
	}

	if err != nil {
		return text(http500, err.Error())
	}

	return reply{http200, htmlType, page.Bytes()}
}

// A reply is a response: its status, what its body is, and the body.
type reply struct {
	status      int
	contentType string
	body        []byte
}

// text returns a reply of a line of text saying why there is no page.
func text(status int, line string) reply { return reply{status, textType, []byte(line)} }

// entries returns the index's entry of every package served, its names
// for the search with it.
func (l *docLoader) entries() []doc.IndexEntry {
	var entries []doc.IndexEntry
	for _, path := range l.servedPaths() {
		if p, err := l.packageAt(path); err == nil {
			entries = append(entries, p.Index())
		}
	}

	return entries
}

// servedPaths returns what the server has a page for: every standard
// package, builtin among them, and every package of the module.
func (l *docLoader) servedPaths() []string {
	paths := slices.Clone(importer.Paths())
	if l.module != nil {
		paths = append(paths, l.modulePaths()...)
	}

	return paths
}

// nav returns what every page's sidebar lists: the packages it serves,
// by where each comes from, builtin's predeclared names, the standard
// library and the module's own.
func (l *docLoader) nav() doc.Nav {
	std := slices.DeleteFunc(slices.Clone(importer.Paths()), func(path string) bool { return path == doc.BuiltinPath })
	nav := doc.Nav{Version: version.Current, Groups: []doc.NavGroup{
		{Title: doc.LanguageGroup, Paths: []string{doc.BuiltinPath}},
		{Title: doc.StandardGroup, Paths: std},
	}}
	if l.module != nil {
		nav.Groups = append(nav.Groups, doc.NavGroup{Title: doc.ModuleGroup, Paths: l.modulePaths()})
	}

	return nav
}

// packageAt loads the package at a path the server has a page for.
func (l *docLoader) packageAt(path string) (*doc.Package, error) {
	if path == doc.BuiltinPath {
		return doc.Builtin()
	}

	return l.load(path)
}

// allowedHost reports whether a request's Host names the server: the
// address it was told, or a loopback name on its port, which a browser
// leaves out for port 80.
func allowedHost(host, addr string, port int) bool {
	if host == addr {
		return true
	}

	name, p, err := net.SplitHostPort(host)
	if err != nil && port == defaultHTTPPort {
		name, p, err = host, strconv.Itoa(defaultHTTPPort), nil
	}

	return err == nil && p == strconv.Itoa(port) && isLoopback(strings.Trim(name, "[]"))
}

// isLoopback reports whether a host is the local machine: localhost, an
// empty host, which net.Listen takes for every address and is warned
// of elsewhere, aside.
func isLoopback(host string) bool {
	if host == localhostName {
		return true
	}

	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// displayAddr returns the address as a browser is to be pointed at it:
// localhost for a host left empty.
func displayAddr(addr string) string {
	if host, port, err := net.SplitHostPort(addr); err == nil && host == "" {
		return net.JoinHostPort(localhostName, port)
	}

	return addr
}
