// Package export renderiza proyectos .dc.html a video, frame a frame y de
// forma determinística. Dos protocolos:
//
//   - Runtime om: el evento `data-om-seek-to-time-frame` con sync:true sobre
//     el `svg[data-om-exportable-video-with-duration-secs]` deja el DOM en el
//     frame exacto en cuanto dispatchEvent retorna; un screenshot de ese svg
//     es el frame.
//   - Composiciones CSS (sin svg exportable): se congelan todas las
//     animaciones con la Web Animations API (pause + currentTime) y se busca
//     cada frame ahí; la duración sale de los propios keyframes (el ciclo más
//     largo entre las animaciones infinitas, o el final de la más tardía de
//     las finitas).
//
// Los PNG se canalizan directo al stdin de ffmpeg.
package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const sel = `svg[data-om-exportable-video-with-duration-secs]`

// stageExpr localiza la raíz visual de una composición CSS: el lienzo marcado
// (convención de los proyectos dc), o el root donde monta el runtime.
const stageExpr = `(document.querySelector('[data-screen-label]') || document.getElementById('dc-root') || document.body)`

// capJS define cap(promesa, ms): un recurso de terceros que nunca responde —
// una fuente de fonts.googleapis.com tras un proxy corporativo, por ejemplo —
// deja `document.fonts.ready` colgado para siempre. Con el tope, el export
// sigue con la fuente de fallback en vez de morir en el arranque.
const capJS = `const cap = (p, ms) => Promise.race([Promise.resolve(p).catch(() => {}), new Promise(r => setTimeout(r, ms))]);`

// assetsWait es lo que se espera antes de capturar: fuentes e imágenes, cada
// una con su tope. Deja en `pending` qué quedó sin cargar — la familia de la
// fuente, no un "algo falló": saber si es la del CDN o una local del proyecto
// es la diferencia entre culpar a la red o a la carpeta.
const assetsWait = capJS + `
	await cap(document.fonts.ready, 10000);
	await cap(Promise.all([...document.images].map(i => i.decode().catch(() => {}))), 5000);
	const slowFonts = [...new Set([...document.fonts].filter(f => f.status === 'loading').map(f => f.family))];
	const slowImgs = [...document.images].filter(i => !i.complete).length;
	const pending = [
		...(slowFonts.length ? ['la fuente ' + slowFonts.slice(0, 3).join(', ')] : []),
		...(slowImgs ? [slowImgs + ' imagen(es)'] : []),
	].join(' y ');`

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
	Warn    string // aviso que no impide el export (fuentes de reserva, por ejemplo)
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

// assetWarn nombra lo que no cargó, para no dejar al usuario adivinando si el
// culpable es su red o la carpeta del proyecto.
func assetWarn(pending string) string {
	return "no cargó a tiempo " + pending + "; se exporta con las de reserva"
}

// hostRe saca los orígenes externos que referencian los archivos del proyecto,
// con su esquema: sondear en http lo que el proyecto pide por http evita dar
// por muerto un host que simplemente no habla https.
var hostRe = regexp.MustCompile(`(https?)://([a-zA-Z0-9.-]+)`)

// localHost distingue el servidor propio de dcx de un origen de verdad externo.
func localHost(h string) bool {
	return h == "localhost" || strings.HasPrefix(h, "127.") || h == "0.0.0.0" || !strings.Contains(h, ".")
}

