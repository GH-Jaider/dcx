// Package ui es la TUI (Bubble Tea) de dcx: selector de proyectos con
// ajustes en vivo, dashboard de progreso paralelo y resumen final.
package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

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
}

type progressMsg export.Progress

type engineDoneMsg struct{ err error }

// Model es el modelo raíz de la TUI.
type Model struct {
	files    []string
	selected map[int]bool
	cursor   int

	opt        export.Options
	fpsIdx     int
	scaleIdx   int
	presetIdx  int
	workersIdx int

	phase     phase
	jobs      []jobView
	ch        chan export.Progress
	done      chan error
	cancel    context.CancelFunc
	canceling bool
	engineErr error

	bar   progress.Model
	spin  spinner.Model
	width int
}

// New crea el modelo con los proyectos descubiertos y las opciones iniciales.
func New(files []string, opt export.Options) Model {
	m := Model{
		files:    files,
		selected: map[int]bool{},
		opt:      opt,
		bar:      progress.New(progress.WithDefaultGradient(), progress.WithoutPercentage()),
		spin:     spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		width:    100,
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
	return m
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
func Run(files []string, opt export.Options) error {
	final, err := tea.NewProgram(New(files, opt), tea.WithAltScreen()).Run()
	if err != nil {
		return err
	}
	if m, ok := final.(Model); ok {
		return m.engineErr
	}
	return nil
}

func (m Model) Init() tea.Cmd { return m.spin.Tick }

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
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.bar.Width = min(36, max(10, msg.Width-56))
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

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
		}
		return m, listen(m.ch, m.done)

	case engineDoneMsg:
		m.engineErr = msg.err
		m.phase = phaseDone
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		if m.phase == phaseRun {
			return m.cancelRun()
		}
		return m, tea.Quit
	}
	switch m.phase {
	case phasePick:
		return m.handlePickKey(key)
	case phaseRun:
		if key == "q" {
			return m.cancelRun()
		}
	case phaseDone:
		if key == "q" || key == "enter" || key == "esc" {
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

func (m Model) handlePickKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.files)-1 {
			m.cursor++
		}
	case " ":
		m.selected[m.cursor] = !m.selected[m.cursor]
	case "a":
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
	case "f":
		m.fpsIdx = (m.fpsIdx + 1) % len(fpsOptions)
		m.opt.FPS = fpsOptions[m.fpsIdx]
	case "s":
		m.scaleIdx = (m.scaleIdx + 1) % len(scaleOptions)
		m.opt.Scale = scaleOptions[m.scaleIdx]
	case "p":
		m.presetIdx = (m.presetIdx + 1) % len(export.Presets())
		m.opt.Preset = export.Presets()[m.presetIdx]
	case "w":
		m.workersIdx = (m.workersIdx + 1) % len(workerRange)
		m.opt.Workers = workerRange[m.workersIdx]
	case "enter":
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

func (m Model) View() string {
	var b strings.Builder
	b.WriteString(headerBox.Render(titleStyle.Render("dcx") + subtleStyle.Render("  ·  .dc.html → video")))
	b.WriteString("\n")
	switch m.phase {
	case phasePick:
		m.viewPick(&b)
	case phaseRun:
		m.viewRun(&b, false)
	case phaseDone:
		m.viewRun(&b, true)
	}
	return containerBox.Render(b.String())
}

func (m Model) settingsLine() string {
	p := m.opt.Preset
	return settingStyle.Render(fmt.Sprintf("%s %d fps   %s %dx   %s %s   %s %d workers",
		settingKey.Render("f"), m.opt.FPS,
		settingKey.Render("s"), m.opt.Scale,
		settingKey.Render("p"), p.Name,
		settingKey.Render("w"), m.opt.Workers,
	))
}

func (m Model) viewPick(b *strings.Builder) {
	b.WriteString(m.settingsLine() + "\n\n")
	for i, f := range m.files {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorStyle.Render("❯ ")
		}
		check := dimFileStyle.Render("[ ]")
		name := dimFileStyle.Render(displayName(f))
		if m.selected[i] {
			check = checkedStyle.Render("[✓]")
			name = fileStyle.Render(displayName(f))
		}
		fmt.Fprintf(b, "%s%s %s\n", cursor, check, name)
	}
	b.WriteString(helpStyle.Render("↑↓ mover · espacio marcar · a todos/ninguno · f/s/p/w ajustes · enter exportar · q salir"))
}

func (m Model) viewRun(b *strings.Builder, done bool) {
	b.WriteString(m.settingsLine() + "\n\n")
	for _, j := range m.jobs {
		b.WriteString(m.jobLine(j) + "\n")
	}
	if done {
		var ok, failed int
		for _, j := range m.jobs {
			if j.err != nil {
				failed++
			} else if j.stage == export.StageDone {
				ok++
			}
		}
		summary := fmt.Sprintf("%d listos", ok)
		if failed > 0 {
			summary += errStyle.Render(fmt.Sprintf(" · %d con error", failed))
		}
		b.WriteString("\n" + okStyle.Render("● ") + summary)
		b.WriteString(helpStyle.Render("q para salir"))
	} else if m.canceling {
		b.WriteString(helpStyle.Render("cancelando…"))
	} else {
		b.WriteString(helpStyle.Render("q cancela"))
	}
}

func (m Model) jobLine(j jobView) string {
	name := truncate(displayName(j.path), 28)
	switch {
	case j.err != nil:
		return fmt.Sprintf("%s %-28s %s", errStyle.Render("✗"), name, errStyle.Render(truncate(j.err.Error(), max(20, m.width-40))))
	case j.stage == export.StageDone:
		return fmt.Sprintf("%s %-28s %s %s",
			okStyle.Render("✓"), name,
			outPathStyle.Render(filepath.Base(j.out)),
			subtleStyle.Render(fmt.Sprintf("(%s · %s)", humanBytes(j.bytes), j.elapsed.Round(time.Second))))
	case j.stage == export.StagePending:
		return fmt.Sprintf("%s %-28s %s", subtleStyle.Render("·"), name, stageStyle.Render(j.stage.String()))
	default:
		pct := 0.0
		extra := ""
		if j.frames > 0 {
			pct = float64(j.frame) / float64(j.frames)
			extra = subtleStyle.Render(fmt.Sprintf(" %d/%d%s", j.frame, j.frames, eta(j)))
		}
		return fmt.Sprintf("%s %-28s %s %s%s",
			checkedStyle.Render(m.spin.View()), name,
			m.bar.ViewAs(pct),
			percentStyle.Render(fmt.Sprintf("%3.0f%%", pct*100)),
			extra)
	}
}

func eta(j jobView) string {
	if j.frame == 0 || j.frames == 0 || j.elapsed == 0 || j.stage != export.StageRendering {
		return ""
	}
	frac := float64(j.frame) / float64(j.frames)
	rest := time.Duration(float64(j.elapsed)/frac - float64(j.elapsed))
	return fmt.Sprintf(" · eta %s", rest.Round(time.Second))
}

func displayName(p string) string {
	return strings.TrimSuffix(filepath.Base(p), ".dc.html")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
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
