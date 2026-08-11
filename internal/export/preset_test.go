package export

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestPresetByName(t *testing.T) {
	p, err := PresetByName("prores4444")
	if err != nil {
		t.Fatal(err)
	}
	if p.Ext != ".mov" || !slices.Contains(p.Args, "prores_ks") {
		t.Fatalf("preset inesperado: %+v", p)
	}
	if _, err := PresetByName("nope"); err == nil {
		t.Fatal("esperaba error para preset desconocido")
	}
}

func TestFfmpegArgs(t *testing.T) {
	opt := Options{FPS: 30, Preset: presets[0]}
	args := ffmpegArgs(opt, "/tmp/out.mov")
	if args[len(args)-1] != "/tmp/out.mov" {
		t.Fatalf("el destino debe ir al final: %v", args)
	}
	for _, want := range []string{"image2pipe", "30", "prores_ks", "4444"} {
		if !slices.Contains(args, want) {
			t.Fatalf("faltó %q en %v", want, args)
		}
	}
}

func TestOutputPath(t *testing.T) {
	opt := Options{Preset: presets[0]}
	got := OutputPath("/proj/Mi Ad.dc.html", opt)
	if got != filepath.Join("/proj", "Mi Ad.prores4444.mov") {
		t.Fatalf("ruta inesperada: %s", got)
	}
	opt.OutDir = "/salida"
	if got := OutputPath("/proj/Mi Ad.dc.html", opt); got != filepath.Join("/salida", "Mi Ad.prores4444.mov") {
		t.Fatalf("con OutDir: %s", got)
	}
}
