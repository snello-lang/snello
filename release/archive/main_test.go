// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The release directory the tests archive: its name, its files, and
// the time its entries carry.
const (
	testName  = "snello-0.1.0-linux-amd64"
	testEpoch = 1700000000
)

var testFiles = map[string]string{"snello": "binary", "LICENSE": "license", "README.md": "readme"}

// stage writes the release directory under a temporary out directory
// and returns the out directory.
func stage(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	root := filepath.Join(out, testName)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}

	for name, content := range testFiles {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return out
}

// archive writes the archive of form into a fresh stage and returns its
// bytes.
func archive(t *testing.T, form string) []byte {
	t.Helper()
	out := stage(t)
	root := filepath.Join(out, testName)
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	when := time.Unix(testEpoch, 0).UTC()
	path := filepath.Join(out, testName+map[string]string{tarForm: tarSuffix, zipForm: zipSuffix}[form])
	if form == tarForm {
		err = writeTar(path, root, testName, entries, when)
	} else {
		err = writeZip(path, root, testName, entries, when)
	}

	if err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// TestSameBytes writes each archive twice, from two stages whose files
// were written at different times with different modes, and holds the
// two to the same bytes.
func TestSameBytes(t *testing.T) {
	for _, form := range []string{tarForm, zipForm} {
		first := archive(t, form)
		time.Sleep(10 * time.Millisecond)
		if !bytes.Equal(first, archive(t, form)) {
			t.Errorf("two %s archives of the same files differ", form)
		}
	}
}

// TestTarEntries reads the tar.gz back: the directory then the files
// in the order of their names, each with the time, no owner, and the
// binary's or a file's mode; and a gzip header with no name and no
// time.
func TestTarEntries(t *testing.T) {
	data := archive(t, tarForm)
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}

	if gz.Name != "" || !gz.ModTime.IsZero() {
		t.Errorf("the gzip header carries name %q and time %v", gz.Name, gz.ModTime)
	}

	want := []struct {
		name string
		mode int64
	}{{testName + "/", dirMode}, {testName + "/LICENSE", fileMode}, {testName + "/README.md", fileMode}, {testName + "/snello", binaryMode}}
	tr := tar.NewReader(gz)
	for _, w := range want {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("after %s: %v", w.name, err)
		}

		if h.Name != w.name || h.Mode != w.mode || h.Uid != 0 || h.Gid != 0 || h.Uname != "" || h.Gname != "" || h.ModTime.Unix() != testEpoch {
			t.Errorf("entry %+v, want %s with mode %o, no owner and time %d", h, w.name, w.mode, testEpoch)
		}
	}

	if _, err := tr.Next(); err != io.EOF {
		t.Errorf("an entry after the files: %v", err)
	}
}

// TestZipEntries reads the zip back: the files in the order of their
// names, each with the time and the binary's or a file's mode, as a
// Unix mode wherever it was written.
func TestZipEntries(t *testing.T) {
	data := archive(t, zipForm)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		name string
		mode os.FileMode
	}{{testName + "/LICENSE", fileMode}, {testName + "/README.md", fileMode}, {testName + "/snello", binaryMode}}
	if len(zr.File) != len(want) {
		t.Fatalf("%d entries, want %d", len(zr.File), len(want))
	}

	for i, w := range want {
		f := zr.File[i]
		if f.Name != w.name || f.Mode() != w.mode || f.Modified.Unix() != testEpoch {
			t.Errorf("entry %s with mode %o at %v, want %s with mode %o at %d", f.Name, f.Mode(), f.Modified, w.name, w.mode, testEpoch)
		}

		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}

		content, _ := io.ReadAll(r)
		r.Close()
		if string(content) != testFiles[filepath.Base(f.Name)] {
			t.Errorf("%s holds %q", f.Name, content)
		}
	}
}
