// Package ui es la TUI (Bubble Tea v2) de dcx: selector de composiciones con
// ajustes en vivo, dashboard de progreso paralelo y resumen final. El diseño
// sigue el vocabulario visual del ecosistema Charm: rail de cursor ┃, radios
// ◉/○, un solo glifo de estado coloreado, badges dark-on-green y una rampa
// de grises adaptativa a terminal clara/oscura.
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/GH-Jaider/dcx/internal/export"
)

type phase int

const (
	phasePick phase = iota
	phaseRun
	phaseDone
)

var (
	fpsOptions   = []int{24, 25, 30, 60}
	scaleOptions = []int{1, 2, 3}
	workerRange  = []int{1, 2, 3, 4, 6, 8}
)

type jobView struct {
	path    string
	stage   export.Stage
	frame   int
	frames  int
	out     string
	bytes   int64
	elapsed time.Duration
	err     error
	warn    string
}

type progressMsg export.Progress

type engineDoneMsg struct{ err error }

type flashExpiredMsg struct{}

// keymaps por vista (bubbles/help los renderiza).
var (
	kMove   = key.NewBinding(key.WithKeys("up", "down", "k", "j"), key.WithHelp("↑↓", "mover"))
	kToggle = key.NewBinding(key.WithKeys("space"), key.WithHelp("espacio", "marcar"))
	kAll    = key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "todo"))
	kAdjust = key.NewBinding(key.WithKeys("f", "s", "p", "w"), key.WithHelp("f/s/p/w", "ajustes"))
	kStart  = key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "exportar"))
	kQuit   = key.NewBinding(key.WithKeys("q", "esc", "ctrl+c"), key.WithHelp("q", "salir"))
	kCancel = key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "cancela"))
	kExit   = key.NewBinding(key.WithKeys("enter", "q", "esc"), key.WithHelp("enter", "salir"))
)

type helpKeys []key.Binding

func (h helpKeys) ShortHelp() []key.Binding  { return h }
func (h helpKeys) FullHelp() [][]key.Binding { return [][]key.Binding{h} }

// Model es el modelo raíz de la TUI.
type Model struct {
	files    []string
	selected map[int]bool
	cursor   int
	meta     string

	opt        export.Options
	fpsIdx     int
	scaleIdx   int
	presetIdx  int
	workersIdx int

	phase     phase
	jobs      []jobView
	started   time.Time
	ch        chan export.Progress
	done      chan error
	cancel    context.CancelFunc
	canceling bool
	engineErr error
	belled    bool

	pal    palette
	bar    progress.Model
	spin   spinner.Model
	hlp    help.Model
	width  int
	height int

	flashKey   string
	flashUntil time.Time
}

// New crea el modelo con los proyectos descubiertos y las opciones iniciales.
func New(files []string, opt export.Options, meta string) Model {
	m := Model{
		files:    files,
		selected: map[int]bool{},
		opt:      opt,
		meta:     meta,
		hlp:      help.New(),
		width:    100,
		height:   32,
	}
	for i := range files {
		m.selected[i] = true
	}
	m.fpsIdx = indexOf(fpsOptions, opt.FPS)
	m.scaleIdx = indexOf(scaleOptions, opt.Scale)
	m.workersIdx = indexOf(workerRange, opt.Workers)
	for i, p := range export.Presets() {
		if p.Name == opt.Preset.Name {
			m.presetIdx = i
		}
	}
	m.applyTheme(true)
	return m
}

// applyTheme reconstruye paleta y componentes al saber el fondo del terminal.
func (m *Model) applyTheme(isDark bool) {
	m.pal = newPalette(isDark)
	stops, empty := m.pal.barColors()
	m.bar = progress.New(progress.WithColors(stops...), progress.WithoutPercentage())
	m.bar.EmptyColor = empty
	m.spin = spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(m.pal.glyphOn))
	m.hlp.Styles = help.DefaultStyles(isDark)
}

func indexOf(xs []int, v int) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return 0
}

// Run lanza el programa Bubble Tea y devuelve el error del export (si hubo).
func Run(files []string, opt export.Options, meta string) error {
	final, err := tea.NewProgram(New(files, opt, meta)).Run()
	if err != nil {
		return err
	}
	if m, ok := final.(Model); ok {
		return m.engineErr
	}
	return nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(tea.RequestBackgroundColor, m.spin.Tick)
}

