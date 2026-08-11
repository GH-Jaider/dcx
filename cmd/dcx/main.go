// dcx exporta proyectos .dc.html a video (ProRes 4444 por defecto) con una
// TUI para lanzar varios en paralelo.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/GH-Jaider/dcx/internal/discover"
	"github.com/GH-Jaider/dcx/internal/export"
	"github.com/GH-Jaider/dcx/internal/ui"
)

func main() {
	fps := flag.Int("fps", 30, "frames por segundo")
	scale := flag.Int("scale", 2, "factor de resolución (2 = lienzo 1080 sale a 2160)")
	presetName := flag.String("preset", "prores4444", "prores4444 | prores422hq | h264")
	workers := flag.Int("workers", 3, "exports en paralelo")
	maxSeconds := flag.Float64("max-seconds", 0, "corta el export a N segundos (pruebas)")
	chrome := flag.String("chrome", "", "ruta al binario de Chrome (default: autodetectar)")
	outDir := flag.String("out-dir", "", "carpeta de salida (default: junto a cada proyecto)")
	plain := flag.Bool("plain", false, "sin TUI: logs planos (se activa solo si no hay terminal)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "uso: dcx [flags] [archivo.dc.html | carpeta]...\n\nSin argumentos busca proyectos .dc.html en el directorio actual.\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		args = []string{"."}
	}
	files, err := discover.Expand(args)
	if err != nil {
		fatal(err)
	}
	if len(files) == 0 {
		fatal(fmt.Errorf("no encontré ningún .dc.html en %v", args))
	}
	preset, err := export.PresetByName(*presetName)
	if err != nil {
		fatal(err)
	}
	opt := export.Options{
		FPS:        *fps,
		Scale:      *scale,
		Preset:     preset,
		Workers:    *workers,
		MaxSeconds: *maxSeconds,
		ChromePath: *chrome,
		OutDir:     *outDir,
	}

	if *plain || !isTTY() {
		if err := runPlain(files, opt); err != nil {
			fatal(err)
		}
		return
	}
	if err := ui.Run(files, opt); err != nil {
		fatal(err)
	}
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runPlain exporta sin TUI, imprimiendo hitos por job (útil para scripts/CI).
func runPlain(files []string, opt export.Options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var mu sync.Mutex
	lastPct := make([]int, len(files))
	report := func(p export.Progress) {
		mu.Lock()
		defer mu.Unlock()
		name := files[p.Job]
		switch p.Stage {
		case export.StageRendering:
			pct := p.Frame * 100 / max(p.Frames, 1)
			if pct/10 > lastPct[p.Job]/10 || p.Frame == p.Frames {
				lastPct[p.Job] = pct
				fmt.Printf("[%d] %s: %d%% (%d/%d)\n", p.Job, name, pct, p.Frame, p.Frames)
			}
		case export.StageDone:
			fmt.Printf("[%d] %s: listo → %s (%.0f MB, %s)\n", p.Job, name, p.Out, float64(p.Bytes)/1e6, p.Elapsed.Round(1e9))
		case export.StageFailed:
			fmt.Printf("[%d] %s: ERROR: %v\n", p.Job, name, p.Err)
		default:
			fmt.Printf("[%d] %s: %s\n", p.Job, name, p.Stage)
		}
	}
	return export.Run(ctx, files, opt, report)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dcx:", err)
	os.Exit(1)
}
