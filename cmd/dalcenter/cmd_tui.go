package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

func newTuiCmd() *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			p := tea.NewProgram(newDashboard(addr), tea.WithAltScreen())
			_, err := p.Run()
			return err
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "http://localhost:11192", "dalcenter API address")
	return cmd
}

// --- styles ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	headerStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	runningStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	sleepingStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	selectedStyle = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	helpStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	tabActive     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Underline(true)
	tabInactive   = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

// --- messages ---

type tickMsg time.Time
type dalStatusMsg []dalInfo
type taskListMsg []taskInfo
type escalationMsg []escalationInfo
type errMsg struct{ err error }

type dalInfo struct {
	Name            string `json:"Name"`
	Role            string `json:"Role"`
	Player          string `json:"Player"`
	ContainerStatus string `json:"container_status"`
	Model           string `json:"Model"`
}

type taskInfo struct {
	ID        string     `json:"id"`
	Dal       string     `json:"dal"`
	Task      string     `json:"task"`
	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	DoneAt    *time.Time `json:"done_at,omitempty"`
}

type escalationInfo struct {
	ID         string    `json:"id"`
	Dal        string    `json:"dal"`
	ErrorClass string    `json:"error_class"`
	Timestamp  time.Time `json:"timestamp"`
	Resolved   bool      `json:"resolved"`
}

// --- model ---

type tab int

const (
	tabDals tab = iota
	tabTasks
	tabEscalations
)

type dashboard struct {
	addr        string
	tab         tab
	dals        []dalInfo
	tasks       []taskInfo
	escalations []escalationInfo
	cursor      int
	err         error
	width       int
	height      int
}

func newDashboard(addr string) dashboard {
	return dashboard{addr: addr}
}

func (d dashboard) Init() tea.Cmd {
	return tea.Batch(d.fetchAll(), tickCmd())
}

func (d dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			return d, tea.Quit
		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			d.tab = (d.tab + 1) % 3
			d.cursor = 0
		case key.Matches(msg, key.NewBinding(key.WithKeys("1"))):
			d.tab = tabDals
			d.cursor = 0
		case key.Matches(msg, key.NewBinding(key.WithKeys("2"))):
			d.tab = tabTasks
			d.cursor = 0
		case key.Matches(msg, key.NewBinding(key.WithKeys("3"))):
			d.tab = tabEscalations
			d.cursor = 0
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			d.cursor++
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if d.cursor > 0 {
				d.cursor--
			}
		}
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
	case dalStatusMsg:
		d.dals = msg
		d.err = nil
	case taskListMsg:
		d.tasks = msg
	case escalationMsg:
		d.escalations = msg
	case errMsg:
		d.err = msg.err
	case tickMsg:
		return d, tea.Batch(d.fetchAll(), tickCmd())
	}

	// Clamp cursor
	max := d.listLen() - 1
	if max < 0 {
		max = 0
	}
	if d.cursor > max {
		d.cursor = max
	}

	return d, nil
}

func (d dashboard) listLen() int {
	switch d.tab {
	case tabDals:
		return len(d.dals)
	case tabTasks:
		return len(d.tasks)
	case tabEscalations:
		return len(d.escalations)
	}
	return 0
}

func (d dashboard) View() string {
	var b strings.Builder

	// Header
	b.WriteString(titleStyle.Render("dalcenter"))
	b.WriteString("  ")
	b.WriteString(helpStyle.Render(d.addr))
	if d.err != nil {
		b.WriteString("  ")
		b.WriteString(errorStyle.Render(d.err.Error()))
	}
	b.WriteString("\n\n")

	// Tabs
	tabs := []string{"Dals", "Tasks", "Escalations"}
	for i, t := range tabs {
		label := fmt.Sprintf(" %d %s ", i+1, t)
		if tab(i) == d.tab {
			b.WriteString(tabActive.Render(label))
		} else {
			b.WriteString(tabInactive.Render(label))
		}
		b.WriteString("  ")
	}
	b.WriteString("\n\n")

	// Content
	switch d.tab {
	case tabDals:
		b.WriteString(d.renderDals())
	case tabTasks:
		b.WriteString(d.renderTasks())
	case tabEscalations:
		b.WriteString(d.renderEscalations())
	}

	// Footer
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("q quit · tab/1-3 switch · j/k navigate"))
	b.WriteString("\n")

	return b.String()
}

