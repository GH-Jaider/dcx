// dcx exporta proyectos .dc.html a video (ProRes 4444 por defecto) con una
// TUI para lanzar varios en paralelo.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"

	"github.com/GH-Jaider/dcx/internal/browser"
	"github.com/GH-Jaider/dcx/internal/discover"
	"github.com/GH-Jaider/dcx/internal/export"
	"github.com/GH-Jaider/dcx/internal/ui"
)

func main() {
	fps := flag.Int("fps", 30, "frames por segundo")
	scale := flag.Int("scale", 2, "factor de resolución (2 = lienzo 1080 sale a 2160)")
	presetName := flag.String("preset", "prores4444", "prores4444 | prores422hq | prores422 | h264 | hevc | hevc-alpha | webm-alpha | png-seq")
	workers := flag.Int("workers", 3, "exports en paralelo")
	maxSeconds := flag.Float64("max-seconds", 0, "corta el export a N segundos (pruebas)")
	browserPath := flag.String("browser", "", "ruta al binario del navegador (default: autodetectar; env DCX_BROWSER)")
	downloadBrowser := flag.Bool("download-browser", false, "descarga (si hace falta) y usa el chrome-headless-shell pineado")
	noDownload := flag.Bool("no-download", false, "nunca descargar el navegador (falla si no hay ninguno)")
	outDir := flag.String("out-dir", "", "carpeta de salida (default: junto a cada proyecto)")
	plain := flag.Bool("plain", false, "sin TUI: logs planos (se activa solo si no hay terminal)")
	showVersion := flag.Bool("version", false, "imprime la versión compilada y sale")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "uso: dcx [flags] [archivo.dc.html | carpeta]...\n\nSin argumentos busca proyectos .dc.html en el directorio actual.\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVersion {
		fmt.Println(versionString())
		return
	}
	if *downloadBrowser && *noDownload {
		fatal(fmt.Errorf("--download-browser y --no-download son contradictorios"))
	}

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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := resolveBrowser(ctx, *browserPath, *downloadBrowser, *noDownload)
	if err != nil {
		fatal(err)
	}
	caps := export.DetectCaps(ctx)
	if !preset.Supported(caps) {
		fatal(fmt.Errorf("el preset %q necesita encoders que tu ffmpeg no trae (hevc-alpha, por ejemplo, requiere VideoToolbox en Apple Silicon)", preset.Name))
	}

	if *outDir != "" {
		if err := os.MkdirAll(*outDir, 0o755); err != nil {
			fatal(fmt.Errorf("no pude crear la carpeta de salida: %w", err))
		}
	}

	opt := export.Options{
		FPS:         *fps,
		Scale:       *scale,
		Preset:      preset,
		Caps:        caps,
		Workers:     *workers,
		MaxSeconds:  *maxSeconds,
		BrowserPath: res.Path,
		OutDir:      *outDir,
	}

	if *plain || !isTTY() {
		fmt.Printf("motor: %s · encoder: %s\n", res.Source, encoderLabel(preset, caps))
		if err := runPlain(ctx, files, opt); err != nil {
			fatal(err)
		}
		return
	}
	if err := ui.Run(files, opt, fmt.Sprintf("motor: %s", res.Source)); err != nil {
		fatal(err)
	}
}

// resolveBrowser aplica el orden flag/env → headless-shell cacheado →
// navegador del sistema → descarga (con consentimiento en TTY, o con
// --download-browser en scripts).
func resolveBrowser(ctx context.Context, flagPath string, download, noDownload bool) (browser.Resolution, error) {
	explicit := flagPath
	if explicit == "" {
		explicit = os.Getenv("DCX_BROWSER")
	}
	if download {
		if explicit != "" {
			return browser.Resolution{}, fmt.Errorf("--download-browser no se puede combinar con --browser ni DCX_BROWSER")
		}
		path, err := downloadShell(ctx)
		if err != nil {
			return browser.Resolution{}, err
		}
		return browser.Resolution{Path: path, Source: browser.SourceCache}, nil
	}
	res, err := browser.Resolve(explicit)
	if err != nil || res.Path != "" {
		return res, err
	}
	if noDownload {
		return res, fmt.Errorf("no encontré ningún navegador; instala Chrome/Chromium o quita --no-download")
	}
	if !isTTY() || !stdinTTY() {
		return res, fmt.Errorf("no encontré ningún navegador; corre con --download-browser o --browser /ruta")
	}
	msg := fmt.Sprintf("dcx necesita un motor de render. ¿Descargar chrome-headless-shell %s (%s, una sola vez, a la caché de usuario)? [Y/n] ", browser.PinnedVersion, browser.DownloadSize)
	if !promptYes(ctx, msg) {
		return res, fmt.Errorf("cancelado; instala Chrome o corre con --browser /ruta/al/navegador")
	}
	path, err := downloadShell(ctx)
	if err != nil {
		return browser.Resolution{}, err
	}
	return browser.Resolution{Path: path, Source: browser.SourceCache}, nil
}

// promptYes pregunta por stderr y lee stdin sin bloquear la cancelación:
// Ctrl+C (ctx), EOF o error de lectura cuentan como "no".
func promptYes(ctx context.Context, msg string) bool {
	fmt.Fprint(os.Stderr, msg)
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		ch <- answer{line, err}
	}()
	select {
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr)
		return false
	case a := <-ch:
		if a.err != nil {
			return false
		}
		line := strings.ToLower(strings.TrimSpace(a.line))
		return line == "" || line == "y" || line == "s" || line == "yes" || line == "si" || line == "sí"
	}
}

func downloadShell(ctx context.Context) (string, error) {
	lastPct := -1
	path, err := browser.Download(ctx, func(done, total int64) {
		if total <= 0 {
			return
		}
		if pct := int(done * 100 / total); pct/10 > lastPct/10 {
			lastPct = pct
			fmt.Fprintf(os.Stderr, "descargando chrome-headless-shell %s… %d%%\n", browser.PinnedVersion, pct)
		}
	})
	if err != nil {
		return "", fmt.Errorf("descargando el navegador: %w", err)
	}
	return path, nil
}

func encoderLabel(p export.Preset, c export.Caps) string {
	if _, hw := p.Args(c); hw {
		return p.Name + " (videotoolbox)"
	}
	return p.Name + " (software)"
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func stdinTTY() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runPlain exporta sin TUI, imprimiendo hitos por job (útil para scripts/CI).
func runPlain(ctx context.Context, files []string, opt export.Options) error {
	var mu sync.Mutex
	lastPct := make([]int, len(files))
	report := func(p export.Progress) {
		mu.Lock()
		defer mu.Unlock()
		name := files[p.Job]
		if p.Warn != "" {
			fmt.Printf("[%d] %s: aviso: %s\n", p.Job, name, p.Warn)
		}
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

// versionString arma la versión desde los datos que Go estampa al compilar
// dentro de un repo git. Sirve para saber, a distancia, si una máquina corre
// un binario viejo.
func versionString() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dcx (versión desconocida)"
	}
	var rev, when, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				rev = s.Value[:7]
			} else {
				rev = s.Value
			}
		case "vcs.time":
			when = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = " (con cambios locales)"
			}
		}
	}
	if rev == "" {
		return "dcx (compilado fuera del repo git)"
	}
	return fmt.Sprintf("dcx %s · %s%s", rev, when, dirty)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "dcx:", err)
	os.Exit(1)
}
