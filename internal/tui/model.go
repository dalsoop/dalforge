package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tab int

const (
	tabDals tab = iota
	tabTalk
	tabLogs
)

var tabNames = []string{"Dals", "Talk", "Logs"}

// DalStatus represents a dal's runtime status.
type DalStatus struct {
	Name     string
	Role     string
	LXC      string
	Channel  string
	Status   string // "online", "offline"
	LastSeen time.Time
}

// TalkMessage represents a Mattermost message.
type TalkMessage struct {
	User    string
	Content string
	Time    time.Time
	IsThread bool
}

// Config for the TUI.
type Config struct {
	ServeURL string // dalcenter serve URL
	MMURL    string // Mattermost URL
	MMToken  string // Mattermost token
	Channel  string // channel ID
}

// Model is the top-level Bubble Tea model.
type Model struct {
	cfg       Config
	width     int
	height    int
	activeTab tab
	dals      []DalStatus
	messages  []TalkMessage
	logLines  []string
	cursor    int
	input     string
	err       error
}

// New creates a new TUI model.
func New(cfg Config) Model {
	return Model{
		cfg:       cfg,
		activeTab: tabDals,
		dals: []DalStatus{
			{Name: "dal-leader", Role: "오케스트레이터", LXC: "host", Status: "online"},
			{Name: "dal-marketing-200", Role: "마케팅 전략가", LXC: "200", Status: "online"},
			{Name: "dal-tech-writer-201", Role: "기술 콘텐츠 담당", LXC: "201", Status: "online"},
		},
	}
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % 3
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab + 2) % 3
			return m, nil
		case "j", "down":
			if m.cursor < len(m.dals)-1 {
				m.cursor++
			}
			return m, nil
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "enter":
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				if dal.LXC != "host" {
					return m, openTmuxWindow(dal)
				}
			}
			return m, nil
		case "s":
			// Shell into dal's LXC
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				if dal.LXC != "host" {
					return m, openShell(dal)
				}
			}
			return m, nil
		case "l":
			// View logs
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				if dal.LXC != "host" {
					return m, openLogs(dal)
				}
			}
			return m, nil
		case "r":
			// Restart dal
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				m.logLines = append(m.logLines, fmt.Sprintf("[%s] restarting %s...", time.Now().Format("15:04:05"), dal.Name))
			}
			return m, nil
		}

	case tickMsg:
		m.refreshDalStatus()
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

func (m *Model) refreshDalStatus() {
	for i := range m.dals {
		dal := &m.dals[i]
		if dal.LXC == "host" {
			out, _ := exec.Command("bash", "-c", "pgrep -f 'dalcenter talk conductor' > /dev/null && echo online || echo offline").Output()
			dal.Status = strings.TrimSpace(string(out))
		} else {
			out, _ := exec.Command("pct", "exec", dal.LXC, "--", "bash", "-c", "pgrep -f 'dalcenter talk' > /dev/null && echo online || echo offline").Output()
			dal.Status = strings.TrimSpace(string(out))
		}
		dal.LastSeen = time.Now()
	}
}

func openTmuxWindow(dal DalStatus) tea.Cmd {
	return tea.ExecProcess(
		exec.Command("tmux", "new-window", "-n", dal.Name,
			fmt.Sprintf("pct exec %s -- bash -c 'tmux attach 2>/dev/null || tmux new-session'", dal.LXC)),
		func(err error) tea.Msg { return nil },
	)
}

func openShell(dal DalStatus) tea.Cmd {
	return tea.ExecProcess(
		exec.Command("tmux", "new-window", "-n", dal.Name+":sh",
			fmt.Sprintf("pct exec %s -- bash -l", dal.LXC)),
		func(err error) tea.Msg { return nil },
	)
}

func openLogs(dal DalStatus) tea.Cmd {
	return tea.ExecProcess(
		exec.Command("tmux", "new-window", "-n", dal.Name+":log",
			fmt.Sprintf("pct exec %s -- tail -f /var/log/dalcenter-talk.log", dal.LXC)),
		func(err error) tea.Msg { return nil },
	)
}

func (m Model) View() string {
	if m.width == 0 {
		return "loading..."
	}

	// Tabs
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == m.activeTab {
			tabs = append(tabs, activeTabStyle.Render(name))
		} else {
			tabs = append(tabs, tabStyle.Render(name))
		}
	}
	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)
	header := titleStyle.Render("dalcenter") + "  " + tabBar

	// Content
	var content string
	switch m.activeTab {
	case tabDals:
		content = m.viewDals()
	case tabTalk:
		content = m.viewTalk()
	case tabLogs:
		content = m.viewLogs()
	}

	// Help
	help := helpStyle.Render("[Tab] 탭 전환  [Enter] tmux 접속  [s] shell  [l] 로그  [r] 재시작  [q] 종료")

	return header + "\n\n" + content + "\n\n" + help
}

func (m Model) viewDals() string {
	header := fmt.Sprintf("  %-25s %-20s %-6s %-10s %-10s",
		"DAL", "ROLE", "LXC", "CHANNEL", "STATUS")
	headerStyle := lipgloss.NewStyle().Foreground(subtle).Render(header)

	var rows []string
	for i, dal := range m.dals {
		cursor := "  "
		if i == m.cursor {
			cursor = "▸ "
		}

		status := statusOnline
		if dal.Status != "online" {
			status = statusOffline
		}

		row := fmt.Sprintf("%s%-25s %-20s %-6s %-10s %s",
			cursor, dal.Name, dal.Role, dal.LXC, dal.Channel, status)

		if i == m.cursor {
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		rows = append(rows, row)
	}

	return headerStyle + "\n" + strings.Join(rows, "\n")
}

func (m Model) viewTalk() string {
	if len(m.messages) == 0 {
		return helpStyle.Render("  Mattermost 연동 준비 중... (다음 버전에서 지원)")
	}
	var lines []string
	for _, msg := range m.messages {
		lines = append(lines, fmt.Sprintf("  [%s] %s: %s",
			msg.Time.Format("15:04"), msg.User, msg.Content))
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewLogs() string {
	if len(m.logLines) == 0 {
		return helpStyle.Render("  dal을 선택하고 [l]을 누르면 로그를 볼 수 있습니다")
	}
	start := 0
	if len(m.logLines) > 20 {
		start = len(m.logLines) - 20
	}
	return strings.Join(m.logLines[start:], "\n")
}
