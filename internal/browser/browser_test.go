package browser

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlatformID(t *testing.T) {
	p, err := platformID()
	if err != nil {
		t.Skipf("plataforma de test sin build: %v", err)
	}
	if p == "" {
		t.Fatal("platformID vacío sin error")
	}
}

func TestZipURL(t *testing.T) {
	got := zipURL("151.0.7922.77", "mac-arm64")
	want := "https://storage.googleapis.com/chrome-for-testing-public/151.0.7922.77/mac-arm64/chrome-headless-shell-mac-arm64.zip"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestResolveExplicitMissing(t *testing.T) {
	if _, err := Resolve(filepath.Join(t.TempDir(), "no-existe")); err == nil {
		t.Fatal("esperaba error para ruta explícita inexistente")
	}
}

func writeZip(t *testing.T, entries map[string]struct {
	body string
	mode os.FileMode
}) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, e := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(e.mode)
		fw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnzipPreservesModeAndNesting(t *testing.T) {
	zp := writeZip(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"top/bin":       {"#!/bin/sh\n", 0o755},
		"top/sub/d.dat": {"data", 0o644},
	})
	dest := t.TempDir()
	if err := unzip(zp, dest); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dest, "top", "bin"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("se perdió el bit ejecutable: %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(dest, "top", "sub", "d.dat")); err != nil {
		t.Fatal(err)
	}
}

func TestUnzipRejectsZipSlip(t *testing.T) {
	zp := writeZip(t, map[string]struct {
		body string
		mode os.FileMode
	}{
		"../evil": {"x", 0o644},
	})
	err := unzip(zp, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "fuera del destino") {
		t.Fatalf("esperaba error de zip-slip, obtuve %v", err)
	}
}
