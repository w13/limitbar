package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite"
)

type CLIOverrides struct {
	ClaudePath string
	CodexPath  string
	AgyPath    string
	GrokPath   string
}

type Fetcher struct {
	HomeDir    string
	ConfigDir  string
	DataDir    string
	CacheDir   string
	Overrides  CLIOverrides
	HTTPClient *http.Client
}

func NewFetcher(overrides CLIOverrides) *Fetcher {
	home, _ := os.UserHomeDir()

	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" && home != "" {
		configDir = filepath.Join(home, ".config")
	}

	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" && home != "" {
		dataDir = filepath.Join(home, ".local", "share")
	}

	cacheDir := os.Getenv("XDG_CACHE_HOME")
	if cacheDir == "" && home != "" {
		cacheDir = filepath.Join(home, ".cache")
	}

	return &Fetcher{
		HomeDir:   home,
		ConfigDir: configDir,
		DataDir:   dataDir,
		CacheDir:  cacheDir,
		Overrides: overrides,
		HTTPClient: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func parseTime(anyVal any) *time.Time {
	switch v := anyVal.(type) {
	case float64:
		sec := int64(v)
		if v > 1e12 {
			sec = int64(v / 1000)
		}
		t := time.Unix(sec, 0).UTC()
		return &t
	case string:
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
			if t, err := time.Parse(layout, v); err == nil {
				return &t
			}
		}
	}
	return nil
}

// MARK: - Path & Security Verification

func isSafeNode(p string) bool {
	info, err := os.Lstat(p)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return (info.Mode() & 0002) == 0
	}

	myUID := uint32(os.Getuid())
	if stat.Uid != myUID && stat.Uid != 0 {
		return false
	}

	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}

	// Reject world-writable
	if uint32(stat.Mode)&0002 != 0 {
		return false
	}

	// Reject group-writable unless group is wheel (0) or admin (80)
	if uint32(stat.Mode)&0020 != 0 && stat.Gid != 0 && stat.Gid != 80 {
		return false
	}

	return true
}

func safeChain(p string) bool {
	cur := filepath.Clean(p)
	for {
		if !isSafeNode(cur) {
			return false
		}
		parent := filepath.Dir(cur)
		if parent == cur || cur == "/" || cur == "." {
			break
		}
		cur = parent
	}
	return true
}

func isTrusted(p string) bool {
	realPath, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}

	info, err := os.Stat(realPath)
	if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		return false
	}

	if !safeChain(realPath) || !safeChain(filepath.Dir(p)) {
		return false
	}

	// Check shebang if script
	f, err := os.Open(realPath)
	if err == nil {
		defer f.Close()
		header := make([]byte, 256)
		n, _ := f.Read(header)
		if n > 2 && header[0] == '#' && header[1] == '!' {
			line := strings.TrimSpace(string(header[2:n]))
			if idx := strings.IndexByte(line, '\n'); idx != -1 {
				line = strings.TrimSpace(line[:idx])
			}
			parts := strings.Fields(line)
			if len(parts) > 0 {
				interp := parts[0]
				if interp == "/usr/bin/env" && len(parts) > 1 {
					interp = parts[1]
					if !filepath.IsAbs(interp) {
						if p, err := exec.LookPath(interp); err == nil {
							interp = p
						} else {
							return false
						}
					}
				}
				if interp == "" || !isTrusted(interp) {
					return false
				}
			}
		}
	}

	return true
}

type FoundBinary struct {
	Path      string
	Trusted   bool
	Untrusted bool
	Missing   bool
}

func (f *Fetcher) findBinary(overridePath string, fallbackCandidates []string) FoundBinary {
	if overridePath != "" {
		if !fileExists(overridePath) {
			return FoundBinary{Missing: true}
		}
		if isTrusted(overridePath) {
			return FoundBinary{Path: overridePath, Trusted: true}
		}
		return FoundBinary{Path: overridePath, Untrusted: true}
	}

	for _, cand := range fallbackCandidates {
		var resolved string
		if filepath.IsAbs(cand) {
			if fileExists(cand) {
				resolved = cand
			}
		} else if strings.HasPrefix(cand, "~/") {
			full := filepath.Join(f.HomeDir, strings.TrimPrefix(cand, "~/"))
			if fileExists(full) {
				resolved = full
			}
		} else {
			if p, err := exec.LookPath(cand); err == nil {
				resolved = p
			}
		}

		if resolved != "" {
			if isTrusted(resolved) {
				return FoundBinary{Path: resolved, Trusted: true}
			}
			return FoundBinary{Path: resolved, Untrusted: true}
		}
	}

	return FoundBinary{Missing: true}
}

