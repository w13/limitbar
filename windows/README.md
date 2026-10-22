# LimitBar for Windows

Native Windows system tray application to track usage limits, rolling windows, and quota metrics across your AI coding agents and desktop applications.

---

## 🚀 Features on Windows

- **Taskbar System Tray Icon**: Lives in your Windows Taskbar Notification Area with real-time glanceable usage in the tooltip.
- **Native Context Menu**: Right-click or left-click the tray icon to see full breakdowns for each active tool.
- **Start with Windows**: Toggle "Start at Windows Login" directly from the tray menu (manages `HKCU\Software\Microsoft\Windows\CurrentVersion\Run\LimitBar`).
- **Zero Runtime Dependencies**: Self-contained executable with no .NET runtime or WebView2 installation required.
- **CLI & Headless Mode**: Supports `LimitBar.exe --probe` to output a clean JSON snapshot for PowerShell, Command Prompt, or terminal statuslines.

---

## 🧭 Windows Supported Applications & Paths

| Provider | Windows Detection Path | Tracked Metrics |
| :--- | :--- | :--- |
| **Cursor IDE** | `%APPDATA%\Cursor\User\globalStorage\state.vscdb` | Fast requests used (e.g. 0 / 500), monthly reset date, Pro tier |
| **Google Antigravity** | `%APPDATA%\Antigravity\User\globalStorage\state.vscdb` | Google AI Pro / Ultra plan status, Gemini 3 Pro & Claude 4.5 |
| **Claude** | `%APPDATA%\Claude\plan-usage-history.json` or `claude.cmd` | 5-hour rolling session %, 7-day weekly %, model breakdown |
| **ChatGPT / Codex** | `%USERPROFILE%\.codex\sessions\YYYY\MM\DD\*.jsonl` | Weekly rate limit %, available free resets, next reset date |
| **Grok Bot** | `%APPDATA%\Grok Bot\sand-client-persistence\*.blob` | Weekly credit %, on-demand dollar spend vs allowance |
| **GitHub Copilot CLI** | `copilot.cmd` / `copilot.exe` via PATH | Premium requests used (e.g. 1500 / 1500), monthly reset date |
| **DeepSeek API** | `%USERPROFILE%\.deepseek\api_key` or `DEEPSEEK_API_KEY` | Real-time balance (USD / CNY), low balance alert |
| **Kimi Code CLI** | `%USERPROFILE%\.kimi-code\config.toml` | Model tier (`Kimi K3`), remaining balance in CNY (¥) |
| **Reasonix CLI** | `%USERPROFILE%\.reasonix\stats\*.jsonl` & `.env` | Session spend (USD $), active model (`deepseek-v4-pro`) |
| **Muse Code** | `%USERPROFILE%\.config\muse\settings.json` & `auth.json` | Active provider configuration (`Meta`), local session status |
| **Ollama Local AI** | `http://127.0.0.1:11434` | Active model name, VRAM allocation (GB), local readiness |

---

## 🛠️ Building from Source

### On Windows
Requirements: Go 1.22+ installed.
```cmd
cd windows
build.bat
```
This produces `LimitBar.exe`.

### Cross-Compiling on macOS / Linux
```bash
cd windows
./build.sh
```
This produces both:
- `LimitBar.exe` (x86_64)
- `LimitBar-arm64.exe` (ARM64 for Surface Pro & Windows on Snapdragon)

---

## 💻 CLI Mode

To inspect all limits headlessly as JSON:
```cmd
LimitBar.exe --probe
```
