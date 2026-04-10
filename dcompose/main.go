package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ── State ────────────────────────────────────────────────────────────────────

type state int

const (
	stateLoading    state = iota
	stateList
	stateGenerating
	stateDone
	stateError
)

// ── Container list item ───────────────────────────────────────────────────────

type container struct {
	id     string
	name   string
	image  string
	status string
}

func (c container) Title() string       { return c.name }
func (c container) Description() string { return c.image + "  (" + c.status + ")" }
func (c container) FilterValue() string { return c.name }

// ── Messages ──────────────────────────────────────────────────────────────────

type containersLoadedMsg struct{ containers []container }
type generateDoneMsg struct{ filename string }
type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	successStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	errorStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("196"))
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	spinnerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	statusStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
)

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	state     state
	list      list.Model
	spinner   spinner.Model
	selected  container
	statusMsg string
	width     int
	height    int
}

func newModel() model {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(lipgloss.Color("205")).
		BorderLeftForeground(lipgloss.Color("205"))
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(lipgloss.Color("183")).
		BorderLeftForeground(lipgloss.Color("205"))

	l := list.New([]list.Item{}, delegate, 0, 0)
	l.Title = "dcompose — select a container"
	l.Styles.Title = titleStyle
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)

	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
		spinner.WithStyle(spinnerStyle),
	)

	return model{
		state:   stateLoading,
		list:    l,
		spinner: s,
	}
}

// ── Init ──────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, fetchContainers())
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			if m.state != stateList {
				return m, tea.Quit
			}
		case "enter":
			switch m.state {
			case stateList:
				if item, ok := m.list.SelectedItem().(container); ok {
					m.selected = item
					m.state = stateGenerating
					m.statusMsg = fmt.Sprintf("Generating %s-compose.yml (may pull image on first run)...", item.name)
					return m, tea.Batch(m.spinner.Tick, generateCompose(item.name))
				}
			case stateDone, stateError:
				return m, tea.Quit
			}
		}

	case spinner.TickMsg:
		if m.state == stateLoading || m.state == stateGenerating {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case containersLoadedMsg:
		if len(msg.containers) == 0 {
			m.state = stateError
			m.statusMsg = "No running containers found. Start a container and try again."
			return m, nil
		}
		m.state = stateList
		items := make([]list.Item, len(msg.containers))
		for i, c := range msg.containers {
			items[i] = c
		}
		m.list.SetItems(items)
		return m, nil

	case generateDoneMsg:
		m.state = stateDone
		m.statusMsg = fmt.Sprintf("Saved %s  (mac_address lines removed)", msg.filename)
		return m, nil

	case errMsg:
		m.state = stateError
		m.statusMsg = msg.err.Error()
		return m, nil
	}

	if m.state == stateList {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	switch m.state {
	case stateLoading:
		return fmt.Sprintf("\n  %s Fetching running containers…\n", m.spinner.View())

	case stateList:
		return "\n" + m.list.View()

	case stateGenerating:
		return fmt.Sprintf("\n  %s %s\n", m.spinner.View(), statusStyle.Render(m.statusMsg))

	case stateDone:
		return fmt.Sprintf("\n  %s\n\n  %s\n\n  %s\n",
			successStyle.Render("Done!"),
			statusStyle.Render(m.statusMsg),
			hintStyle.Render("Press any key to exit."),
		)

	case stateError:
		return fmt.Sprintf("\n  %s\n\n  %s\n\n  %s\n",
			errorStyle.Render("Error"),
			statusStyle.Render(m.statusMsg),
			hintStyle.Render("Press any key to exit."),
		)
	}
	return ""
}

// ── Commands ──────────────────────────────────────────────────────────────────

func fetchContainers() tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("docker", "ps",
			"--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.Status}}",
		).Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return errMsg{fmt.Errorf("docker ps failed: %s", exitErr.Stderr)}
			}
			return errMsg{fmt.Errorf("docker ps: %w", err)}
		}
		return containersLoadedMsg{containers: parseContainers(string(out))}
	}
}

func parseContainers(output string) []container {
	var containers []container
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 4)
		if len(parts) < 4 {
			continue
		}
		containers = append(containers, container{
			id:     parts[0],
			name:   parts[1],
			image:  parts[2],
			status: parts[3],
		})
	}
	return containers
}

func isNetworkError(stderr string) bool {
	networkIndicators := []string{
		"no such host",
		"network is unreachable",
		"connection refused",
		"dial tcp",
		"TLS handshake timeout",
		"i/o timeout",
		"EOF",
		"unable to pull",
		"pull access denied",
	}
	lower := strings.ToLower(stderr)
	for _, indicator := range networkIndicators {
		if strings.Contains(lower, strings.ToLower(indicator)) {
			return true
		}
	}
	return false
}

func generateCompose(name string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("docker", "run", "--rm",
			"-v", "/var/run/docker.sock:/var/run/docker.sock",
			"ghcr.io/red5d/docker-autocompose",
			name,
		)

		output, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				stderr := strings.TrimSpace(string(exitErr.Stderr))
				if isNetworkError(stderr) {
					return errMsg{fmt.Errorf("network error: unable to pull ghcr.io/red5d/docker-autocompose. Check your internet connection and try again.")}
				}
				return errMsg{fmt.Errorf("docker-autocompose failed: %s", stderr)}
			}
			return errMsg{fmt.Errorf("docker-autocompose: %w", err)}
		}

		// Strip mac_address: lines in memory
		var filtered strings.Builder
		for _, line := range strings.Split(string(output), "\n") {
			if !strings.Contains(line, "mac_address:") {
				filtered.WriteString(line)
				filtered.WriteByte('\n')
			}
		}

		filename := name + "-compose.yml"
		if err := os.WriteFile(filename, []byte(filtered.String()), 0644); err != nil {
			return errMsg{fmt.Errorf("writing file: %w", err)}
		}

		return generateDoneMsg{filename: filename}
	}
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	if _, err := exec.LookPath("docker"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: 'docker' not found in PATH. Install Docker and try again.")
		os.Exit(1)
	}

	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