// externalHosts lista los dominios que el proyecto va a pedir: los que salen
// de su .dc.html y de las hojas de estilo que trae al lado (el @import de
// Google Fonts del design system, por ejemplo), más el CDN del runtime.
func externalHosts(project string) []string {
	seen := map[string]bool{"https://unpkg.com": true}
	scan := func(p string) {
		b, err := os.ReadFile(p)
		if err != nil || len(b) > 4<<20 {
			return
		}
		for _, m := range hostRe.FindAllSubmatch(b, -1) {
			if host := string(m[2]); !localHost(host) {
				seen[string(m[1])+"://"+host] = true
			}
		}
	}
	scan(project)
	root := filepath.Dir(project)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && p != root {
			return fs.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".css") {
			scan(p)
		}
		return nil
	})
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// probeHosts averigua, antes de navegar, cuáles de esos dominios no responden.
// Importa porque Chrome bloquea la ejecución de scripts mientras espera una
// hoja de estilos remota: un dominio que traga la petición sin contestar (un
// proxy corporativo) congela la página entera hasta que vence el timeout de
// TCP. Detectarlos aquí cuesta segundos y permite bloquearlos.
// La sonda corre DENTRO del tab, no desde Go: Chrome usa el proxy del sistema,
// las políticas del equipo y su propio DNS, así que un curl desde Go puede
// llegar a un host al que la página nunca llegará (y al revés).
func probeHosts(ctx context.Context, origins []string) (dead []string) {
	var ask []string
	for _, o := range origins {
		if alive, known := probeCached(o); known {
			if !alive {
				dead = append(dead, hostOf(o))
			}
			continue
		}
		ask = append(ask, o)
	}
	if len(ask) > 0 {
		list, err := json.Marshal(ask)
		if err != nil {
			return dead
		}
		// En un tab aparte: evaluar en el tab del job antes de navegar deja su
		// contexto de ejecución viciado y el poll posterior nunca ve la página.
		tab, cancelTab := chromedp.NewContext(ctx)
		defer cancelTab()
		pctx, cancel := context.WithTimeout(tab, 12*time.Second)
		defer cancel()
		probe := `(async () => {
			const reach = async u => { const c = new AbortController(); const t = setTimeout(() => c.abort(), 5000);
				try { await fetch(u + '/', { mode: 'no-cors', cache: 'no-store', signal: c.signal }); return true; }
				catch { return false; } finally { clearTimeout(t); } };
			const origins = ` + string(list) + `;
			return Object.fromEntries(await Promise.all(origins.map(async o => [o, await reach(o)])));
		})()`
		alive := map[string]bool{}
		if err := chromedp.Run(pctx, chromedp.Navigate("about:blank"), chromedp.Evaluate(probe, &alive, awaitPromise)); err != nil {
			return dead // sin veredicto: mejor seguir que bloquear a ciegas
		}
		for origin, ok := range alive {
			probeStore(origin, ok)
			if !ok {
				dead = append(dead, hostOf(origin))
			}
		}
	}
	sort.Strings(dead)
	return slices.Compact(dead)
}

func hostOf(origin string) string {
	return strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
}

// El resultado de la sonda se reusa entre jobs: los proyectos de una carpeta
// comparten los mismos CDNs y no tiene sentido volver a esperar por cada uno.
var (
	probeMu  sync.Mutex
	probeMem = map[string]bool{}
)

func probeCached(origin string) (alive, known bool) {
	probeMu.Lock()
	defer probeMu.Unlock()
	alive, known = probeMem[origin]
	return alive, known
}

func probeStore(origin string, alive bool) {
	probeMu.Lock()
	probeMem[origin] = alive
	probeMu.Unlock()
}

// dropPending cancela las descargas que quedaron colgadas. Chrome bloquea el
// pintado mientras espera una hoja de estilos pendiente, así que sin esto cada
// frame paga la espera de un recurso que nunca va a llegar.
func dropPending(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = chromedp.Run(sctx, chromedp.ActionFunc(func(c context.Context) error {
		return page.StopLoading().Do(c)
	}))
}

// bootErr traduce un fallo de arranque a algo accionable: los timeouts casi
// nunca son "la página tardó", sino un recurso que nunca responde o un runtime
// que no montó, así que se inspecciona la página antes de rendirse.
func bootErr(tabCtx context.Context, err error) error {
	if errors.Is(err, chromedp.ErrPollingTimeout) || errors.Is(err, context.DeadlineExceeded) {
		msg := "la composición nunca terminó de cargar"
		if why := diagnoseBoot(tabCtx); why != "" {
			msg += ": " + why
		}
		return fmt.Errorf("arrancando la composición: %s", msg)
	}
	return fmt.Errorf("arrancando la composición: %w", err)
}

