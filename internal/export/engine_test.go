package export

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestExternalHosts(t *testing.T) {
	dir := t.TempDir()
	project := filepath.Join(dir, "x.dc.html")
	// El proyecto pide una fuente por http y su propio servidor por localhost;
	// el design system importa Google Fonts desde un css vecino.
	if err := os.WriteFile(project, []byte(`
		<script src="http://cdn.ejemplo.com/lib.js"></script>
		<img src="http://127.0.0.1:8080/a.png">
		<a href="http://localhost/b">x</a>`), 0o644); err != nil {
		t.Fatal(err)
	}
	css := filepath.Join(dir, "_ds")
	if err := os.MkdirAll(css, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(css, "tokens.css"),
		[]byte(`@import url('https://fonts.googleapis.com/css2?family=DM+Sans');`), 0o644); err != nil {
		t.Fatal(err)
	}

	got := externalHosts(project)
	want := []string{"http://cdn.ejemplo.com", "https://fonts.googleapis.com", "https://unpkg.com"}
	if !slices.Equal(got, want) {
		t.Errorf("externalHosts() = %v, quiero %v", got, want)
	}
}

func TestLocalHost(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "0.0.0.0", "servidor"} {
		if !localHost(h) {
			t.Errorf("localHost(%q) = false, quiero true", h)
		}
	}
	for _, h := range []string{"unpkg.com", "fonts.googleapis.com", "10.255.255.1"} {
		if localHost(h) {
			t.Errorf("localHost(%q) = true, quiero false", h)
		}
	}
}

func TestHostOf(t *testing.T) {
	for origin, want := range map[string]string{
		"https://unpkg.com": "unpkg.com",
		"http://10.0.0.1":   "10.0.0.1",
	} {
		if got := hostOf(origin); got != want {
			t.Errorf("hostOf(%q) = %q, quiero %q", origin, got, want)
		}
	}
}