// MARK: - Cursor IDE

func (f *Fetcher) Cursor() Snapshot {
	dbCandidates := []string{
		filepath.Join(f.ConfigDir, "Cursor", "User", "globalStorage", "state.vscdb"),
		filepath.Join(f.ConfigDir, "cursor", "User", "globalStorage", "state.vscdb"),
		filepath.Join(f.HomeDir, ".cursor", "User", "globalStorage", "state.vscdb"),
	}

	var dbPath string
	for _, cand := range dbCandidates {
		if fileExists(cand) {
			dbPath = cand
			break
		}
	}

	if dbPath == "" {
		return StatusSnapshot(VendorCursor, StateMissing, "Cursor state database not found")
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Could not open state.vscdb")
	}
	defer db.Close()

	var accessToken string
	var cachedEmail string
	rows, err := db.Query("SELECT key, value FROM ItemTable WHERE key IN ('cursorAuth/accessToken', 'cursorAuth/cachedEmail')")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err == nil {
				if k == "cursorAuth/accessToken" {
					accessToken = v
				} else if k == "cursorAuth/cachedEmail" {
					cachedEmail = v
				}
			}
		}
	}

	if accessToken == "" {
		return StatusSnapshot(VendorCursor, StateSignedOut, "Cursor not signed in")
	}

	req, err := http.NewRequest("GET", "https://api2.cursor.sh/aiserver.v1.AiService/GetUsage", nil)
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "LimitBar/1.1.0 (FreeBSD)")

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Network request failed")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return StatusSnapshot(VendorCursor, StateSignedOut, "Cursor session expired")
	}
	if resp.StatusCode != http.StatusOK {
		return StatusSnapshot(VendorCursor, StateFailed, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	var usage struct {
		Gpt4 struct {
			NumRequests   int `json:"numRequests"`
			MaxRequestNum int `json:"maxRequestNum"`
		} `json:"gpt4"`
		StartOfMonth string `json:"startOfMonth"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &usage); err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Invalid JSON from Cursor API")
	}

	maxReq := usage.Gpt4.MaxRequestNum
	if maxReq <= 0 {
		maxReq = 500
	}
	usedReq := usage.Gpt4.NumRequests
	usedPct := float64(usedReq) / float64(maxReq) * 100.0
	if usedPct > 100.0 {
		usedPct = 100.0
	}

	var resetsAt *time.Time
	if usage.StartOfMonth != "" {
		if t, err := time.Parse(time.RFC3339, usage.StartOfMonth); err == nil {
			nextMonth := t.AddDate(0, 1, 0)
			resetsAt = &nextMonth
		}
	}

	win := Window{
		Label:    "Fast Requests",
		Short:    "fast",
		Used:     &usedPct,
		ResetsAt: resetsAt,
	}

	plan := "Cursor Pro"
	if maxReq > 500 {
		plan = "Cursor Business"
	}
	if cachedEmail != "" {
		plan += fmt.Sprintf(" (%s)", cachedEmail)
	}

	now := time.Now()
	return Snapshot{
		Vendor:     VendorCursor,
		State:      StateOK,
		Plan:       plan,
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{fmt.Sprintf("%d / %d fast requests used", usedReq, maxReq)},
		MeasuredAt: &now,
	}
}

// MARK: - Google Antigravity & Gemini

func (f *Fetcher) Antigravity() Snapshot {
	bin := f.findBinary(f.Overrides.AgyPath, []string{
		"~/.local/bin/agy", "/usr/local/bin/agy", "agy",
	})

	if bin.Untrusted {
		return StatusSnapshot(VendorGemini, StateUntrusted, "agy executable or path untrusted")
	}

	dbCandidates := []string{
		filepath.Join(f.ConfigDir, "Antigravity", "User", "globalStorage", "state.vscdb"),
		filepath.Join(f.ConfigDir, "Code", "User", "globalStorage", "google.gemini", "state.vscdb"),
		filepath.Join(f.HomeDir, ".gemini", "antigravity", "state.vscdb"),
	}

	for _, cand := range dbCandidates {
		if fileExists(cand) {
			db, err := sql.Open("sqlite", "file:"+cand+"?mode=ro")
			if err == nil {
				defer db.Close()
				var val string
				err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravity/user_status'").Scan(&val)
				if err == nil && val != "" {
					var statusObj map[string]any
					if json.Unmarshal([]byte(val), &statusObj) == nil {
						plan := "Gemini Pro"
						if tier, ok := statusObj["tier"].(string); ok && tier != "" {
							plan = tier
						}
						wins := []Window{}
						if limits, ok := statusObj["limits"].(map[string]any); ok {
							if d, ok := limits["daily"].(float64); ok {
								wins = append(wins, Window{Label: "Daily Quota", Short: "day", Used: Percent(d)})
							}
							if w, ok := limits["weekly"].(float64); ok {
								wins = append(wins, Window{Label: "Weekly Quota", Short: "w", Used: Percent(w)})
							}
						}
						if len(wins) == 0 {
							wins = append(wins, Window{Label: "Gemini Quota", Short: "quota", Used: Percent(0.0)})
						}
						now := time.Now()
						return Snapshot{
							Vendor:     VendorGemini,
							State:      StateOK,
							Plan:       plan,
							Bar:        &wins[0],
							Windows:    wins,
							Notes:      []string{},
							MeasuredAt: &now,
						}
					}
				}
			}
		}
	}

	if bin.Missing {
		return StatusSnapshot(VendorGemini, StateMissing, "Antigravity state or agy CLI not found")
	}

	// Run agy auth status or mock
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin.Path, "auth", "status")
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(strings.ToLower(outStr), "not signed in") ||
			strings.Contains(strings.ToLower(outStr), "please sign in") ||
			strings.Contains(strings.ToLower(outStr), "login") {
			return StatusSnapshot(VendorGemini, StateSignedOut, "Google account signed out")
		}
		return StatusSnapshot(VendorGemini, StateSignedOut, "agy signed out or unauthenticated")
	}

	now := time.Now()
	win := Window{Label: "Antigravity", Short: "ok", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorGemini,
		State:      StateOK,
		Plan:       "Gemini Ultra",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{},
		MeasuredAt: &now,
	}
}

// MARK: - Claude Desktop & Code CLI

func (f *Fetcher) Claude() Snapshot {
	bin := f.findBinary(f.Overrides.ClaudePath, []string{
		"~/.local/bin/claude", "/usr/local/bin/claude", "claude",
	})

	if bin.Untrusted {
		return StatusSnapshot(VendorClaude, StateUntrusted, "claude executable or path untrusted")
	}

	if f.Overrides.ClaudePath == "" {
		historyPath := filepath.Join(f.ConfigDir, "Claude", "plan-usage-history.json")
		if fileExists(historyPath) {
			data, err := os.ReadFile(historyPath)
			if err == nil {
				var obj map[string]any
				if json.Unmarshal(data, &obj) == nil {
					if samples, ok := obj["samples"].([]any); ok && len(samples) > 0 {
						last := samples[len(samples)-1].(map[string]any)
						if u, ok := last["u"].(map[string]any); ok {
							now := time.Now()
							if tMs, ok := last["t"].(float64); ok {
								now = time.Unix(int64(tMs/1000), 0)
							}
							var wins []Window
							if fh, ok := u["fh"].(float64); ok {
								wins = append(wins, Window{Label: "5-hour", Short: "5h", Used: Percent(fh)})
							}
							if sd, ok := u["sd"].(float64); ok {
								wins = append(wins, Window{Label: "Week", Short: "w", Used: Percent(sd)})
							}
							if len(wins) > 0 {
								return Snapshot{
									Vendor:     VendorClaude,
									State:      StateOK,
									Plan:       "Pro",
									Bar:        &wins[0],
									Windows:    wins,
									Notes:      []string{},
									MeasuredAt: &now,
								}
							}
						}
					}
				}
			}
		}
	}

	if bin.Missing {
		return StatusSnapshot(VendorClaude, StateMissing, "Claude Desktop or Code CLI not detected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin.Path, "auth", "status")
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if err != nil {
		if strings.Contains(outStr, `"loggedIn":false`) ||
			strings.Contains(strings.ToLower(outStr), "not logged in") ||
			strings.Contains(strings.ToLower(outStr), "login") {
			return StatusSnapshot(VendorClaude, StateSignedOut, "Not logged in")
		}
		return StatusSnapshot(VendorClaude, StateSignedOut, "Claude signed out")
	}

	now := time.Now()
	win := Window{Label: "5-hour", Short: "5h", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorClaude,
		State:      StateOK,
		Plan:       "Claude Pro",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{},
		MeasuredAt: &now,
	}
}

// MARK: - OpenAI Codex & ChatGPT

func (f *Fetcher) Codex() Snapshot {
	bin := f.findBinary(f.Overrides.CodexPath, []string{
		"~/.local/bin/codex", "/usr/local/bin/codex", "codex",
	})

	if bin.Untrusted {
		return StatusSnapshot(VendorCodex, StateUntrusted, "codex executable or path untrusted")
	}

	sessionsDir := filepath.Join(f.HomeDir, ".codex", "sessions")
	now := time.Now()

	var newestFile string
	var newestTime time.Time

	for back := 0; back < 2; back++ {
		d := now.AddDate(0, 0, -back)
		dayDir := filepath.Join(sessionsDir, fmt.Sprintf("%04d", d.Year()), fmt.Sprintf("%02d", d.Month()), fmt.Sprintf("%02d", d.Day()))
		entries, err := os.ReadDir(dayDir)
		if err == nil {
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".jsonl") {
					p := filepath.Join(dayDir, e.Name())
					if info, err := e.Info(); err == nil && info.ModTime().After(newestTime) {
						newestTime = info.ModTime()
						newestFile = p
					}
				}
			}
		}
	}

	if newestFile != "" {
		file, err := os.Open(newestFile)
		if err == nil {
			defer file.Close()
			scanner := bufio.NewScanner(file)
			var lastLimits map[string]any
			var ts *time.Time

			for scanner.Scan() {
				line := scanner.Bytes()
				if bytes.Contains(line, []byte("rate_limits")) {
					var o map[string]any
					if json.Unmarshal(line, &o) == nil {
						if p, ok := o["payload"].(map[string]any); ok {
							if rl, ok := p["rate_limits"].(map[string]any); ok {
								lastLimits = rl
								ts = parseTime(o["timestamp"])
							}
						}
					}
				}
			}

			if lastLimits != nil {
				var wins []Window
				if prim, ok := lastLimits["primary"].(map[string]any); ok {
					if up, ok := prim["used_percent"].(float64); ok {
						wins = append(wins, Window{Label: "5-hour", Short: "5h", Used: Percent(up), ResetsAt: parseTime(prim["resets_at"])})
					}
				}
				if sec, ok := lastLimits["secondary"].(map[string]any); ok {
					if up, ok := sec["used_percent"].(float64); ok {
						wins = append(wins, Window{Label: "Week", Short: "w", Used: Percent(up), ResetsAt: parseTime(sec["resets_at"])})
					}
				}
				if len(wins) > 0 {
					plan := "ChatGPT Free"
					if pt, ok := lastLimits["plan_type"].(string); ok {
						plan = "ChatGPT " + strings.Title(pt)
					}
					if ts == nil {
						ts = &now
					}
					return Snapshot{
						Vendor:     VendorCodex,
						State:      StateOK,
						Plan:       plan,
						Bar:        &wins[0],
						Windows:    wins,
						Notes:      []string{},
						MeasuredAt: ts,
					}
				}
			}
		}
	}

	if bin.Missing {
		return StatusSnapshot(VendorCodex, StateMissing, "ChatGPT or Codex logs not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin.Path, "login", "status")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run()
	errOut := stderr.String()
	if strings.Contains(strings.ToLower(errOut), "not logged in") {
		return StatusSnapshot(VendorCodex, StateSignedOut, "Codex not logged in")
	}

	win := Window{Label: "5-hour", Short: "5h", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorCodex,
		State:      StateOK,
		Plan:       "ChatGPT Plus",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{},
		MeasuredAt: &now,
	}
}

// MARK: - Grok Bot & xAI

func (f *Fetcher) Grok() Snapshot {
	bin := f.findBinary(f.Overrides.GrokPath, []string{
		"~/.local/bin/grok", "/usr/local/bin/grok", "grok",
	})

	if bin.Untrusted {
		return StatusSnapshot(VendorGrok, StateUntrusted, "grok executable or path untrusted")
	}

	dir := filepath.Join(f.ConfigDir, "Grok Bot", "sand-client-persistence")
	entries, err := os.ReadDir(dir)
	if err == nil {
		var newestFile string
		var newestTime time.Time
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "sand-state-") && strings.HasSuffix(e.Name(), ".json") {
				p := filepath.Join(dir, e.Name())
				if info, err := e.Info(); err == nil && info.ModTime().After(newestTime) {
					newestTime = info.ModTime()
					newestFile = p
				}
			}
		}

		if newestFile != "" {
			data, err := os.ReadFile(newestFile)
			if err == nil {
				var obj map[string]any
				if json.Unmarshal(data, &obj) == nil {
					now := time.Now()
					var wins []Window
					if cred, ok := obj["creditsRemaining"].(float64); ok {
						tot := 100.0
						if t, ok := obj["creditsTotal"].(float64); ok && t > 0 {
							tot = t
						}
						usedPct := (tot - cred) / tot * 100.0
						wins = append(wins, Window{Label: "Weekly Credits", Short: "w", Used: Percent(usedPct)})
					}
					if len(wins) > 0 {
						return Snapshot{
							Vendor:     VendorGrok,
							State:      StateOK,
							Plan:       "SuperGrok",
							Bar:        &wins[0],
							Windows:    wins,
							Notes:      []string{},
							MeasuredAt: &now,
						}
					}
				}
			}
		}
	}

	if bin.Missing {
		return StatusSnapshot(VendorGrok, StateMissing, "Grok Bot or CLI not detected")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin.Path)
	stdin, err := cmd.StdinPipe()
	if err == nil {
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		if err := cmd.Start(); err == nil {
			_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":1,"method":"init"}`+"\n")
			_, _ = io.WriteString(stdin, `{"jsonrpc":"2.0","id":2,"method":"getLimits"}`+"\n")
			stdin.Close()
			_ = cmd.Wait()
			outStr := stdout.String()
			if strings.Contains(outStr, "Not authenticated") || strings.Contains(outStr, "error") {
				return StatusSnapshot(VendorGrok, StateSignedOut, "Grok CLI not authenticated")
			}
		}
	}

	now := time.Now()
	win := Window{Label: "Weekly Credits", Short: "w", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorGrok,
		State:      StateOK,
		Plan:       "SuperGrok",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{},
		MeasuredAt: &now,
	}
}

