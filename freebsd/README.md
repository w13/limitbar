# LimitBar for FreeBSD

Native FreeBSD port to track usage limits, rolling quotas, and spend metrics across your AI coding agents, local LLMs, and developer tools.

---

## 🚀 Features on FreeBSD

- **Two-Column Terminal Dashboard**: Compact 2-column ANSI terminal dashboard rendering rolling 5-hour and weekly quotas, progress meters, and currency spend side-by-side.
- **Status Bar Integration**: Output formatted one-liners with `--bar` or `--polybar` or `--tmux` for Polybar, Waybar, i3status, i3blocks, dwm, and tmux.
- **Waybar JSON Output**: Direct support for Waybar module JSON (`--waybar`) complete with rich HTML tooltips and critical status classes.
- **Background Daemon Mode**: Run `limitbar --daemon` to keep `~/.cache/limitbar/bar.txt` and `~/.cache/limitbar/state.json` hot, triggering desktop notifications (`notify-send`) when limits reach 90%.
- **Zero Runtime Dependencies**: Pure Go with no CGO dependencies or dynamic C libraries required (`statically linked`).
- **Full Probe Engine**: Supports `limitbar --probe` returning the exact JSON schema shared with macOS and Windows.

---

## 🧭 FreeBSD Supported Applications & Paths

| Provider | FreeBSD Detection Paths | Tracked Metrics |
| :--- | :--- | :--- |
| **Cursor IDE** | `~/.config/Cursor/User/globalStorage/state.vscdb` | Fast requests used (e.g. 0 / 500), monthly reset date, Pro tier |
| **Google Antigravity & Gemini** | `~/.config/Antigravity/...` or `/usr/local/bin/agy` | Google AI Pro / Ultra plan status, Gemini 3 Pro & Claude 4.5 Sonnet |
| **Claude Code & Desktop** | `~/.config/Claude/plan-usage-history.json` or `claude` | 5-hour rolling session %, 7-day weekly %, model breakdown |
| **ChatGPT / Codex** | `~/.codex/sessions/YYYY/MM/DD/*.jsonl` or `codex` | Weekly rate limit %, primary 5h %, plan type, next reset date |
| **Grok Bot & xAI** | `~/.config/Grok Bot/sand-client-persistence` or `grok` | Weekly credit %, on-demand dollar spend vs allowance |
| **GitHub Copilot CLI** | `~/.config/github-copilot/hosts.json` or `gh` | Premium requests allowance, monthly reset date |
| **DeepSeek API** | `~/.deepseek/api_key`, `~/.config/deepseek/api_key` | Live balance in USD ($) or CNY (¥), low-balance warnings |
| **Kimi Code CLI** | `~/.kimi-code/config.toml` or `kimi` | Model tier (`Kimi K3`), remaining balance in CNY (¥) |
| **Reasonix CLI** | `~/.reasonix/stats/*.json` & `.env` | Session spend (USD $), model utilization |
| **Muse Code** | `~/.config/muse/settings.json`, `auth.json`, `metrics.db` | Weekly prompts used (e.g. 42 / 200), Contributor Preview tier |
| **Ollama Local AI** | `http://127.0.0.1:11434` or `$OLLAMA_HOST` | Active loaded models, local engine readiness |

---

## 🛠️ Building from Source

### On FreeBSD
Requirements: Go 1.22+ (`pkg install go`).
```sh
cd freebsd
go build -ldflags "-s -w" -o limitbar .
```

### Cross-Compiling on macOS or Linux
```sh
cd freebsd
./build.sh
```
This produces:
- `limitbar-freebsd-amd64` (FreeBSD x86_64)
- `limitbar-freebsd-arm64` (FreeBSD aarch64 / ARM64)

---

## 💻 Usage & Integration

### 1. Interactive Terminal Dashboard
```sh
# Single snapshot
limitbar

# Live auto-refreshing monitor
limitbar --watch --interval 10
```

### 2. Status Bars (Polybar, i3blocks, dwm)
```sh
limitbar --bar
```
Output:
```text
Codex: 12% (5h) · Grok: 4% (w) · Copilot: 0% · DeepSeek: $14.50 · Muse: 21% (w)
```

#### Polybar Module (`~/.config/polybar/config.ini`)
```ini
[module/limitbar]
type = custom/script
exec = cat ~/.cache/limitbar/bar.txt 2>/dev/null || limitbar --bar
interval = 15
```

#### Waybar Module (`~/.config/waybar/config`)
```json
"custom/limitbar": {
    "exec": "limitbar --waybar",
    "return-type": "json",
    "interval": 30,
    "format": "🤖 {}"
}
```

#### Tmux Statusline (`~/.tmux.conf`)
```tmux
set -g status-right '#(limitbar --tmux) | %H:%M '
```

### 3. Background Daemon
```sh
limitbar --daemon --interval 30 &
```
Continuously updates `~/.cache/limitbar/bar.txt` and `~/.cache/limitbar/state.json`, and triggers `notify-send` when any quota exceeds 90%.

### 4. Headless JSON Probe
```sh
limitbar --probe
```
Outputs the complete JSON snapshot of all AI agents.
