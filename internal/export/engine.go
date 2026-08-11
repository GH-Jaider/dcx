// Package export renderiza proyectos .dc.html a video, frame a frame y de
// forma determinística, usando el protocolo de export del runtime dc: el
// evento `data-om-seek-to-time-frame` con sync:true sobre el
// `svg[data-om-exportable-video-with-duration-secs]` deja el DOM en el frame
// exacto en cuanto dispatchEvent retorna; un screenshot de ese svg es el
// frame. Los PNG se canalizan directo al stdin de ffmpeg.
package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const sel = `svg[data-om-exportable-video-with-duration-secs]`

// Stage es la fase en la que está un job.
type Stage int

const (
	StagePending Stage = iota
	StageBooting
	StageRendering
	StageEncoding
	StageDone
	StageFailed
)

func (s Stage) String() string {
	switch s {
	case StageBooting:
		return "arrancando"
	case StageRendering:
		return "renderizando"
	case StageEncoding:
		return "codificando"
	case StageDone:
		return "listo"
	case StageFailed:
		return "error"
	default:
		return "en cola"
	}
}

// Options controla el export.
type Options struct {
	FPS         int
	Scale       int // factor de resolución: 2 = un lienzo 1080 sale a 2160
	Preset      Preset
	Caps        Caps // encoders por hardware del ffmpeg local (DetectCaps)
	Workers     int
	MaxSeconds  float64 // >0 corta el export (para pruebas)
	BrowserPath string  // resuelto por internal/browser; vacío = que chromedp busque
	OutDir      string  // vacío = junto a cada proyecto
}

// Progress es un reporte de avance de un job (se emite desde varias goroutines).
type Progress struct {
	Job     int
	Stage   Stage
	Frame   int
	Frames  int
	Out     string
	Bytes   int64
	Elapsed time.Duration
	Err     error
}

// OutputPath calcula el destino de un proyecto con las opciones dadas.
func OutputPath(project string, opt Options) string {
	name := strings.TrimSuffix(filepath.Base(project), ".dc.html")
	dir := opt.OutDir
	if dir == "" {
		dir = filepath.Dir(project)
	}
	return filepath.Join(dir, name+"."+opt.Preset.Name+opt.Preset.Ext)
}

// Run exporta todos los proyectos en paralelo (opt.Workers a la vez)
// compartiendo un solo Chrome headless: cada job usa su propio tab, su propio
// servidor HTTP efímero y su propio ffmpeg. Devuelve el primer error (los
// demás jobs siguen y reportan el suyo por report).
func Run(ctx context.Context, projects []string, opt Options, report func(Progress)) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errors.New("ffmpeg no está en el PATH (brew install ffmpeg)")
	}
	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	if opt.BrowserPath != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opt.BrowserPath))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	if err := chromedp.Run(browserCtx); err != nil {
		return fmt.Errorf("no pude lanzar Chrome: %w", err)
	}

	workers := max(opt.Workers, 1)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for i, p := range projects {
		wg.Add(1)
		go func(job int, project string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				report(Progress{Job: job, Stage: StageFailed, Err: ctx.Err()})
				return
			}
			if err := runJob(browserCtx, ctx, job, project, opt, report); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}(i, p)
	}
	wg.Wait()
	return firstErr
}

type svgBox struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func awaitPromise(p *cdpruntime.EvaluateParams) *cdpruntime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

