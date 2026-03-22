package tui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

// Config for the TUI.
type Config struct {
	ServeURL  string // dalcenter serve URL
	MMURL     string // Mattermost URL
	MMToken   string // Mattermost bot token (for reading)
	ChannelID string // channel ID
	// Bot name mapping: user_id → display name
	BotNames map[string]string
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
	inputMode bool
	err       error
	lastMsgAt int64 // last message create_at for polling
}

// New creates a new TUI model.
func New(cfg Config) Model {
	m := Model{
		cfg:       cfg,
		activeTab: tabDals,
		lastMsgAt: time.Now().UnixMilli(),
	}
	// Load from serve registry if available, otherwise detect via pct
	m.dals = m.loadDals()
	return m
}

func (m *Model) loadDals() []DalStatus {
	// Try serve API first
	if m.cfg.ServeURL != "" {
		if dals := m.fetchDalsFromServe(); len(dals) > 0 {
			return dals
		}
	}
	// Fallback: detect running dals via pct
	return m.detectDals()
}

func (m *Model) fetchDalsFromServe() []DalStatus {
	resp, err := http.Get(m.cfg.ServeURL + "/api/dals")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var entries []struct {
		Name   string `json:"name"`
		IP     string `json:"ip"`
		Port   int    `json:"port"`
		VMID   string `json:"vmid"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if json.Unmarshal(body, &entries) != nil {
		return nil
	}
	var dals []DalStatus
	for _, e := range entries {
		lxc := e.VMID
		if lxc == "" {
			lxc = "host"
		}
		dals = append(dals, DalStatus{
			Name:    e.Name,
			Role:    e.Role,
			LXC:     lxc,
			Channel: m.cfg.ChannelID,
			Status:  e.Status,
		})
	}
	return dals
}

func (m *Model) detectDals() []DalStatus {
	var dals []DalStatus
	// Check host conductor
	out, _ := exec.Command("bash", "-c", "pgrep -f 'dalcenter talk conductor' > /dev/null && echo online || echo offline").Output()
	dals = append(dals, DalStatus{
		Name: "dal-leader", Role: "오케스트레이터", LXC: "host",
		Status: strings.TrimSpace(string(out)),
	})
	// Check running LXCs for dalcenter talk
	pctOut, _ := exec.Command("pct", "list").Output()
	for _, line := range strings.Split(string(pctOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[1] != "running" {
			continue
		}
		vmid := fields[0]
		name := fields[2]
		chk, _ := exec.Command("pct", "exec", vmid, "--", "bash", "-c",
			"pgrep -f 'dalcenter talk' > /dev/null && echo online || echo offline").Output()
		status := strings.TrimSpace(string(chk))
		if status == "online" {
			dals = append(dals, DalStatus{
				Name: "dal-" + name, Role: "(detected)", LXC: vmid,
				Status: status,
			})
		}
	}
	return dals
}

type tickMsg time.Time
type messagesMsg []TalkMessage

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
		if m.inputMode {
			return m.handleInput(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % 3
			m.cursor = 0
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab + 2) % 3
			m.cursor = 0
			return m, nil
		case "j", "down":
			if m.activeTab == tabDals && m.cursor < len(m.dals)-1 {
				m.cursor++
			}
			return m, nil
		case "k", "up":
			if m.activeTab == tabDals && m.cursor > 0 {
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
			if m.activeTab == tabTalk {
				m.inputMode = true
				m.input = ""
				return m, nil
			}
			return m, nil
		case "s":
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				if dal.LXC != "host" {
					return m, openShell(dal)
				}
			}
			return m, nil
		case "l":
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				if dal.LXC != "host" {
					return m, openLogs(dal)
				}
			}
			return m, nil
		case "r":
			if m.activeTab == tabDals && m.cursor < len(m.dals) {
				dal := m.dals[m.cursor]
				m.logLines = append(m.logLines, fmt.Sprintf("[%s] restarting %s...", time.Now().Format("15:04:05"), dal.Name))
				return m, restartDal(dal)
			}
			return m, nil
		}

	case tickMsg:
		m.refreshDalStatus()
		if m.cfg.MMURL != "" && m.cfg.MMToken != "" && m.cfg.ChannelID != "" {
			return m, m.fetchMessages()
		}
		return m, tickCmd()

	case messagesMsg:
		m.messages = append(m.messages, []TalkMessage(msg)...)
		// Keep last 50
		if len(m.messages) > 50 {
			m.messages = m.messages[len(m.messages)-50:]
		}
		return m, tickCmd()

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

func (m Model) handleInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.input != "" {
			m.postMessage(m.input)
			m.messages = append(m.messages, TalkMessage{
				User:    "나",
				Content: m.input,
				Time:    time.Now(),
			})
		}
		m.inputMode = false
		m.input = ""
		return m, nil
	case "esc":
		m.inputMode = false
		m.input = ""
		return m, nil
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	default:
		if len(msg.String()) == 1 || msg.String() == " " {
			m.input += msg.String()
		}
		return m, nil
	}
}

func (m *Model) refreshDalStatus() {
	// If serve is available, reload full list
	if m.cfg.ServeURL != "" {
		if fresh := m.fetchDalsFromServe(); len(fresh) > 0 {
			m.dals = fresh
			return
		}
	}
	// Fallback: check each dal via pct
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

func (m *Model) fetchMessages() tea.Cmd {
	return func() tea.Msg {
		url := fmt.Sprintf("%s/api/v4/channels/%s/posts?since=%d",
			m.cfg.MMURL, m.cfg.ChannelID, m.lastMsgAt)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+m.cfg.MMToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return messagesMsg{}
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		var result struct {
			Order []string                   `json:"order"`
			Posts map[string]json.RawMessage `json:"posts"`
		}
		if json.Unmarshal(body, &result) != nil {
			return messagesMsg{}
		}

		var msgs []TalkMessage
		for i := len(result.Order) - 1; i >= 0; i-- {
			id := result.Order[i]
			var post struct {
				UserID   string `json:"user_id"`
				Message  string `json:"message"`
				CreateAt int64  `json:"create_at"`
			}
			if json.Unmarshal(result.Posts[id], &post) != nil {
				continue
			}
			if post.CreateAt <= m.lastMsgAt {
				continue
			}
			if post.CreateAt > m.lastMsgAt {
				m.lastMsgAt = post.CreateAt
			}
			user := post.UserID[:8]
			if name, ok := m.cfg.BotNames[post.UserID]; ok {
				user = name
			}
			msgs = append(msgs, TalkMessage{
				User:    user,
				Content: post.Message,
				Time:    time.UnixMilli(post.CreateAt),
			})
		}
		return messagesMsg(msgs)
	}
}

func (m *Model) postMessage(content string) {
	if m.cfg.MMURL == "" || m.cfg.MMToken == "" {
		return
	}
	body := fmt.Sprintf(`{"channel_id":%q,"message":%q}`, m.cfg.ChannelID, content)
	req, _ := http.NewRequest("POST", m.cfg.MMURL+"/api/v4/posts", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+m.cfg.MMToken)
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)
}

func restartDal(dal DalStatus) tea.Cmd {
	return func() tea.Msg {
		if dal.LXC == "host" {
			return nil
		}
		// Kill and let healthcheck or systemd restart
		exec.Command("pct", "exec", dal.LXC, "--", "bash", "-c", "pkill -f 'dalcenter talk'").Run()
		return nil
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
	var help string
	switch m.activeTab {
	case tabDals:
		help = "[Tab] 탭  [Enter] tmux  [s] shell  [l] 로그  [r] 재시작  [q] 종료"
	case tabTalk:
		if m.inputMode {
			help = "[Enter] 전송  [Esc] 취소"
		} else {
			help = "[Tab] 탭  [Enter] 메시지 입력  [q] 종료"
		}
	case tabLogs:
		help = "[Tab] 탭  [q] 종료"
	}

	return header + "\n\n" + content + "\n\n" + helpStyle.Render(help)
}

func (m Model) viewDals() string {
	header := fmt.Sprintf("  %-25s %-20s %-6s %-10s",
		"DAL", "ROLE", "LXC", "STATUS")
	headerLine := lipgloss.NewStyle().Foreground(subtle).Render(header)

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

		row := fmt.Sprintf("%s%-25s %-20s %-6s %s",
			cursor, dal.Name, dal.Role, dal.LXC, status)

		if i == m.cursor {
			row = lipgloss.NewStyle().Bold(true).Render(row)
		}
		rows = append(rows, row)
	}

	return headerLine + "\n" + strings.Join(rows, "\n")
}

func (m Model) viewTalk() string {
	maxLines := m.height - 8
	if maxLines < 5 {
		maxLines = 5
	}

	var lines []string
	start := 0
	if len(m.messages) > maxLines {
		start = len(m.messages) - maxLines
	}
	for _, msg := range m.messages[start:] {
		userStyle := lipgloss.NewStyle().Foreground(accent).Bold(true)
		timeStyle := lipgloss.NewStyle().Foreground(subtle)
		content := msg.Content
		if len(content) > m.width-30 && m.width > 40 {
			content = content[:m.width-33] + "..."
		}
		line := fmt.Sprintf("  %s %s %s",
			timeStyle.Render(msg.Time.Format("15:04")),
			userStyle.Render(msg.User+":"),
			content)
		lines = append(lines, line)
	}

	if len(lines) == 0 {
		lines = append(lines, helpStyle.Render("  대화가 없습니다. Mattermost 채널에 메시지를 보내보세요."))
	}

	result := strings.Join(lines, "\n")

	// Input bar
	if m.inputMode {
		inputBar := fmt.Sprintf("\n  > %s_", m.input)
		result += lipgloss.NewStyle().Foreground(highlight).Render(inputBar)
	}

	return result
}

func (m Model) viewLogs() string {
	if len(m.logLines) == 0 {
		return helpStyle.Render("  Dals 탭에서 [l]을 누르면 로그를 볼 수 있습니다\n  [r]을 누르면 재시작 로그가 여기에 표시됩니다")
	}
	maxLines := m.height - 8
	if maxLines < 5 {
		maxLines = 5
	}
	start := 0
	if len(m.logLines) > maxLines {
		start = len(m.logLines) - maxLines
	}
	return strings.Join(m.logLines[start:], "\n")
}
