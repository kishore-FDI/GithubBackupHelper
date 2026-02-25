# ⬡ github-dl

A terminal UI for downloading your GitHub repositories as ZIP files.


## Features

- Interactive repo browser with fuzzy-friendly keyboard navigation
- Select individual repos or grab everything at once
- Real-time download progress bar per file
- Token saved securely to `~/.config/github-dl/token` after first login
- Automatic token expiry detection with a prompt to re-authenticate

## Installation

```bash
go install github.com/yourusername/github-dl@latest
```

Or build from source:

```bash
git clone https://github.com/yourusername/github-dl
cd github-dl
go build -o github-dl .
```

## Usage

```bash
github-dl
```

On first run you'll be prompted for a GitHub Personal Access Token. After a successful auth the token is saved and future runs skip straight to the repo list.

### Keyboard shortcuts

| Key | Action |
|-----|--------|
| `↑` / `k` | Move cursor up |
| `↓` / `j` | Move cursor down |
| `space` | Toggle selection |
| `a` / `shift+enter` | Select all |
| `n` | Deselect all |
| `enter` | Download selected repos |
| `b` / `backspace` | Back to repo list (from done screen) |
| `q` / `ctrl+c` | Quit |

## Setup

### Generating a token

1. Go to [github.com/settings/tokens/new](https://github.com/settings/tokens/new)
2. Enable the **`repo`** scope
3. Copy the generated token and paste it into the prompt on first run

### Token storage

Your token is stored at `~/.config/github-dl/token` with `0600` permissions (readable only by you). To log out or switch accounts, delete this file:

```bash
rm ~/.config/github-dl/token
```

## Dependencies

```bash
go get github.com/charmbracelet/bubbletea
go get github.com/charmbracelet/bubbles
go get github.com/charmbracelet/lipgloss
```