func (d dashboard) renderDals() string {
	if len(d.dals) == 0 {
		return helpStyle.Render("no dals")
	}

	var b strings.Builder
	header := fmt.Sprintf("  %-20s %-10s %-10s %s", "NAME", "ROLE", "PLAYER", "STATUS")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	for i, dal := range d.dals {
		status := dal.ContainerStatus
		var statusStyled string
		if status == "running" {
			statusStyled = runningStyle.Render(status)
		} else {
			statusStyled = sleepingStyle.Render(status)
		}

		line := fmt.Sprintf("  %-20s %-10s %-10s %s", dal.Name, dal.Role, dal.Player, statusStyled)
		if i == d.cursor {
			// Re-render without color for selected
			plain := fmt.Sprintf("  %-20s %-10s %-10s %s", dal.Name, dal.Role, dal.Player, status)
			b.WriteString(selectedStyle.Render(plain))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (d dashboard) renderTasks() string {
	if len(d.tasks) == 0 {
		return helpStyle.Render("no tasks")
	}

	var b strings.Builder
	header := fmt.Sprintf("  %-18s %-12s %-10s %-8s %s", "ID", "DAL", "STATUS", "AGO", "TASK")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	for i, t := range d.tasks {
		ago := shortDuration(time.Since(t.StartedAt))
		task := t.Task
		if len(task) > 40 {
			task = task[:40] + "…"
		}

		var statusStyled string
		switch t.Status {
		case "running":
			statusStyled = runningStyle.Render(t.Status)
		case "failed":
			statusStyled = errorStyle.Render(t.Status)
		default:
			statusStyled = t.Status
		}

		line := fmt.Sprintf("  %-18s %-12s %-10s %-8s %s", t.ID, t.Dal, statusStyled, ago, task)
		if i == d.cursor {
			plain := fmt.Sprintf("  %-18s %-12s %-10s %-8s %s", t.ID, t.Dal, t.Status, ago, task)
			b.WriteString(selectedStyle.Render(plain))
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (d dashboard) renderEscalations() string {
	if len(d.escalations) == 0 {
		return helpStyle.Render("no escalations")
	}

	var b strings.Builder
	header := fmt.Sprintf("  %-12s %-15s %-25s %-10s %s", "ID", "DAL", "CLASS", "AGO", "RESOLVED")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	for i, e := range d.escalations {
		ago := shortDuration(time.Since(e.Timestamp))
		resolved := "no"
		if e.Resolved {
			resolved = "yes"
		}
		class := e.ErrorClass
		if len(class) > 25 {
			class = class[:25]
		}

		line := fmt.Sprintf("  %-12s %-15s %-25s %-10s %s", e.ID, e.Dal, class, ago, resolved)
		if i == d.cursor {
			b.WriteString(selectedStyle.Render(line))
		} else {
			if !e.Resolved {
				b.WriteString(errorStyle.Render(line))
			} else {
				b.WriteString(line)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// --- commands ---

func tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (d dashboard) fetchAll() tea.Cmd {
	return tea.Batch(
		d.fetchDals(),
		d.fetchTasks(),
		d.fetchEscalations(),
	)
}

func (d dashboard) fetchDals() tea.Cmd {
	return func() tea.Msg {
		data, err := apiGet(d.addr + "/api/status")
		if err != nil {
			return errMsg{err}
		}
		var dals []dalInfo
		if err := json.Unmarshal(data, &dals); err != nil {
			return errMsg{err}
		}
		// Sort: running first, then by name
		sort.Slice(dals, func(i, j int) bool {
			if dals[i].ContainerStatus != dals[j].ContainerStatus {
				return dals[i].ContainerStatus == "running"
			}
			return dals[i].Name < dals[j].Name
		})
		return dalStatusMsg(dals)
	}
}

func (d dashboard) fetchTasks() tea.Cmd {
	return func() tea.Msg {
		data, err := apiGet(d.addr + "/api/tasks")
		if err != nil {
			return nil // non-critical
		}
		var tasks []taskInfo
		if err := json.Unmarshal(data, &tasks); err != nil {
			return nil
		}
		// Sort: newest first
		sort.Slice(tasks, func(i, j int) bool {
			return tasks[i].StartedAt.After(tasks[j].StartedAt)
		})
		return taskListMsg(tasks)
	}
}

func (d dashboard) fetchEscalations() tea.Cmd {
	return func() tea.Msg {
		data, err := apiGet(d.addr + "/api/escalations")
		if err != nil {
			return nil
		}
		var resp struct {
			Escalations []escalationInfo `json:"escalations"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil
		}
		return escalationMsg(resp.Escalations)
	}
}

func apiGet(url string) ([]byte, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func shortDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
