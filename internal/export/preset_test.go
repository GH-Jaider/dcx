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
	if p.Ext != ".mov" {
		t.Fatalf("preset inesperado: %+v", p)
	}
	if _, err := PresetByName("nope"); err == nil {
		t.Fatal("esperaba error para preset desconocido")
	}
}

func TestPresetArgsHardwareFallback(t *testing.T) {
	p, _ := PresetByName("prores4444")

	args, hw := p.Args(Caps{})
	if hw || !slices.Contains(args, "prores_ks") {
		t.Fatalf("sin caps debe caer a software: hw=%v args=%v", hw, args)
	}
	args, hw = p.Args(Caps{ProResVT: true})
	if !hw || !slices.Contains(args, "prores_videotoolbox") || !slices.Contains(args, "4444") {
		t.Fatalf("con ProResVT debe usar hardware: hw=%v args=%v", hw, args)
	}

	h, _ := PresetByName("h264")
	args, hw = h.Args(Caps{H264VT: true})
	if !hw || !slices.Contains(args, "h264_videotoolbox") {
		t.Fatalf("h264 con H264VT debe usar hardware: %v", args)
	}
}

func TestPresetSupported(t *testing.T) {
	ha, _ := PresetByName("hevc-alpha")
	if ha.Supported(Caps{}) {
		t.Fatal("hevc-alpha sin VideoToolbox no debe estar soportado")
	}
	wa, _ := PresetByName("webm-alpha")
	if wa.Supported(Caps{}) || !wa.Supported(Caps{VP9: true}) {
		t.Fatal("webm-alpha debe depender de libvpx-vp9")
	}
	seq, _ := PresetByName("png-seq")
	if !seq.Supported(Caps{}) {
		t.Fatal("png-seq debe estar siempre soportado")
	}
	p4, _ := PresetByName("prores4444")
	if !p4.Supported(Caps{}) {
		t.Fatal("prores4444 debe soportarse por software sin caps")
	}
}

func TestFfmpegArgsSeq(t *testing.T) {
	seq, _ := PresetByName("png-seq")
	opt := Options{FPS: 30, Preset: seq}
	args, _ := ffmpegArgs(opt, "/salida/Ad.png-seq")
	last := args[len(args)-1]
	if last != filepath.Join("/salida/Ad.png-seq", "f%05d.png") {
		t.Fatalf("patrón de secuencia inesperado: %s", last)
	}
	if !slices.Contains(args, "copy") {
		t.Fatalf("png-seq debe usar stream copy: %v", args)
	}
	if got := OutputPath("/proj/Ad.dc.html", opt); got != filepath.Join("/proj", "Ad.png-seq") {
		t.Fatalf("OutputPath de secuencia: %s", got)
	}
}

func TestFfmpegArgs(t *testing.T) {
	opt := Options{FPS: 30, Preset: presets[0]}
	args, hw := ffmpegArgs(opt, "/tmp/out.mov")
	if hw {
		t.Fatal("sin caps no debe reportar hardware")
	}
	if args[len(args)-1] != "/tmp/out.mov" {
		t.Fatalf("el destino debe ir al final: %v", args)
	}
	for _, want := range []string{"image2pipe", "30", "prores_ks", "4444"} {
		if !slices.Contains(args, want) {
			t.Fatalf("faltó %q en %v", want, args)
		}
	}
	opt.Caps = Caps{ProResVT: true}
	args, hw = ffmpegArgs(opt, "/tmp/out.mov")
	if !hw || !slices.Contains(args, "prores_videotoolbox") {
		t.Fatalf("con caps debe usar videotoolbox: %v", args)
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
