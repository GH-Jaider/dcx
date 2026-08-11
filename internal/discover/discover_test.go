package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestExpand(t *testing.T) {
	root := t.TempDir()
	touch(t, filepath.Join(root, "a", "Uno.dc.html"))
	touch(t, filepath.Join(root, "a", "notas.html"))
	touch(t, filepath.Join(root, "b", "Dos.dc.html"))
	touch(t, filepath.Join(root, "node_modules", "Oculto.dc.html"))
	touch(t, filepath.Join(root, ".cache", "Oculto.dc.html"))

	got, err := Expand([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("esperaba 2, obtuve %v", got)
	}
	for _, p := range got {
		if strings.Contains(p, "node_modules") || strings.Contains(p, ".cache") {
			t.Fatalf("no debía entrar a %s", p)
		}
	}

	// archivo directo + duplicado vía carpeta = una sola vez
	got, err = Expand([]string{filepath.Join(root, "a", "Uno.dc.html"), root})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("dedupe falló: %v", got)
	}

	if _, err := Expand([]string{filepath.Join(root, "a", "notas.html")}); err == nil {
		t.Fatal("esperaba error para un archivo que no es .dc.html")
	}
}