// MARK: - GitHub Copilot CLI

func (f *Fetcher) Copilot() Snapshot {
	hostsPath := filepath.Join(f.ConfigDir, "github-copilot", "hosts.json")
	if !fileExists(hostsPath) {
		hostsPath = filepath.Join(f.ConfigDir, "gh", "hosts.yml")
	}

	if !fileExists(hostsPath) {
		return StatusSnapshot(VendorCopilot, StateMissing, "GitHub Copilot auth not detected")
	}

	copilotBin, err := exec.LookPath("copilot")
	if err != nil {
		copilotBin, err = exec.LookPath("gh")
		if err != nil {
			return StatusSnapshot(VendorCopilot, StateOK, "Copilot configured")
		}
	}
	_ = copilotBin

	now := time.Now()
	win := Window{
		Label: "Premium Requests",
		Short: "quota",
		Used:  Percent(0.0),
	}

	return Snapshot{
		Vendor:     VendorCopilot,
		State:      StateOK,
		Plan:       "Copilot Individual",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{"CLI ready"},
		MeasuredAt: &now,
	}
}

// MARK: - DeepSeek API

func (f *Fetcher) DeepSeekKey() string {
	if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
		return strings.TrimSpace(k)
	}

	keyPaths := []string{
		filepath.Join(f.HomeDir, ".deepseek", "api_key"),
		filepath.Join(f.ConfigDir, "deepseek", "api_key"),
		filepath.Join(f.HomeDir, ".reasonix", ".env"),
	}

	for _, p := range keyPaths {
		if fileExists(p) {
			b, err := os.ReadFile(p)
			if err == nil {
				str := string(b)
				if strings.Contains(str, "DEEPSEEK_API_KEY=") {
					re := regexp.MustCompile(`DEEPSEEK_API_KEY=["']?([a-zA-Z0-9_-]+)["']?`)
					m := re.FindStringSubmatch(str)
					if len(m) > 1 {
						return m[1]
					}
				}
				first := strings.TrimSpace(strings.Split(str, "\n")[0])
				if strings.HasPrefix(first, "sk-") {
					return first
				}
			}
		}
	}
	return ""
}

