// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package examples

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A script drives an example that serves rather than prints: the suite
// starts the program, reads the port it prints on standard error, and
// plays the script's sessions against it over the loopback. A script
// is sections separated by blank lines, one connection each, of lines
// "> text", sent to the server with a newline, and "< text", the reply
// expected back; a line starting with # is a comment. A section ends
// when the server closes the connection, which the suite requires, so
// a session's last line is a request the server answers by closing,
// and the script's last one stops the server, so that its standard
// output is complete when it is compared with the expected output.

// play starts bin in dir, drives it with script and returns what it
// printed on standard output once it exited. A reply that differs, a
// connection the server left open, an exit that is not 0 or a server
// that did not exit within ten seconds of the script's end is an
// error that says which.
func play(bin, dir string, script []byte) ([]byte, error) {
	cmd := exec.Command(bin)
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// The rest of standard error is read until the server exits, which
	// Wait must come after, and is what every failure reports, since a
	// server that panicked says so there.
	r := bufio.NewReader(stderr)
	var rest strings.Builder
	exited := make(chan error, 1)
	fail := func(err error) ([]byte, error) {
		cmd.Process.Kill()
		<-exited
		if rest.Len() > 0 {
			err = fmt.Errorf("%v\n--- the server's standard error ---\n%s", err, rest.String())
		}

		return nil, err
	}

	// The port is the first line of standard error, waited for ten
	// seconds at most, so that a server that hangs before it listens
	// fails saying so rather than at go test's own deadline.
	first := make(chan string, 1)
	go func() {
		line, err := r.ReadString('\n')
		if err != nil {
			line = ""
		}

		first <- line
		io.Copy(&rest, r)
		exited <- cmd.Wait()
	}()

	var port string
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	select {
	case line := <-first:
		port = strings.TrimSpace(line)
	case <-deadline.C:
		return fail(errors.New("no port on standard error within 10s"))
	}

	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fail(fmt.Errorf("the first line of standard error is %q, not a port", port))
	}

	for i, lines := range sections(script) {
		if err := session(port, lines); err != nil {
			return fail(fmt.Errorf("session %d: %v", i+1, err))
		}
	}

	deadline.Reset(10 * time.Second)
	select {
	case err := <-exited:
		if err != nil {
			return nil, fmt.Errorf("%v\n%s", err, rest.String())
		}
	case <-deadline.C:
		return fail(errors.New("the server did not stop within 10s of the script's end"))
	}

	return stdout.Bytes(), nil
}

// sections splits a script into its sessions: the lines of each, with
// the comments left out.
func sections(script []byte) [][]string {
	var all [][]string
	var cur []string
	for _, line := range strings.Split(string(script), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") {
			continue
		}

		if strings.TrimSpace(line) == "" {
			if cur != nil {
				all = append(all, cur)
				cur = nil
			}

			continue
		}

		cur = append(cur, line)
	}

	if cur != nil {
		all = append(all, cur)
	}

	return all
}

// session plays one section over a connection to port: each "> text"
// is sent, each "< text" is read and compared, and at the end the
// server must close the connection with nothing more sent. A reply
// or the close that does not come within ten seconds is a failure.
func session(port string, lines []string) error {
	return sessionWithin(port, lines, 10*time.Second)
}

// sessionWithin is session with the time every wait is given.
func sessionWithin(port string, lines []string, wait time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), wait)
	if err != nil {
		return err
	}

	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(wait)); err != nil {
		return err
	}

	r := bufio.NewReader(conn)
	for _, line := range lines {
		switch {
		case line == ">" || strings.HasPrefix(line, "> "):
			if _, err := io.WriteString(conn, strings.TrimPrefix(line[1:], " ")+"\n"); err != nil {
				return err
			}
		case line == "<" || strings.HasPrefix(line, "< "):
			want := strings.TrimPrefix(line[1:], " ")
			got, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("waiting for %q: %v", want, err)
			}

			if got = strings.TrimSuffix(got, "\n"); got != want {
				return fmt.Errorf("got %q, want %q", got, want)
			}
		default:
			return fmt.Errorf("script line %q starts with neither > nor <", line)
		}
	}

	extra, err := r.ReadString('\n')
	if extra != "" || err == nil {
		return fmt.Errorf("the server sent %q after the session's last line", extra)
	}

	if err != io.EOF {
		return fmt.Errorf("the server did not close the connection: %v", err)
	}

	return nil
}

// TestSections checks how a script is cut into sessions: comments and
// blank lines around them dropped, a blank line between them a cut,
// and a Windows line ending not part of a line.
func TestSections(t *testing.T) {
	script := "# a comment\n\n> SET a 1\r\n< OK\n\n\n> QUIT\n# the end\n"
	got := sections([]byte(script))
	want := [][]string{{"> SET a 1", "< OK"}, {"> QUIT"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("sections: got %q, want %q", got, want)
	}
}

// TestSession drives a Go server with the three endings a session can
// have: the server closes after the last line, which is right; the
// server sends more, which is an error naming it; and the server keeps
// the connection open, which is an error too. A wrong reply is the
// fourth case.
func TestSession(t *testing.T) {
	cases := []struct {
		name  string
		serve func(c net.Conn, end <-chan struct{}) // what the server does after answering "PING" with "PONG"; end closes when the case is over
		lines []string
		want  string // "" for no error, else text the error must hold
	}{
		{"closes", func(c net.Conn, _ <-chan struct{}) { c.Close() }, []string{"> PING", "< PONG"}, ""},
		{"sends more", func(c net.Conn, _ <-chan struct{}) { io.WriteString(c, "MORE\n"); c.Close() }, []string{"> PING", "< PONG"}, "sent \"MORE\\n\" after"},
		{"stays open", func(c net.Conn, end <-chan struct{}) { <-end; c.Close() }, []string{"> PING", "< PONG"}, "did not close"}, // open past the second the session waits
		{"wrong reply", func(c net.Conn, _ <-chan struct{}) { c.Close() }, []string{"> PING", "< PANG"}, "got \"PONG\", want \"PANG\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			defer l.Close()
			end := make(chan struct{})
			defer close(end)
			go func() {
				c, err := l.Accept()
				if err != nil {
					return
				}

				r := bufio.NewReader(c)
				if line, _ := r.ReadString('\n'); line == "PING\n" {
					io.WriteString(c, "PONG\n")
				}

				tc.serve(c, end)
			}()

			_, port, _ := net.SplitHostPort(l.Addr().String())
			err = sessionWithin(port, tc.lines, time.Second)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("session: %v, want no error", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("session: %v, want an error holding %q", err, tc.want)
			}
		})
	}
}
