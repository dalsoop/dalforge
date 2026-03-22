package tui

import "github.com/charmbracelet/lipgloss"

var (
	accent    = lipgloss.Color("#0f766e")
	subtle    = lipgloss.Color("#6b7280")
	highlight = lipgloss.Color("#fbbf24")
	danger    = lipgloss.Color("#ef4444")
	success   = lipgloss.Color("#22c55e")

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(subtle)

	activeTabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(lipgloss.Color("#ffffff")).
			Background(accent).
			Bold(true)

	titleStyle = lipgloss.NewStyle().
			Foreground(accent).
			Bold(true)

	statusOnline  = lipgloss.NewStyle().Foreground(success).Render("● online")
	statusOffline = lipgloss.NewStyle().Foreground(danger).Render("○ offline")

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(subtle)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtle)
)
