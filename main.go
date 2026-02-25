package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// program is stored globally so background goroutines can push progress updates.
var program *tea.Program

// ── Token persistence ─────────────────────────────────────────────────────────

func tokenConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "github-dl", "token")
}

func saveToken(token string) {
	p := tokenConfigPath()
	_ = os.MkdirAll(filepath.Dir(p), 0700)
	_ = os.WriteFile(p, []byte(token), 0600)
}

func loadSavedToken() string {
	data, err := os.ReadFile(tokenConfigPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func clearSavedToken() { _ = os.Remove(tokenConfigPath()) }

// ── Styles ────────────────────────────────────────────────────────────────────

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7C3AED")).
			MarginBottom(1)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280")).
			MarginBottom(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#7C3AED")).
			Bold(true)

	cursorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#A78BFA"))

	checkedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#EF4444")).
			Bold(true)

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F59E0B")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#6B7280"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#4B5563")).
			MarginTop(1)

	borderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#7C3AED")).
			Padding(0, 1)

	linkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#60A5FA")).
			Underline(true)
)

// ── GitHub types ──────────────────────────────────────────────────────────────

type Repo struct {
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	Description string `json:"description"`
	Private     bool   `json:"private"`
	Language    string `json:"language"`
}

// ── App state ─────────────────────────────────────────────────────────────────

type appState int

const (
	stateToken appState = iota
	stateLoading
	stateRepoList
	stateDownloading
	stateDone
	stateError
)

// ── Messages ──────────────────────────────────────────────────────────────────

type reposFetchedMsg struct{ repos []Repo }
type tokenExpiredMsg struct{}
type progressMsg struct{ current, total int64 }
type fileStartMsg struct {
	index int
	name  string
	size  int64
}
type allDownloadsDoneMsg struct{ paths, errs []string }
type errMsg struct{ err error }

// ── Progress reader ───────────────────────────────────────────────────────────

// progressReader wraps an io.Reader and fires a progressMsg after every chunk.
type progressReader struct {
	r   io.Reader
	cur *int64
	tot int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	*pr.cur += int64(n)
	program.Send(progressMsg{current: *pr.cur, total: pr.tot})
	return n, err
}

// ── Model ─────────────────────────────────────────────────────────────────────

type model struct {
	state       appState
	tokenInput  textinput.Model
	spinner     spinner.Model
	progressBar progress.Model

	token        string
	tokenExpired bool

	repos    []Repo
	cursor   int
	selected map[int]bool

	dlNames    []string
	dlIdx      int
	dlCurName  string
	dlCurBytes int64
	dlCurTotal int64
	dlPaths    []string
	dlErrs     []string

	errMsg string
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "ghp_xxxxxxxxxxxxxxxxxxxx"
	ti.EchoMode = textinput.EchoPassword
	ti.EchoCharacter = '•'
	ti.Focus()
	ti.Width = 50
	ti.CharLimit = 200

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("#7C3AED"))

	pb := progress.New(
		progress.WithGradient("#7C3AED", "#A78BFA"),
		progress.WithWidth(52),
	)

	m := model{
		state:       stateToken,
		tokenInput:  ti,
		spinner:     sp,
		progressBar: pb,
		selected:    make(map[int]bool),
	}

	// Skip the token screen when a valid token was saved from a previous run.
	if saved := loadSavedToken(); saved != "" {
		m.token = saved
		m.state = stateLoading
	}

	return m
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// countSelected returns the number of repos actually selected (value == true).
// We cannot use len(m.selected) because the space toggle stores explicit false
// values in the map when unchecking, which would inflate the count.
func countSelected(selected map[int]bool) int {
	n := 0
	for _, v := range selected {
		if v {
			n++
		}
	}
	return n
}

// ── Commands ──────────────────────────────────────────────────────────────────

func setGitHubHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
}

func fetchRepos(token string) tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 15 * time.Second}

		req, _ := http.NewRequest("GET", "https://api.github.com/user", nil)
		setGitHubHeaders(req, token)

		resp, err := client.Do(req)
		if err != nil {
			return errMsg{fmt.Errorf("network error: %w", err)}
		}
		defer resp.Body.Close()

		if resp.StatusCode == 401 {
			clearSavedToken()
			return tokenExpiredMsg{} // triggers expiry flow
		}
		if resp.StatusCode != 200 {
			return errMsg{fmt.Errorf("GitHub API error: status %d", resp.StatusCode)}
		}

		var user struct {
			Login string `json:"login"`
		}
		json.NewDecoder(resp.Body).Decode(&user)

		var allRepos []Repo
		for page := 1; ; page++ {
			url := fmt.Sprintf(
				"https://api.github.com/user/repos?affiliation=owner&per_page=100&page=%d&sort=updated",
				page,
			)
			req2, _ := http.NewRequest("GET", url, nil)
			setGitHubHeaders(req2, token)

			resp2, err := client.Do(req2)
			if err != nil {
				return errMsg{fmt.Errorf("failed to fetch repos page %d: %w", page, err)}
			}
			defer resp2.Body.Close()

			var repos []Repo
			if err := json.NewDecoder(resp2.Body).Decode(&repos); err != nil {
				return errMsg{fmt.Errorf("failed to parse repos: %w", err)}
			}
			if len(repos) == 0 {
				break
			}
			allRepos = append(allRepos, repos...)
			if len(repos) < 100 {
				break
			}
		}

		saveToken(token) // persist only after a successful auth
		return reposFetchedMsg{repos: allRepos}
	}
}