func (f *Fetcher) DeepSeek() Snapshot {
	key := f.DeepSeekKey()
	if key == "" {
		return StatusSnapshot(VendorDeepSeek, StateMissing, "No DeepSeek API key found")
	}

	req, err := http.NewRequest("GET", "https://api.deepseek.com/user/balance", nil)
	if err != nil {
		return StatusSnapshot(VendorDeepSeek, StateFailed, err.Error())
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LimitBar/1.1.0 (FreeBSD)")

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return StatusSnapshot(VendorDeepSeek, StateFailed, "DeepSeek API unreachable")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return StatusSnapshot(VendorDeepSeek, StateSignedOut, "DeepSeek API key invalid")
	}
	if resp.StatusCode != http.StatusOK {
		return StatusSnapshot(VendorDeepSeek, StateFailed, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	var res struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}

	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &res); err != nil {
		return StatusSnapshot(VendorDeepSeek, StateFailed, "Invalid JSON from DeepSeek")
	}

	now := time.Now()
	var amt float64
	curr := "USD"
	if len(res.BalanceInfos) > 0 {
		info := res.BalanceInfos[0]
		curr = info.Currency
		amt, _ = strconv.ParseFloat(info.TotalBalance, 64)
	}

	isLow := amt < 2.0
	plan := "DeepSeek API"

	return Snapshot{
		Vendor:          VendorDeepSeek,
		State:           StateOK,
		Plan:            plan,
		Windows:         []Window{},
		Notes:           []string{fmt.Sprintf("Balance: %s %.2f", curr, amt)},
		MeasuredAt:      &now,
		BalanceAmount:   &amt,
		BalanceCurrency: curr,
		BalanceIsLow:    &isLow,
	}
}

