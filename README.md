# github-dl

A terminal UI for downloading your GitHub repositories as ZIP files.

## Features

- Paste your PAT token securely (masked input)
- Fetches **only repos you own** (not org repos or collaborator repos)
- Navigate with **↑ ↓ arrow keys**
- Select repos with **spacebar**
- Download multiple repos at once as `.zip` files

## Build

```bash
# Install dependencies
go mod tidy

# Build
go build -o github-dl .

# Run
./github-dl
```

Or run directly:
```bash
go run .
```

## Usage

1. **Run** the binary
2. **Paste your PAT** — needs the `repo` scope (or `public_repo` for public repos only)
   - Generate one at: https://github.com/settings/tokens
3. **Navigate** repos with `↑` / `↓`
4. **Select** with `Space` (toggle), `a` to select all, `n` to deselect all
5. **Download** with `Enter`

ZIPs are saved to your **current working directory**.

## Keybindings

| Key       | Action              |
|-----------|---------------------|
| `↑` / `↓` | Move cursor         |
| `Space`   | Toggle selection    |
| `Enter`   | Download selected   |
| `a`       | Select all          |
| `n`       | Deselect all        |
| `q`       | Quit                |
| `Ctrl+C`  | Quit                |