func downloadReposCmd(token string, repos []Repo) tea.Cmd {
	return func() tea.Msg {
		cwd, _ := os.Getwd()

		client := &http.Client{
			// No global timeout — repos can be large.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				setGitHubHeaders(req, token)
				return nil
			},
		}

		var paths, errs []string

		for i, repo := range repos {
			// Announce before we know the size.
			program.Send(fileStartMsg{index: i, name: repo.Name, size: 0})

			url := fmt.Sprintf("https://api.github.com/repos/%s/zipball/HEAD", repo.FullName)
			req, _ := http.NewRequest("GET", url, nil)
			setGitHubHeaders(req, token)

			resp, err := client.Do(req)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", repo.Name, err))
				continue
			}

			// Re-announce once headers arrive so the UI gets Content-Length.
			program.Send(fileStartMsg{index: i, name: repo.Name, size: resp.ContentLength})

			filename := filepath.Join(cwd, repo.Name+".zip")
			f, err := os.Create(filename)
			if err != nil {
				resp.Body.Close()
				errs = append(errs, fmt.Sprintf("%s: cannot create file: %v", repo.Name, err))
				continue
			}

			var cur int64
			pr := &progressReader{r: resp.Body, cur: &cur, tot: resp.ContentLength}
			_, copyErr := io.Copy(f, pr)
			f.Close()
			resp.Body.Close()

			if copyErr != nil {
				os.Remove(filename)
				errs = append(errs, fmt.Sprintf("%s: download failed: %v", repo.Name, copyErr))
				continue
			}

			paths = append(paths, filename)
		}

		return allDownloadsDoneMsg{paths: paths, errs: errs}
	}
}

// ── Update ────────────────────────────────────────────────────────────────────