func listen(ch <-chan export.Progress, done <-chan error) tea.Cmd {
	return func() tea.Msg {
		select {
		case p, ok := <-ch:
			if !ok {
				return engineDoneMsg{err: <-done}
			}
			return progressMsg(p)
		case err := <-done:
			return engineDoneMsg{err: err}
		}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		m.applyTheme(msg.IsDark())
		return m, m.spin.Tick

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case flashExpiredMsg:
		return m, nil

	case progressMsg:
		p := export.Progress(msg)
		if p.Job >= 0 && p.Job < len(m.jobs) {
			j := &m.jobs[p.Job]
			j.stage, j.frame, j.frames, j.elapsed = p.Stage, p.Frame, p.Frames, p.Elapsed
			if p.Out != "" {
				j.out, j.bytes = p.Out, p.Bytes
			}
			if p.Err != nil {
				j.err = p.Err
			}
			if p.Warn != "" {
				j.warn = p.Warn
			}
		}
		return m, listen(m.ch, m.done)

	case engineDoneMsg:
		m.engineErr = msg.err
		m.phase = phaseDone
		if !m.belled {
			m.belled = true
			fmt.Fprint(os.Stderr, "\a")
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phasePick:
		return m.handlePickKey(msg)
	case phaseRun:
		if key.Matches(msg, kCancel) {
			return m.cancelRun()
		}
	case phaseDone:
		if key.Matches(msg, kExit) || key.Matches(msg, kQuit) {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) cancelRun() (tea.Model, tea.Cmd) {
	if m.cancel != nil && !m.canceling {
		m.canceling = true
		m.cancel()
	}
	return m, nil
}

func (m Model) handlePickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, kQuit):
		return m, tea.Quit
	case key.Matches(msg, kMove):
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		default:
			if m.cursor < len(m.files)-1 {
				m.cursor++
			}
		}
	case key.Matches(msg, kToggle):
		m.selected[m.cursor] = !m.selected[m.cursor]
	case key.Matches(msg, kAll):
		all := true
		for i := range m.files {
			if !m.selected[i] {
				all = false
				break
			}
		}
		for i := range m.files {
			m.selected[i] = !all
		}
	case key.Matches(msg, kAdjust):
		k := msg.String()
		switch k {
		case "f":
			m.fpsIdx = (m.fpsIdx + 1) % len(fpsOptions)
			m.opt.FPS = fpsOptions[m.fpsIdx]
		case "s":
			m.scaleIdx = (m.scaleIdx + 1) % len(scaleOptions)
			m.opt.Scale = scaleOptions[m.scaleIdx]
		case "p":
			all := export.Presets()
			for i := 1; i <= len(all); i++ {
				idx := (m.presetIdx + i) % len(all)
				if all[idx].Supported(m.opt.Caps) {
					m.presetIdx = idx
					m.opt.Preset = all[idx]
					break
				}
			}
		case "w":
			m.workersIdx = (m.workersIdx + 1) % len(workerRange)
			m.opt.Workers = workerRange[m.workersIdx]
		}
		m.flashKey = k
		m.flashUntil = time.Now().Add(850 * time.Millisecond)
		return m, tea.Tick(900*time.Millisecond, func(time.Time) tea.Msg { return flashExpiredMsg{} })
	case key.Matches(msg, kStart):
		return m.startRun()
	}
	return m, nil
}

func (m Model) startRun() (tea.Model, tea.Cmd) {
	var picked []string
	for i, f := range m.files {
		if m.selected[i] {
			picked = append(picked, f)
		}
	}
	if len(picked) == 0 {
		return m, nil
	}
	m.jobs = make([]jobView, len(picked))
	for i, p := range picked {
		m.jobs[i] = jobView{path: p, stage: export.StagePending}
	}
	m.started = time.Now()
	m.ch = make(chan export.Progress, 256)
	m.done = make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	ch, done, opt := m.ch, m.done, m.opt
	go func() {
		err := export.Run(ctx, picked, opt, func(p export.Progress) { ch <- p })
		close(ch)
		done <- err
	}()
	m.phase = phaseRun
	return m, listen(m.ch, m.done)
}

// ── vistas ──

func (m Model) View() tea.View {
	var b strings.Builder
	switch m.phase {
	case phasePick:
		m.viewPick(&b)
	case phaseRun:
		m.viewRun(&b, false)
	case phaseDone:
		m.viewRun(&b, true)
	}
	v := tea.NewView(m.pal.root.Render(b.String()))
	v.AltScreen = true
	return v
}