// diagnoseBoot inspecciona la página tras un boot fallido para explicar por
// qué no montó: sin acceso al CDN del runtime, React sin cargar, etc. Devuelve
// "" si no logra averiguarlo.
func diagnoseBoot(tabCtx context.Context) string {
	dctx, cancel := context.WithTimeout(tabCtx, 8*time.Second)
	defer cancel()
	var d struct {
		DcRoot  bool   `json:"dcRoot"`
		React   bool   `json:"react"`
		Anims   int    `json:"anims"`
		Support int    `json:"support"`
		Unpkg   bool   `json:"unpkg"`
		Fonts   string `json:"fonts"`
		GFonts  bool   `json:"gfonts"`
		Imgs    int    `json:"imgs"`
	}
	// Cada sonda va con AbortController: si la red cuelga (proxy que traga la
	// petición sin responder), el diagnóstico no puede colgarse también.
	probe := `(async () => {
		const head = async (u, ms, opts) => { const c = new AbortController(); const t = setTimeout(() => c.abort(), ms);
			try { const r = await fetch(u, { method: 'HEAD', cache: 'no-store', signal: c.signal, ...(opts || {}) }); return r.status || 200; } catch { return -1; } finally { clearTimeout(t); } };
		return {
			dcRoot: !!document.getElementById('dc-root'),
			react: !!window.React,
			anims: document.getAnimations().length,
			imgs: [...document.images].filter(i => !i.complete).length,
			fonts: document.fonts.status,
			support: await head('support.js', 5000),
			unpkg: (await head('https://unpkg.com/react@18.3.1/package.json', 6000, { mode: 'no-cors' })) !== -1,
			gfonts: (await head('https://fonts.googleapis.com/css2?family=DM+Sans', 6000, { mode: 'no-cors' })) !== -1,
		};
	})()`
	if err := chromedp.Run(dctx, chromedp.Evaluate(probe, &d, awaitPromise)); err != nil {
		return ""
	}
	switch {
	case d.Support != 200:
		return "no responde ./support.js junto al proyecto — el folder parece incompleto; copia la carpeta completa del export (support.js, _ds/, assets/, uploads/)"
	case !d.Unpkg:
		return "sin acceso a unpkg.com — el runtime dc carga React desde ese CDN y necesita internet"
	case !d.React:
		return "React no cargó desde unpkg.com (¿proxy o firewall bloqueando el CDN?)"
	case !d.DcRoot:
		return "el runtime dc no montó (¿el archivo abre bien en un browser normal?)"
	case !d.GFonts:
		return "fonts.googleapis.com no responde desde el navegador y el design system lo importa (¿proxy corporativo?) — dcx ya no se cuelga por eso: reintenta y exportará con las fuentes de reserva"
	case d.Fonts == "loading":
		return "hay fuentes que nunca terminan de cargar (red o proxy); reintenta y exportará con las de reserva"
	case d.Imgs > 0:
		return fmt.Sprintf("%d imagen(es) del proyecto no cargan — revisa que uploads/ y assets/ estén completos", d.Imgs)
	case d.Anims == 0:
		return "montó pero no expone svg exportable ni animaciones CSS"
	}
	return ""
}