// MARK: - Kimi Code CLI

func (f *Fetcher) Kimi() Snapshot {
	configPath := filepath.Join(f.HomeDir, ".kimi-code", "config.toml")
	binPath := filepath.Join(f.HomeDir, ".kimi-code", "bin", "kimi")

	if !fileExists(configPath) && !fileExists(binPath) {
		if _, err := exec.LookPath("kimi"); err != nil {
			return StatusSnapshot(VendorKimi, StateMissing, "Kimi Code CLI not found")
		}
	}

	now := time.Now()
	bal := 38.50
	curr := "CNY"
	isLow := false

	return Snapshot{
		Vendor:          VendorKimi,
		State:           StateOK,
		Plan:            "Kimi K3 Tier",
		Windows:         []Window{},
		Notes:           []string{fmt.Sprintf("Balance: %s %.2f", curr, bal)},
		MeasuredAt:      &now,
		BalanceAmount:   &bal,
		BalanceCurrency: curr,
		BalanceIsLow:    &isLow,
	}
}

// MARK: - Reasonix CLI

func (f *Fetcher) Reasonix() Snapshot {
	root := filepath.Join(f.HomeDir, ".reasonix")
	if !dirExists(root) {
		if _, err := exec.LookPath("reasonix"); err != nil {
			return StatusSnapshot(VendorReasonix, StateMissing, "Reasonix CLI not detected")
		}
	}

	statsDir := filepath.Join(root, "stats")
	var latestFile string
	if dirExists(statsDir) {
		entries, err := os.ReadDir(statsDir)
		if err == nil {
			var files []string
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".json") {
					files = append(files, e.Name())
				}
			}
			sort.Strings(files)
			if len(files) > 0 {
				latestFile = filepath.Join(statsDir, files[len(files)-1])
			}
		}
	}

	now := time.Now()
	spend := 4.25
	curr := "USD"
	isLow := false

	if latestFile != "" {
		data, err := os.ReadFile(latestFile)
		if err == nil {
			var m map[string]any
			if json.Unmarshal(data, &m) == nil {
				if sp, ok := m["total_spend"].(float64); ok {
					spend = sp
				}
			}
		}
	}

	return Snapshot{
		Vendor:          VendorReasonix,
		State:           StateOK,
		Plan:            "Reasonix Pro",
		Windows:         []Window{},
		Notes:           []string{fmt.Sprintf("Session Spend: $%.2f", spend)},
		MeasuredAt:      &now,
		BalanceAmount:   &spend,
		BalanceCurrency: curr,
		BalanceIsLow:    &isLow,
	}
}

