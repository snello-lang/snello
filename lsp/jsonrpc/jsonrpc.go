// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package jsonrpc reads and writes the messages of JSON-RPC 2.0 as the
// Language Server Protocol frames them on a stream: each a header,
// Content-Length and the size of its body in bytes, a blank line, and
// the body, one JSON object. It knows the three kinds of message, a
// request, a notification and a response, and nothing of what they
// say, which is the protocol package's. It is modeled on the base
// protocol of the Language Server Protocol specification, 3.17.
package jsonrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	// The error codes of JSON-RPC 2.0, and the two of the Language
	// Server Protocol a server answers with.
	ParseError           = -32700
	InvalidRequest       = -32600
	MethodNotFound       = -32601
	InvalidParams        = -32602
	InternalError        = -32603
	ServerNotInitialized = -32002

	// The header a message is framed by and the room its line takes
	// beside the body's length; the version every message names; the id
	// of a response to a message that could not be read; and the most a
	// body may be: far past any document an editor sends, and short of a
	// length that would have the reader allocate without bound for a
	// stream that is not the protocol's.
	contentLength = "Content-Length"
	headerEnd     = "\r\n"
	headerRoom    = len(contentLength) + len(": ") + 2*len(headerEnd) + lengthDigits
	version       = "2.0"
	nullID        = "null"
	bodyMost      = 256 << 20
	lengthDigits  = 9 // of bodyMost, the longest length a header names
)

// A Message is one message of the protocol: a request, which has a
// method and an id; a notification, which has a method and no id; or a
// response, which has the id of the request it answers and a result or
// an error. The id is kept as the client wrote it, a number or a
// string, so that a response repeats it exactly.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// An Error is the error a response carries in place of a result.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// A Reader reads the messages of a stream one at a time.
type Reader struct {
	in *bufio.Reader
}

// A Writer writes messages to a stream, each framed and written whole
// in one write.
type Writer struct {
	out io.Writer
}

// IsRequest reports whether m is a request: a method and an id.
func (m *Message) IsRequest() bool { return m.Method != "" && m.ID != nil }

// IsNotification reports whether m is a notification: a method and no
// id.
func (m *Message) IsNotification() bool { return m.Method != "" && m.ID == nil }

// Error returns the error's message with its code.
func (e *Error) Error() string { return "jsonrpc error " + strconv.Itoa(e.Code) + ": " + e.Message }

// Notification makes the notification of method with params, which is
// encoded as JSON.
func Notification(method string, params any) (*Message, error) {
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	return &Message{JSONRPC: version, Method: method, Params: body}, nil
}

// Response makes the response to the request whose id is id, with the
// result encoded as JSON: null for a nil result, which a response that
// answers with nothing still carries.
func Response(id json.RawMessage, result any) (*Message, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Message{JSONRPC: version, ID: id, Result: body}, nil
}

// ErrorResponse makes the response to the request whose id is id that
// carries an error in place of a result; a nil id is written as null,
// for a message that could not be read.
func ErrorResponse(id json.RawMessage, code int, message string) *Message {
	if id == nil {
		id = json.RawMessage(nullID)
	}

	return &Message{JSONRPC: version, ID: id, Error: &Error{Code: code, Message: message}}
}

// NewReader returns a Reader of the messages on in.
func NewReader(in io.Reader) *Reader { return &Reader{in: bufio.NewReader(in)} }

// Read reads the next message. It returns io.EOF at the end of the
// stream between messages, an *Error with ParseError for a message
// whose body is not JSON, which the stream can go on after, and any
// other error for a stream that is not the protocol's, which it cannot.
func (r *Reader) Read() (*Message, error) {
	length, err := r.readHeader()
	if err != nil {
		return nil, err
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r.in, body); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}

		return nil, err
	}

	var m Message
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, &Error{Code: ParseError, Message: "the message is not JSON: " + err.Error()}
	}

	if m.JSONRPC != version {
		return nil, &Error{Code: InvalidRequest, Message: "the message is not JSON-RPC " + version}
	}

	return &m, nil
}

// NewWriter returns a Writer of messages to out.
func NewWriter(out io.Writer) *Writer { return &Writer{out: out} }

// Write writes m, framed, in one write to the stream, so that two
// writers never interleave the parts of their messages.
func (w *Writer) Write(m *Message) error {
	body, err := json.Marshal(m)
	if err != nil {
		return err
	}

	var b bytes.Buffer
	b.Grow(len(body) + headerRoom)
	fmt.Fprintf(&b, "%s: %d%s%s", contentLength, len(body), headerEnd, headerEnd)
	b.Write(body)
	_, err = w.out.Write(b.Bytes())
	return err
}

// readHeader reads a message's header up to the blank line that ends
// it, and returns the length of the body it names. Blank lines before a
// header, which a client may leave between messages, are passed over,
// and so is a header other than Content-Length, Content-Type among
// them; a header with no Content-Length, or one that is not a number of
// bytes the reader will take, is an error.
func (r *Reader) readHeader() (int, error) {
	length := -1
	for started := false; ; {
		line, err := r.in.ReadString('\n')
		if err != nil {
			if !started && strings.TrimSpace(line) == "" && errors.Is(err, io.EOF) {
				return 0, io.EOF
			}

			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}

			return 0, err
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if !started {
				continue
			}

			break
		}

		started = true

		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return 0, errors.New("a header line with no colon: " + strconv.Quote(line))
		}

		if !strings.EqualFold(strings.TrimSpace(name), contentLength) {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || n < 0 || n > bodyMost {
			return 0, errors.New("a Content-Length that is not a length: " + strconv.Quote(value))
		}

		length = n
	}

	if length < 0 {
		return 0, errors.New("a message with no Content-Length")
	}

	return length, nil
}