func runJob(browserCtx, appCtx context.Context, job int, project string, opt Options, report func(Progress)) error {
	start := time.Now()
	fail := func(err error) error {
		report(Progress{Job: job, Stage: StageFailed, Err: err, Elapsed: time.Since(start)})
		return fmt.Errorf("%s: %w", filepath.Base(project), err)
	}
	report(Progress{Job: job, Stage: StageBooting})

	// El error más común al copiar proyectos: llevarse los .dc.html sin los
	// archivos de al lado. Sin sus scripts la página nunca monta, así que
	// mejor fallar al instante y con nombre que esperar el timeout del boot.
	if html, err := os.ReadFile(project); err == nil {
		for _, ref := range []string{"support.js", "image-slot.js"} {
			if bytes.Contains(html, []byte(ref)) {
				if _, err := os.Stat(filepath.Join(filepath.Dir(project), ref)); err != nil {
					return fail(fmt.Errorf("el proyecto referencia ./%s pero no está junto al archivo — copia la carpeta completa del export (support.js, _ds/, assets/, uploads/)", ref))
				}
			}
		}
	}

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
	// Lista cuando aparece el svg del protocolo om, o — composiciones CSS —
	// cuando el runtime ya montó y hay animaciones que manejar. El fondo por
	// defecto va transparente para que los presets con alpha lo conserven
	// donde la página no pinta.
	ready := `!!document.querySelector('` + sel + `') || (!!document.getElementById('dc-root') && document.getAnimations().length > 0)`

	// Los dominios que no contestan se bloquean antes de navegar: si no, la
	// página queda congelada esperándolos y ni siquiera arranca el runtime.
	blockActions := []chromedp.Action{}
	if dead := probeHosts(browserCtx, externalHosts(project)); len(dead) > 0 {
		if slices.Contains(dead, "unpkg.com") {
			return fail(errors.New("sin acceso a unpkg.com — el runtime dc carga React desde ese CDN; revisa internet, VPN, proxy o firewall"))
		}
		patterns := make([]*network.BlockPattern, 0, len(dead))
		for _, h := range dead {
			patterns = append(patterns, &network.BlockPattern{URLPattern: "*://" + h + "/*", Block: true})
		}
		blockActions = append(blockActions, network.Enable(), network.SetBlockedURLs().WithURLPatterns(patterns))
		report(Progress{Job: job, Stage: StageBooting, Warn: "sin acceso a " + strings.Join(dead, ", ") + "; se exporta con las fuentes de reserva"})
	}

	if err := chromedp.Run(bootCtx, append(blockActions,
		emulation.SetDeviceMetricsOverride(1400, 1400, float64(opt.Scale), false),
		emulation.SetDefaultBackgroundColorOverride().WithColor(&cdp.RGBA{R: 0, G: 0, B: 0, A: 0}),
		chromedp.Navigate(pageURL),
		chromedp.Poll(ready, nil, chromedp.WithPollingInterval(150*time.Millisecond)),
	)...); err != nil {
		if errors.Is(err, chromedp.ErrPollingTimeout) || errors.Is(err, context.DeadlineExceeded) {
			msg := "la composición nunca se montó"
			if why := diagnoseBoot(tabCtx); why != "" {
				msg += ": " + why
			}
			err = errors.New(msg)
		}
		return fail(bootErr(tabCtx, err))
	}
	var hasOM bool
	if err := chromedp.Run(bootCtx, chromedp.Evaluate(`!!document.querySelector('`+sel+`')`, &hasOM)); err != nil {
		return fail(bootErr(tabCtx, err))
	}
	if !hasOM {
		// Gracia corta por si el runtime om monta el svg después de que ya
		// corren animaciones; si no llega, es una composición CSS.
		graceCtx, cancelGrace := context.WithTimeout(tabCtx, 3*time.Second)
		if chromedp.Run(graceCtx, chromedp.Poll(`!!document.querySelector('`+sel+`')`, nil, chromedp.WithPollingInterval(100*time.Millisecond))) == nil {
			hasOM = true
		}
		cancelGrace()
	}

	var duration, seekFrom float64
	var box svgBox
	var syncSeek bool
	if hasOM {
		prep := `(async () => {` + assetsWait + `
			const el = document.querySelector('` + sel + `');
			el.style.transform = 'scale(1)';
			el.style.boxShadow = 'none';
			return pending;
		})()`
		var pending string
		if err := chromedp.Run(bootCtx, chromedp.Evaluate(prep, &pending, awaitPromise)); err != nil {
			return fail(bootErr(tabCtx, err))
		}
		if pending != "" {
			report(Progress{Job: job, Stage: StageBooting, Warn: assetWarn(pending)})
			dropPending(bootCtx)
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
			return fail(bootErr(tabCtx, err))
		}
	} else {
		// Composición CSS pura. El viewport se ajusta exacto al lienzo: el
		// fit() típico de estos proyectos (scale = min(vw/W, vh/H)) queda en 1
		// y el stage llena la ventana.
		prep := `(async () => {` + assetsWait + `
			const el = ` + stageExpr + `;
			return {pending: pending, w: el.offsetWidth, h: el.offsetHeight};
		})()`
		var probe struct {
			Pending string  `json:"pending"`
			W       float64 `json:"w"`
			H       float64 `json:"h"`
		}
		if err := chromedp.Run(bootCtx, chromedp.Evaluate(prep, &probe, awaitPromise)); err != nil {
			return fail(bootErr(tabCtx, err))
		}
		dims := svgBox{W: probe.W, H: probe.H}
		if probe.Pending != "" {
			report(Progress{Job: job, Stage: StageBooting, Warn: assetWarn(probe.Pending)})
			dropPending(bootCtx)
		}
		if dims.W < 1 || dims.H < 1 {
			return fail(errors.New("no pude medir el lienzo de la composición"))
		}
		// Congela el reloj (pause + seek 0) y resuelve la ventana a exportar.
		// Si el proyecto la declara — data-export-secs, o data-export-in /
		// data-export-out (segundos, en cualquier elemento) — esa manda; si
		// no, la duración se infiere de los keyframes: el ciclo más largo
		// entre animaciones infinitas, o el final de la más tardía de las
		// finitas.
		freeze := `(() => {
			const el = ` + stageExpr + `;
			if (Math.abs(el.getBoundingClientRect().width - el.offsetWidth) > 0.5) el.style.transform = 'scale(1)';
			let cycle = 0, finite = 0;
			for (const a of document.getAnimations()) {
				a.pause();
				a.currentTime = 0;
				const t = a.effect.getTiming();
				const dur = (typeof t.duration === 'number' ? t.duration : 0) / 1000;
				const delay = (t.delay || 0) / 1000;
				if (t.iterations === Infinity) cycle = Math.max(cycle, dur);
				else finite = Math.max(finite, delay + dur * (t.iterations || 1));
			}
			const attr = n => { const e = document.querySelector('[' + n + ']'); const v = e ? parseFloat(e.getAttribute(n)) : NaN; return isFinite(v) ? v : NaN; };
			const secs = attr('data-export-secs'), tin = attr('data-export-in'), tout = attr('data-export-out');
			const start = tin >= 0 ? tin : 0;
			let end = Math.max(cycle, finite);
			if (secs > 0) end = start + secs;
			if (tout > start) end = tout;
			return { start: start, len: Math.max(end - start, 0) };
		})()`
		var win struct {
			Start float64 `json:"start"`
			Len   float64 `json:"len"`
		}
		if err := chromedp.Run(bootCtx,
			emulation.SetDeviceMetricsOverride(int64(math.Round(dims.W)), int64(math.Round(dims.H)), float64(opt.Scale), false),
			chromedp.Evaluate(`(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() => r(true)))))()`, nil, awaitPromise),
			chromedp.Sleep(250*time.Millisecond), // ResizeObserver → re-fit del stage
			chromedp.Evaluate(freeze, &win),
			chromedp.Evaluate(`(() => { const r = `+stageExpr+`.getBoundingClientRect(); return {x: r.x, y: r.y, w: r.width, h: r.height}; })()`, &box),
		); err != nil {
			return fail(bootErr(tabCtx, err))
		}
		duration = win.Len
		seekFrom = win.Start
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
		var seek string
		if hasOM {
			seek = fmt.Sprintf(`document.querySelector('%s').dispatchEvent(new CustomEvent('data-om-seek-to-time-frame', { detail: { time: %g, sync: true } }))`, sel, t)
		} else {
			seek = fmt.Sprintf(`(() => { for (const a of document.getAnimations()) { a.pause(); a.currentTime = %g; } })()`, (seekFrom+t)*1000)
		}
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