// header compone el chip de marca + metadatos separados por · .
func (m Model) header(parts ...string) string {
	sep := m.pal.dotSep.Render(" · ")
	metas := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			metas = append(metas, m.pal.meta.Render(p))
		}
	}
	return m.pal.chip.Render("dcx") + " " + m.pal.slashes.Render("╱╱") + "  " + strings.Join(metas, sep)
}

func (m Model) settingsLine() string {
	item := func(k, v string) string {
		vs := m.pal.settingVal
		if m.flashKey == k && time.Now().Before(m.flashUntil) {
			vs = m.pal.settingHot
		}
		return m.pal.settingKey.Render(k) + " " + vs.Render(v)
	}
	enc := m.pal.encoderSW.Render("software")
	if _, hw := m.opt.Preset.Args(m.opt.Caps); hw {
		enc = m.pal.encoderHW.Render("videotoolbox")
	}
	return strings.Join([]string{
		item("f", fmt.Sprintf("%d fps", m.opt.FPS)),
		item("s", fmt.Sprintf("%d×", m.opt.Scale)),
		item("p", m.opt.Preset.Name),
		item("w", fmt.Sprintf("%d workers", m.opt.Workers)),
		enc,
	}, "   ")
}

func (m Model) helpLine(ks ...key.Binding) string {
	return lipglossMarginTop(m.hlp.View(helpKeys(ks)))
}

func lipglossMarginTop(s string) string { return "\n" + s }

func (m Model) viewPick(b *strings.Builder) {
	nSel := 0
	for i := range m.files {
		if m.selected[i] {
			nSel++
		}
	}
	b.WriteString(m.header(
		fmt.Sprintf("%d %s", len(m.files), plural(len(m.files), "composición", "composiciones")),
		fmt.Sprintf("%d %s", nSel, plural(nSel, "marcada", "marcadas")),
		m.meta,
	))
	b.WriteString("\n\n")
	b.WriteString(m.settingsLine())
	b.WriteString("\n\n")
	nameW := max(12, m.width-10)
	for i, f := range m.files {
		rail := "  "
		if i == m.cursor {
			rail = m.pal.rail.Render("┃") + " "
		}
		glyph := m.pal.glyphOff.Render("○")
		style := m.pal.rowMuted
		if m.selected[i] {
			glyph = m.pal.glyphOn.Render("◉")
			style = m.pal.rowBase
		}
		if i == m.cursor {
			style = style.Bold(true)
		}
		name := ansi.Truncate(displayName(f), nameW, "…")
		fmt.Fprintf(b, "%s%s %s\n", rail, glyph, style.Render(name))
	}
	b.WriteString(m.helpLine(kToggle, kAll, kAdjust, kStart, kQuit))
}

func (m Model) viewRun(b *strings.Builder, done bool) {
	finished := 0
	for _, j := range m.jobs {
		if j.stage == export.StageDone || j.stage == export.StageFailed {
			finished++
		}
	}
	b.WriteString(m.header(
		fmt.Sprintf("exportando %d/%d", finished, len(m.jobs)),
		m.opt.Preset.Name,
		fmt.Sprintf("%d workers", m.opt.Workers),
	))
	b.WriteString("\n\n")

	// columnas: [glifo 1] [nombre nameW] [barra] [pct 5] [extra]
	longest := 0
	for _, j := range m.jobs {
		longest = max(longest, len([]rune(displayName(j.path))))
	}
	nameW := clamp(longest, 12, 28)
	if m.width < 80 {
		nameW = min(nameW, 18)
	}
	showFrames := m.width >= 88
	showEta := m.width >= 100
	extraW := 24
	barW := max(10, m.width-4-2-nameW-1-6-1-extraW)
	bar := m.bar
	bar.SetWidth(barW)

	for _, j := range m.jobs {
		b.WriteString(m.jobLine(j, bar, nameW, barW, showFrames, showEta))
		b.WriteString("\n")
	}

	if done {
		m.viewSummary(b)
	} else if m.canceling {
		b.WriteString("\n" + m.pal.quiet.Render("cancelando…"))
	} else {
		b.WriteString(m.helpLine(kCancel))
	}
}

