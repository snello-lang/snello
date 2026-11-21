// Copyright 2026 The Snello Programming Language. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Archive writes one archive of a release's directory, the same bytes
// wherever it runs: a tar.gz, or a zip for Windows, of the directory
// OUT/NAME, as OUT/NAME.tar.gz or OUT/NAME.zip, every entry under NAME/
// in the order of its name, with the time EPOCH, no owner, and one of
// two modes, 755 for the directory and the snello binary and 644 for
// the rest; the gzip header carries no name and no time, and both
// compress at one level. The tar and the zip are Go's own, so that no
// tar, gzip or zip of the system, and no zlib, has a say.
//
//	go run ./release/archive OUT NAME EPOCH tar
//	go run ./release/archive OUT NAME EPOCH zip
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// The arguments, the entries' modes and names, and the archives' forms.
const (
	usage       = "usage: go run ./release/archive OUT NAME EPOCH tar|zip"
	argCount    = 5
	dirMode     = 0o755
	binaryMode  = 0o755
	fileMode    = 0o644
	binaryName  = "snello"
	windowsName = "snello.exe"
	tarForm     = "tar"
	zipForm     = "zip"
	tarSuffix   = ".tar.gz"
	zipSuffix   = ".zip"
	pathSep     = "/"
	level       = flate.BestCompression
	exitFailure = 1
	decimal     = 10
	epochBits   = 64
)

func main() {
	if len(os.Args) != argCount {
		fail(usage)
	}

	out, name, form := os.Args[1], os.Args[2], os.Args[4]
	epoch, err := strconv.ParseInt(os.Args[3], decimal, epochBits)
	if err != nil {
		fail(usage + ": EPOCH is seconds since 1970")
	}

	root := filepath.Join(out, name)
	entries, err := os.ReadDir(root) // in the order of their names
	if err != nil {
		fail(err.Error())
	}

	when := time.Unix(epoch, 0).UTC()
	switch form {
	case tarForm:
		err = writeTar(filepath.Join(out, name+tarSuffix), root, name, entries, when)
	case zipForm:
		err = writeZip(filepath.Join(out, name+zipSuffix), root, name, entries, when)
	default:
		fail(usage)
	}

	if err != nil {
		fail(err.Error())
	}
}

// writeTar writes the tar.gz: the directory, then each file, in the
// USTAR form with no owner.
func writeTar(path, root, name string, entries []fs.DirEntry, when time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	gz, err := gzip.NewWriterLevel(f, level)
	if err != nil {
		return err
	}

	gz.ModTime = time.Time{} // no time in the header
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + pathSep, Mode: dirMode, ModTime: when, Format: tar.FormatUSTAR}); err != nil {
		return err
	}

	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return err
		}

		header := &tar.Header{Typeflag: tar.TypeReg, Name: name + pathSep + e.Name(), Mode: modeOf(e.Name()), Size: info.Size(), ModTime: when, Format: tar.FormatUSTAR}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if err := copyFile(tw, filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}

	for _, c := range []io.Closer{tw, gz, f} {
		if err := c.Close(); err != nil {
			return err
		}
	}

	return nil
}

// writeZip writes the zip: each file, deflated at the one level, with
// its mode as a Unix one wherever it is written.
func writeZip(path, root, name string, entries []fs.DirEntry, when time.Time) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}

	zw := zip.NewWriter(f)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, level) })
	for _, e := range entries {
		header := &zip.FileHeader{Name: name + pathSep + e.Name(), Method: zip.Deflate, Modified: when}
		header.SetMode(fs.FileMode(modeOf(e.Name())))
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}

		if err := copyFile(w, filepath.Join(root, e.Name())); err != nil {
			return err
		}
	}

	for _, c := range []io.Closer{zw, f} {
		if err := c.Close(); err != nil {
			return err
		}
	}

	return nil
}

// modeOf returns the mode an entry is archived with: the binary's, or
// a file's.
func modeOf(name string) int64 {
	if name == binaryName || name == windowsName {
		return binaryMode
	}

	return fileMode
}

// copyFile copies the file at path into w.
func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// fail prints why and exits.
func fail(why string) {
	fmt.Fprintln(os.Stderr, "release/archive: "+why)
	os.Exit(exitFailure)
}