func runJob(browserCtx, appCtx context.Context, job int, project string, opt Options, report func(Progress)) error {
	start := time.Now()
	fail := func(err error) error {
		report(Progress{Job: job, Stage: StageFailed, Err: err, Elapsed: time.Since(start)})
		return fmt.Errorf("%s: %w", filepath.Base(project), err)
	}
	report(Progress{Job: job, Stage: StageBooting})

	// Servidor efímero del directorio del proyecto (support.js, jsx, uploads…).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	srv := &http.Server{Handler: http.FileServer(http.Dir(filepath.Dir(project)))}
	go srv.Serve(ln) //nolint:errcheck // se cierra con srv.Close
	defer srv.Close()
	pageURL := fmt.Sprintf("http://%s/%s", ln.Addr(), url.PathEscape(filepath.Base(project)))

	// Tab propio dentro del Chrome compartido.
	tabCtx, cancelTab := chromedp.NewContext(browserCtx)
	defer cancelTab()

	bootCtx, cancelBoot := context.WithTimeout(tabCtx, 2*time.Minute)
	defer cancelBoot()
	prep := `(async () => {
		await document.fonts.ready;
		const el = document.querySelector('` + sel + `');
		el.style.transform = 'scale(1)';
		el.style.boxShadow = 'none';
		return true;
	})()`
	var duration float64
	var box svgBox
	var syncSeek bool
	if err := chromedp.Run(bootCtx,
		emulation.SetDeviceMetricsOverride(1400, 1400, float64(opt.Scale), false),
		chromedp.Navigate(pageURL),
		chromedp.Poll(`!!document.querySelector('`+sel+`')`, nil, chromedp.WithPollingInterval(150*time.Millisecond)),
		chromedp.Evaluate(prep, nil, awaitPromise),
	); err != nil {
		return fail(fmt.Errorf("arrancando la composición: %w", err))
	}
	// El runtime inyecta las @font-face dentro del svg y lo marca; esperarlo
	// evita frames tempranos con fuentes de fallback. Best-effort con timeout
	// corto: un runtime viejo sin el atributo no debe pagar una espera larga
	// (document.fonts.ready ya se esperó arriba).
	fontsCtx, cancelFonts := context.WithTimeout(tabCtx, 4*time.Second)
	_ = chromedp.Run(fontsCtx, chromedp.Poll(
		`document.querySelector('`+sel+`').getAttribute('data-om-fonts-inlined') === 'true'`,
		nil, chromedp.WithPollingInterval(100*time.Millisecond)))
	cancelFonts()
	if err := chromedp.Run(bootCtx,
		chromedp.Sleep(150*time.Millisecond),
		chromedp.Evaluate(`+document.querySelector('`+sel+`').getAttribute('data-om-exportable-video-with-duration-secs')`, &duration),
		chromedp.Evaluate(`document.querySelector('`+sel+`').getAttribute('data-om-sync-seek') === 'true'`, &syncSeek),
		chromedp.Evaluate(`(() => { const r = document.querySelector('`+sel+`').getBoundingClientRect(); return {x: r.x, y: r.y, w: r.width, h: r.height}; })()`, &box),
	); err != nil {
		return fail(fmt.Errorf("arrancando la composición: %w", err))
	}
	if opt.MaxSeconds > 0 {
		duration = math.Min(duration, opt.MaxSeconds)
	}
	frames := int(math.Round(duration * float64(opt.FPS)))
	if frames < 1 {
		return fail(errors.New("la composición reporta duración 0"))
	}

	out := OutputPath(project, opt)
	if opt.Preset.Seq {
		if err := os.MkdirAll(out, 0o755); err != nil {
			return fail(err)
		}
	}
	ffArgs, _ := ffmpegArgs(opt, out)
	var ffErr bytes.Buffer
	ff := exec.CommandContext(appCtx, "ffmpeg", ffArgs...)
	ff.Stderr = &ffErr
	stdin, err := ff.StdinPipe()
	if err != nil {
		return fail(err)
	}
	if err := ff.Start(); err != nil {
		return fail(err)
	}
	ffFail := func(err error) error {
		stdin.Close()
		ff.Wait() //nolint:errcheck
		if s := strings.TrimSpace(ffErr.String()); s != "" {
			return fail(fmt.Errorf("%w (ffmpeg: %s)", err, s))
		}
		return fail(err)
	}

	clip := &page.Viewport{X: box.X, Y: box.Y, Width: box.W, Height: box.H, Scale: 1}
	// Sin flushSync anunciado (data-om-sync-seek) el commit del seek es
	// asíncrono: hay que dejar pasar dos frames de compositor antes de capturar.
	const rafSettle = `(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => r(true)))))()`
	for i := 0; i < frames; i++ {
		if err := appCtx.Err(); err != nil {
			return ffFail(err)
		}
		t := math.Min(float64(i)/float64(opt.FPS), duration-1e-4)
		seek := fmt.Sprintf(`document.querySelector('%s').dispatchEvent(new CustomEvent('data-om-seek-to-time-frame', { detail: { time: %g, sync: true } }))`, sel, t)
		actions := []chromedp.Action{chromedp.Evaluate(seek, nil)}
		if !syncSeek {
			actions = append(actions, chromedp.Evaluate(rafSettle, nil, awaitPromise))
		}
		var shot []byte
		actions = append(actions, chromedp.ActionFunc(func(c context.Context) error {
			var err error
			shot, err = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithOptimizeForSpeed(true).
				WithClip(clip).
				Do(c)
			return err
		}))
		if err := chromedp.Run(tabCtx, actions...); err != nil {
			return ffFail(err)
		}
		if _, err := stdin.Write(shot); err != nil {
			return ffFail(fmt.Errorf("escribiendo frame a ffmpeg: %w", err))
		}
		report(Progress{Job: job, Stage: StageRendering, Frame: i + 1, Frames: frames, Elapsed: time.Since(start)})
	}

	report(Progress{Job: job, Stage: StageEncoding, Frame: frames, Frames: frames, Elapsed: time.Since(start)})
	stdin.Close()
	if err := ff.Wait(); err != nil {
		if s := strings.TrimSpace(ffErr.String()); s != "" {
			return fail(fmt.Errorf("ffmpeg: %s", s))
		}
		return fail(fmt.Errorf("ffmpeg: %w", err))
	}
	report(Progress{Job: job, Stage: StageDone, Frame: frames, Frames: frames, Out: out, Bytes: outputSize(out), Elapsed: time.Since(start)})
	return nil
}

// outputSize mide el resultado: tamaño del archivo, o suma del directorio
// para secuencias.
func outputSize(out string) int64 {
	fi, err := os.Stat(out)
	if err != nil {
		return 0
	}
	if !fi.IsDir() {
		return fi.Size()
	}
	var total int64
	entries, err := os.ReadDir(out)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}
