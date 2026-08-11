package ui

import "github.com/charmbracelet/lipgloss"

var (
	green   = lipgloss.Color("#3DD98A")
	dimGray = lipgloss.Color("245")
	red     = lipgloss.Color("#FF6B6B")

	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(green)
	subtleStyle  = lipgloss.NewStyle().Foreground(dimGray)
	settingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	settingKey   = lipgloss.NewStyle().Foreground(green).Bold(true)
	cursorStyle  = lipgloss.NewStyle().Foreground(green).Bold(true)
	checkedStyle = lipgloss.NewStyle().Foreground(green)
	fileStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	dimFileStyle = lipgloss.NewStyle().Foreground(dimGray)
	okStyle      = lipgloss.NewStyle().Foreground(green).Bold(true)
	errStyle     = lipgloss.NewStyle().Foreground(red)
	outPathStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Underline(true)
	helpStyle    = lipgloss.NewStyle().Foreground(dimGray).MarginTop(1)
	headerBox    = lipgloss.NewStyle().MarginBottom(1)
	containerBox = lipgloss.NewStyle().Padding(1, 2)
	stageStyle   = lipgloss.NewStyle().Foreground(dimGray).Width(13)
	percentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Width(5).Align(lipgloss.Right)
)
