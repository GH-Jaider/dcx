package ui

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

// palette es el sistema de color adaptativo de dcx: verde de marca #3DD98A
// en terminal oscura y una variante oscurecida con contraste real en clara,
// más una rampa de grises anclada en los pares probados del ecosistema Charm.
type palette struct {
	isDark bool

	accent    color.Color
	accentDim color.Color
	onAccent  color.Color
	fgBase    color.Color
	fgSubtle  color.Color
	fgMuted   color.Color
	fgFaint   color.Color
	hairline  color.Color
	errColor  color.Color

	// estilos derivados listos para usar
	chip       lipgloss.Style // wordmark " dcx " dark-on-green
	slashes    lipgloss.Style // ╱╱ decorativas de marca
	meta       lipgloss.Style // metadatos del header
	dotSep     lipgloss.Style // separadores ·
	settingKey lipgloss.Style
	settingVal lipgloss.Style
	settingHot lipgloss.Style // valor recién cambiado (flash)
	encoderHW  lipgloss.Style
	encoderSW  lipgloss.Style
	rail       lipgloss.Style // ┃ del cursor
	rowBase    lipgloss.Style
	rowBold    lipgloss.Style
	rowMuted   lipgloss.Style
	glyphOn    lipgloss.Style // ◉
	glyphOff   lipgloss.Style // ○
	glyphWait  lipgloss.Style // ● en cola
	ok         lipgloss.Style // ✓
	errText    lipgloss.Style // × y mensajes de error
	warnText   lipgloss.Style // ! y avisos que no impiden el export
	subtle     lipgloss.Style
	faint      lipgloss.Style
	quiet      lipgloss.Style // estados "cancelando…" (faint+italic)
	hairSep    lipgloss.Style // línea ─
	badgeOK    lipgloss.Style // " LISTO "
	badgeErr   lipgloss.Style // " ERROR "
	barDone    lipgloss.Style // barra sólida de job terminado
	root       lipgloss.Style
}

func newPalette(isDark bool) palette {
	ld := lipgloss.LightDark(isDark)
	p := palette{isDark: isDark}
	p.accent = ld(lipgloss.Color("#1C8760"), lipgloss.Color("#3DD98A"))
	p.accentDim = ld(lipgloss.Color("#5FB48E"), lipgloss.Color("#1E7A52"))
	p.onAccent = ld(lipgloss.Color("#F7FDF9"), lipgloss.Color("#0C1F16"))
	p.fgBase = ld(lipgloss.Color("#1A1A1A"), lipgloss.Color("#DDDDDD"))
	p.fgSubtle = ld(lipgloss.Color("#6B6B6B"), lipgloss.Color("#9A9A9A"))
	p.fgMuted = ld(lipgloss.Color("#909090"), lipgloss.Color("#626262"))
	p.fgFaint = ld(lipgloss.Color("#B2B2B2"), lipgloss.Color("#4A4A4A"))
	p.hairline = ld(lipgloss.Color("#DDDADA"), lipgloss.Color("#3C3C3C"))
	p.errColor = ld(lipgloss.Color("#FF4672"), lipgloss.Color("#ED567A"))

	p.chip = lipgloss.NewStyle().Bold(true).Foreground(p.onAccent).Background(p.accent).Padding(0, 1)
	p.slashes = lipgloss.NewStyle().Foreground(p.accent)
	p.meta = lipgloss.NewStyle().Foreground(p.fgMuted)
	p.dotSep = lipgloss.NewStyle().Foreground(p.hairline)
	p.settingKey = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	p.settingVal = lipgloss.NewStyle().Foreground(p.fgBase)
	p.settingHot = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	p.encoderHW = lipgloss.NewStyle().Foreground(p.accent)
	p.encoderSW = lipgloss.NewStyle().Foreground(p.fgMuted)
	p.rail = lipgloss.NewStyle().Foreground(p.accent)
	p.rowBase = lipgloss.NewStyle().Foreground(p.fgBase)
	p.rowBold = lipgloss.NewStyle().Foreground(p.fgBase).Bold(true)
	p.rowMuted = lipgloss.NewStyle().Foreground(p.fgMuted)
	p.glyphOn = lipgloss.NewStyle().Foreground(p.accent)
	p.glyphOff = lipgloss.NewStyle().Foreground(p.fgMuted)
	p.glyphWait = lipgloss.NewStyle().Foreground(p.fgFaint)
	p.ok = lipgloss.NewStyle().Foreground(p.accent).Bold(true)
	p.errText = lipgloss.NewStyle().Foreground(p.errColor)
	p.warnText = lipgloss.NewStyle().Foreground(ld(lipgloss.Color("#B26B00"), lipgloss.Color("#E0A030")))
	p.subtle = lipgloss.NewStyle().Foreground(p.fgSubtle)
	p.faint = lipgloss.NewStyle().Foreground(p.fgFaint)
	p.quiet = lipgloss.NewStyle().Foreground(p.fgSubtle).Italic(true)
	p.hairSep = lipgloss.NewStyle().Foreground(p.hairline)
	p.badgeOK = lipgloss.NewStyle().Bold(true).Foreground(p.onAccent).Background(p.accent).Padding(0, 1)
	p.badgeErr = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(p.errColor).Padding(0, 1)
	p.barDone = lipgloss.NewStyle().Foreground(p.accent)
	p.root = lipgloss.NewStyle().Padding(1, 2)
	return p
}

// barColors devuelve el gradiente de marca y el color del track vacío para
// la barra de progreso según el tema.
func (p palette) barColors() (stops []color.Color, empty color.Color) {
	if p.isDark {
		return []color.Color{lipgloss.Color("#1E7A52"), lipgloss.Color("#3DD98A")}, lipgloss.Color("#3C3C3C")
	}
	return []color.Color{lipgloss.Color("#9BDFC2"), lipgloss.Color("#1C8760")}, lipgloss.Color("#DDDADA")
}