func (m Model) jobLine(j jobView, bar progress.Model, nameW, barW int, showFrames, showEta bool) string {
	name := ansi.Truncate(displayName(j.path), nameW, "…")
	pad := strings.Repeat(" ", nameW-ansi.StringWidth(name))
	pct := func(p float64) string {
		return m.pal.subtle.Render(fmt.Sprintf("%4.0f%%", p*100))
	}
	switch {
	case j.err != nil:
		msg := ansi.Truncate(j.err.Error(), max(20, m.width-nameW-12), "…")
		return m.pal.errText.Render("×") + " " + m.pal.rowBase.Render(name) + pad + " " + m.pal.errText.Render(msg)
	case j.stage == export.StageDone:
		// truncar la cola al ancho restante para no desbordar la fila
		meta := fmt.Sprintf("  %s · %s", humanBytes(j.bytes), j.elapsed.Round(time.Second))
		avail := m.width - 4 - 2 - nameW - 1 - barW - 1 - 5 - 4 - ansi.StringWidth(meta)
		tail := ""
		if avail >= 10 {
			tail = m.pal.faint.Render("→ ") + m.pal.subtle.Render(ansi.Truncate(filepath.Base(j.out), avail, "…")) + m.pal.faint.Render(meta)
		} else if avail+ansi.StringWidth(meta) >= 10 {
			tail = m.pal.faint.Render("→ ") + m.pal.subtle.Render(ansi.Truncate(filepath.Base(j.out), avail+ansi.StringWidth(meta), "…"))
		}
		glyph := m.pal.ok.Render("✓")
		if j.warn != "" {
			glyph = m.pal.warnText.Render("!")
		}
		return glyph + " " + m.pal.rowBase.Render(name) + pad + " " +
			m.pal.barDone.Render(strings.Repeat("█", barW)) + " " + pct(1) + "  " + tail
	case j.stage == export.StagePending:
		return m.pal.glyphWait.Render("●") + " " + m.pal.rowMuted.Render(name) + pad + " " +
			bar.ViewAs(0) + " " + strings.Repeat(" ", 5) + " " + m.pal.faint.Render("en cola")
	case j.stage == export.StageBooting:
		return m.spin.View() + " " + m.pal.rowBase.Render(name) + pad + " " +
			bar.ViewAs(0) + " " + strings.Repeat(" ", 5) + " " + m.pal.faint.Render("arrancando")
	case j.stage == export.StageEncoding:
		return m.spin.View() + " " + m.pal.rowBase.Render(name) + pad + " " +
			bar.ViewAs(1) + " " + pct(1) + " " + m.pal.faint.Render("codificando")
	default: // rendering
		p := 0.0
		if j.frames > 0 {
			p = float64(j.frame) / float64(j.frames)
		}
		extra := ""
		if showFrames && j.frames > 0 {
			extra = m.pal.subtle.Render(fmt.Sprintf(" %d/%d", j.frame, j.frames))
		}
		if showEta && j.frame > 0 && j.elapsed > 0 && j.stage == export.StageRendering {
			frac := float64(j.frame) / float64(j.frames)
			rest := time.Duration(float64(j.elapsed)/frac - float64(j.elapsed))
			extra += m.pal.faint.Render(fmt.Sprintf(" · eta %s", rest.Round(time.Second)))
		}
		return m.spin.View() + " " + m.pal.rowBase.Render(name) + pad + " " +
			bar.ViewAs(p) + " " + pct(p) + extra
	}
}

func (m Model) viewSummary(b *strings.Builder) {
	var ok, failed int
	var bytes int64
	var warn string
	for _, j := range m.jobs {
		switch {
		case j.err != nil:
			failed++
		case j.stage == export.StageDone:
			ok++
			bytes += j.bytes
			if j.warn != "" && warn == "" {
				warn = j.warn
			}
		}
	}
	b.WriteString("\n" + m.pal.hairSep.Render(strings.Repeat("─", max(20, m.width-4))) + "\n\n")
	badge := m.pal.badgeOK.Render("LISTO")
	if ok == 0 && failed > 0 {
		badge = m.pal.badgeErr.Render("ERROR")
	}
	total := m.pal.rowBase.Render(fmt.Sprintf("%d %s", ok, plural(ok, "exportado", "exportados"))) +
		m.pal.faint.Render(fmt.Sprintf(" · %s · %s", humanBytes(bytes), time.Since(m.started).Round(time.Second)))
	line := badge + "  " + total
	if failed > 0 {
		line += m.pal.errText.Render(fmt.Sprintf("   × %d con error", failed))
	}
	b.WriteString(line)
	if warn != "" {
		b.WriteString("\n" + m.pal.warnText.Render("! "+ansi.Truncate(warn, max(20, m.width-6), "…")))
	}
	b.WriteString(m.helpLine(kExit))
}

func displayName(p string) string {
	return strings.TrimSuffix(filepath.Base(p), ".dc.html")
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func humanBytes(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1f GB", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.0f MB", float64(n)/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.0f kB", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