func (m model) Init() tea.Cmd {
	if m.state == stateLoading {
		return tea.Batch(m.spinner.Tick, fetchRepos(m.token))
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		key := msg.String()

		switch m.state {

		case stateToken:
			switch key {
			case "ctrl+c", "esc":
				return m, tea.Quit
			case "enter":
				token := strings.TrimSpace(m.tokenInput.Value())
				if token == "" {
					return m, nil
				}
				m.token = token
				m.tokenExpired = false
				m.state = stateLoading
				return m, tea.Batch(m.spinner.Tick, fetchRepos(token))
			}

		case stateRepoList:
			switch key {
			case "ctrl+c", "q":
				return m, tea.Quit
			case "up", "k":
				if m.cursor > 0 {
					m.cursor--
				}
			case "down", "j":
				if m.cursor < len(m.repos)-1 {
					m.cursor++
				}
			case " ":
				m.selected[m.cursor] = !m.selected[m.cursor]
			// 'a' works everywhere; shift+enter works in Kitty-protocol terminals
			// (Kitty, Ghostty, WezTerm, …).
			case "a", "shift+enter":
				for i := range m.repos {
					m.selected[i] = true
				}
			case "n":
				m.selected = make(map[int]bool)
			case "enter":
				if countSelected(m.selected) == 0 {
					return m, nil
				}
				var toDownload []Repo
				var names []string
				for i, repo := range m.repos {
					if m.selected[i] {
						toDownload = append(toDownload, repo)
						names = append(names, repo.Name)
					}
				}
				m.dlNames = names
				m.dlIdx = 0
				m.dlCurBytes = 0
				m.dlCurTotal = 0
				m.dlPaths = nil
				m.dlErrs = nil
				m.state = stateDownloading
				return m, tea.Batch(m.spinner.Tick, downloadReposCmd(m.token, toDownload))
			}
			return m, nil

		case stateDone:
			switch key {
			case "b", "backspace":
				// Go back to the repo list; selections are preserved.
				m.state = stateRepoList
				return m, nil
			case "ctrl+c", "q":
				return m, tea.Quit
			}

		case stateError:
			switch key {
			case "ctrl+c", "q", "esc":
				return m, tea.Quit
			}
		}

	case reposFetchedMsg:
		m.repos = msg.repos
		m.state = stateRepoList
		return m, nil

	case tokenExpiredMsg:
		m.tokenExpired = true
		m.token = ""
		m.state = stateToken
		m.tokenInput.SetValue("")
		m.tokenInput.Focus()
		return m, textinput.Blink

	case fileStartMsg:
		m.dlIdx = msg.index
		m.dlCurName = msg.name
		m.dlCurBytes = 0
		m.dlCurTotal = msg.size
		return m, nil

	case progressMsg:
		m.dlCurBytes = msg.current
		m.dlCurTotal = msg.total
		return m, nil

	case allDownloadsDoneMsg:
		m.dlPaths = msg.paths
		m.dlErrs = msg.errs
		m.state = stateDone
		return m, nil

	case errMsg:
		m.state = stateError
		m.errMsg = msg.err.Error()
		return m, nil

	case spinner.TickMsg:
		if m.state == stateLoading || m.state == stateDownloading {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}

	case progress.FrameMsg:
		pm, cmd := m.progressBar.Update(msg)
		m.progressBar = pm.(progress.Model)
		return m, cmd
	}

	if m.state == stateToken {
		var cmd tea.Cmd
		m.tokenInput, cmd = m.tokenInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m model) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("⬡  github-dl") + "\n")

	switch m.state {

	case stateToken:
		b.WriteString(subtitleStyle.Render("Download your GitHub repos as ZIP files") + "\n\n")
		if m.tokenExpired {
			b.WriteString(warnStyle.Render("⚠  Your saved token has expired — please generate a new one.") + "\n")
			b.WriteString("   " + linkStyle.Render("github.com/settings/tokens/new") +
				dimStyle.Render("  (enable the repo scope)") + "\n\n")
		}
		b.WriteString("Paste your Personal Access Token:\n")
		b.WriteString(dimStyle.Render("(needs repo scope  •  saved to ~/.config/github-dl/token)") + "\n\n")
		b.WriteString(borderStyle.Render(m.tokenInput.View()) + "\n")
		b.WriteString(helpStyle.Render("enter  confirm  •  esc  quit"))

	case stateLoading:
		b.WriteString("\n" + m.spinner.View() + "  Fetching your repositories…\n")

	case stateRepoList:
		count := len(m.repos)
		selCount := countSelected(m.selected) // FIX: was len(m.selected), which counted false entries
		b.WriteString(subtitleStyle.Render(fmt.Sprintf(
			"%d repositories  •  %d selected", count, selCount)) + "\n\n")

		const maxVisible = 20
		start := 0
		if m.cursor >= maxVisible {
			start = m.cursor - maxVisible + 1
		}
		end := start + maxVisible
		if end > len(m.repos) {
			end = len(m.repos)
		}

		for i := start; i < end; i++ {
			repo := m.repos[i]
			checkbox := "[ ]"
			if m.selected[i] {
				checkbox = checkedStyle.Render("[✓]")
			}
			cur := "  "
			if i == m.cursor {
				cur = cursorStyle.Render("▶ ")
			}
			lang := ""
			if repo.Language != "" {
				lang = dimStyle.Render("  " + repo.Language)
			}
			priv := ""
			if repo.Private {
				priv = dimStyle.Render(" 🔒")
			}
			name := repo.Name
			if i == m.cursor {
				name = selectedStyle.Render(name)
			}
			b.WriteString(fmt.Sprintf("%s%s  %s%s%s\n", cur, checkbox, name, priv, lang))
			if i == m.cursor && repo.Description != "" {
				desc := repo.Description
				if len(desc) > 70 {
					desc = desc[:70] + "…"
				}
				b.WriteString("        " + dimStyle.Render(desc) + "\n")
			}
		}

		if count > maxVisible {
			b.WriteString(dimStyle.Render(fmt.Sprintf(
				"\n  showing %d–%d of %d", start+1, end, count)) + "\n")
		}

		b.WriteString(helpStyle.Render(
			"↑↓/jk  navigate  •  space  toggle  •  enter  download  •  a/shift+enter  all  •  n  none  •  q  quit"))

	case stateDownloading:
		total := len(m.dlNames)
		done := m.dlIdx

		b.WriteString(fmt.Sprintf("\n%s  File %d of %d\n\n",
			m.spinner.View(), done+1, total))

		if m.dlCurName != "" {
			b.WriteString("  " + selectedStyle.Render(m.dlCurName+".zip") + "\n\n")

			if m.dlCurTotal > 0 {
				pct := float64(m.dlCurBytes) / float64(m.dlCurTotal)
				if pct > 1 {
					pct = 1
				}
				b.WriteString("  " + m.progressBar.ViewAs(pct) + "\n")
				b.WriteString(fmt.Sprintf("  %s / %s  (%s)\n",
					dimStyle.Render(formatBytes(m.dlCurBytes)),
					dimStyle.Render(formatBytes(m.dlCurTotal)),
					dimStyle.Render(fmt.Sprintf("%.0f%%", pct*100)),
				))
			} else {
				// GitHub didn't send Content-Length (chunked transfer).
				b.WriteString("  " + m.progressBar.ViewAs(0) + "\n")
				b.WriteString(fmt.Sprintf("  %s received  (size unknown)\n",
					dimStyle.Render(formatBytes(m.dlCurBytes))))
			}
		}

		if done > 0 {
			b.WriteString(fmt.Sprintf("\n  %s  %d %s complete\n",
				checkedStyle.Render("✓"), done, pluralFile(done)))
		}

	case stateDone:
		b.WriteString(successStyle.Render("✓  All downloads complete!") + "\n\n")
		cwd, _ := os.Getwd()
		b.WriteString(dimStyle.Render("Saved to: "+cwd) + "\n\n")
		for _, path := range m.dlPaths {
			if path != "" {
				b.WriteString("  " + checkedStyle.Render("✓") + "  " + filepath.Base(path) + "\n")
			}
		}
		if len(m.dlErrs) > 0 {
			b.WriteString("\n" + warnStyle.Render(fmt.Sprintf("⚠  %d failed:", len(m.dlErrs))) + "\n")
			for _, e := range m.dlErrs {
				b.WriteString("  " + errorStyle.Render("✗") + "  " + e + "\n")
			}
		}
		b.WriteString(helpStyle.Render("\nb  back to repo list  •  q  quit"))

	case stateError:
		b.WriteString(errorStyle.Render("✗  Error") + "\n\n")
		b.WriteString(m.errMsg + "\n")
		b.WriteString(helpStyle.Render("\nq  quit"))
	}

	return b.String()
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func formatBytes(b int64) string {
	switch {
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
}

func pluralFile(n int) string {
	if n == 1 {
		return "file"
	}
	return "files"
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	m := initialModel()
	program = tea.NewProgram(m)
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