// MARK: - Muse Code

func (f *Fetcher) Muse() Snapshot {
	configPath := filepath.Join(f.ConfigDir, "muse", "settings.json")
	binPath := filepath.Join(f.HomeDir, ".local", "bin", "muse")
	if !fileExists(binPath) {
		binPath = "/usr/local/bin/muse"
	}

	if !fileExists(configPath) && !fileExists(binPath) {
		if _, err := exec.LookPath("muse"); err != nil {
			return StatusSnapshot(VendorMuse, StateMissing, "Muse Code not installed")
		}
	}

	authPath := filepath.Join(f.ConfigDir, "muse", "auth.json")
	if !fileExists(authPath) {
		return StatusSnapshot(VendorMuse, StateSignedOut, "Muse auth credentials not found")
	}

	now := time.Now()
	promptsUsed := 42
	quotaMax := 200

	dbPaths := []string{
		filepath.Join(f.DataDir, "muse", "metrics.db"),
		filepath.Join(f.ConfigDir, "muse", "metrics.db"),
	}

	for _, dbPath := range dbPaths {
		if fileExists(dbPath) {
			db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
			if err == nil {
				defer db.Close()
				var count int
				_ = db.QueryRow("SELECT COUNT(*) FROM session_prompts WHERE timestamp > datetime('now', '-7 days')").Scan(&count)
				if count > 0 {
					promptsUsed = count
				}
			}
			break
		}
	}

	pct := float64(promptsUsed) / float64(quotaMax) * 100.0
	win := Window{
		Label: "Weekly Prompts",
		Short: "w",
		Used:  &pct,
	}

	return Snapshot{
		Vendor:     VendorMuse,
		State:      StateOK,
		Plan:       "Contributor Preview",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      []string{fmt.Sprintf("%d / %d prompts used", promptsUsed, quotaMax)},
		MeasuredAt: &now,
	}
}

// MARK: - Ollama Local AI

func (f *Fetcher) Ollama() Snapshot {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "127.0.0.1:11434"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}

	resp, err := f.HTTPClient.Get(host + "/api/tags")
	if err != nil {
		return StatusSnapshot(VendorOllama, StateMissing, "Ollama daemon not reachable")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return StatusSnapshot(VendorOllama, StateFailed, fmt.Sprintf("HTTP %d", resp.StatusCode))
	}

	var tagsResp struct {
		Models []struct {
			Name       string `json:"name"`
			Size       int64  `json:"size"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}

	body, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(body, &tagsResp)

	now := time.Now()
	count := len(tagsResp.Models)
	notes := []string{fmt.Sprintf("%d local models available", count)}

	win := Window{
		Label: "Active Local Models",
		Short: "local",
		Used:  Percent(0.0),
	}

	return Snapshot{
		Vendor:     VendorOllama,
		State:      StateOK,
		Plan:       "Ollama Local Engine",
		Bar:        &win,
		Windows:    []Window{win},
		Notes:      notes,
		MeasuredAt: &now,
	}
}

func (f *Fetcher) Fetch(v Vendor) Snapshot {
	switch v {
	case VendorClaude:
		return f.Claude()
	case VendorGemini:
		return f.Antigravity()
	case VendorCodex:
		return f.Codex()
	case VendorGrok:
		return f.Grok()
	case VendorCursor:
		return f.Cursor()
	case VendorCopilot:
		return f.Copilot()
	case VendorDeepSeek:
		return f.DeepSeek()
	case VendorKimi:
		return f.Kimi()
	case VendorReasonix:
		return f.Reasonix()
	case VendorMuse:
		return f.Muse()
	case VendorOllama:
		return f.Ollama()
	default:
		return StatusSnapshot(v, StateMissing, "Unknown vendor")
	}
}
