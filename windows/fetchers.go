package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/base64"
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
	"time"

	_ "modernc.org/sqlite"
)

type Fetcher struct {
	HomeDir      string
	AppData      string
	LocalAppData string
	HTTPClient   *http.Client
}

func NewFetcher() *Fetcher {
	home, _ := os.UserHomeDir()
	appData := os.Getenv("APPDATA")
	if appData == "" && home != "" {
		appData = filepath.Join(home, "AppData", "Roaming")
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" && home != "" {
		localAppData = filepath.Join(home, "AppData", "Local")
	}

	return &Fetcher{
		HomeDir:      home,
		AppData:      appData,
		LocalAppData: localAppData,
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

// MARK: - Cursor IDE

func (f *Fetcher) Cursor() Snapshot {
	dbPath := filepath.Join(f.AppData, "Cursor", "User", "globalStorage", "state.vscdb")
	if !fileExists(dbPath) {
		return StatusSnapshot(VendorCursor, StateMissing, "Cursor IDE not installed")
	}

	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Could not open Cursor state database")
	}
	defer db.Close()

	queryKey := func(k string) string {
		var val string
		_ = db.QueryRow("SELECT value FROM ItemTable WHERE key = ? LIMIT 1", k).Scan(&val)
		return val
	}

	token := queryKey("cursorAuth/accessToken")
	if token == "" {
		return StatusSnapshot(VendorCursor, StateSignedOut, "Sign in through Cursor IDE")
	}

	var userId string
	profStr := queryKey("cursorAuth/cachedScopedProfile")
	if profStr != "" {
		var prof map[string]any
		if json.Unmarshal([]byte(profStr), &prof) == nil {
			if authId, ok := prof["authId"].(string); ok {
				parts := strings.Split(authId, "|")
				userId = parts[len(parts)-1]
			}
		}
	}
	if userId == "" {
		userId = queryKey("cursorAuth/cachedEmail")
	}

	plan := queryKey("cursorAuth/stripeMembershipType")
	if plan == "" {
		plan = "Pro"
	}
	plan = strings.Title(plan)

	reqURL := fmt.Sprintf("https://www.cursor.com/api/usage?user=%s", userId)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Invalid request URL")
	}
	req.Header.Set("Cookie", fmt.Sprintf("WorkosCursorSessionToken=%s%%3A%%3A%s", userId, token))
	req.Header.Set("User-Agent", "LimitBar-Windows/1.2.0")

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Could not fetch usage API")
	}
	defer resp.Body.Close()

	var data map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return StatusSnapshot(VendorCursor, StateFailed, "Invalid response from Cursor API")
	}

	var numRequests, maxRequests float64 = 0, 500
	if gpt4, ok := data["gpt-4"].(map[string]any); ok {
		if nr, ok := gpt4["numRequests"].(float64); ok {
			numRequests = nr
		} else if nrt, ok := gpt4["numRequestsTotal"].(float64); ok {
			numRequests = nrt
		}
		if mr, ok := gpt4["maxRequestUsage"].(float64); ok && mr > 0 {
			maxRequests = mr
		}
	}

	usedPct := (numRequests / maxRequests) * 100.0
	now := time.Now()
	var resetDate *time.Time
	if startStr, ok := data["startOfMonth"].(string); ok {
		if st := parseTime(startStr); st != nil {
			nextMonth := st.AddDate(0, 1, 0)
			resetDate = &nextMonth
		}
	}

	w := Window{
		Label:    "Fast requests",
		Short:    "m",
		Used:     Percent(usedPct),
		ResetsAt: resetDate,
	}

	return Snapshot{
		Vendor:     VendorCursor,
		State:      StateOK,
		Plan:       "Cursor " + plan,
		Bar:        &w,
		Windows:    []Window{w},
		Notes:      []string{fmt.Sprintf("%d / %d requests used", int(numRequests), int(maxRequests))},
		MeasuredAt: &now,
	}
}

// MARK: - Google Antigravity

func (f *Fetcher) Antigravity() Snapshot {
	dbPath := filepath.Join(f.AppData, "Antigravity", "User", "globalStorage", "state.vscdb")
	if !fileExists(dbPath) {
		return StatusSnapshot(VendorGemini, StateMissing, "Antigravity not installed")
	}

	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", uri)
	if err != nil {
		return StatusSnapshot(VendorGemini, StateFailed, "Could not open Antigravity database")
	}
	defer db.Close()

	var authStr string
	err = db.QueryRow("SELECT value FROM ItemTable WHERE key = 'antigravityAuthStatus' LIMIT 1").Scan(&authStr)
	if err != nil || authStr == "" {
		return StatusSnapshot(VendorGemini, StateSignedOut, "Sign in through Antigravity")
	}

	var authObj map[string]any
	if err := json.Unmarshal([]byte(authStr), &authObj); err != nil {
		return StatusSnapshot(VendorGemini, StateFailed, "Corrupted Antigravity session")
	}

	plan := "Google AI Pro"
	if b64, ok := authObj["userStatusProtoBinaryBase64"].(string); ok {
		if decoded, err := base64.StdEncoding.DecodeString(b64); err == nil {
			s := string(decoded)
			if strings.Contains(s, "Google AI Ultra") {
				plan = "Google AI Ultra"
			} else if strings.Contains(s, "Google AI Pro") || strings.Contains(s, "g1-pro-tier") {
				plan = "Google AI Pro"
			}
		}
	}

	now := time.Now()
	w := Window{
		Label: "AI Pro Plan",
		Short: "pro",
		Used:  Percent(0.0),
	}

	return Snapshot{
		Vendor:     VendorGemini,
		State:      StateOK,
		Plan:       plan,
		Bar:        &w,
		Windows:    []Window{w},
		Notes:      []string{"Gemini 3 Pro · Claude Sonnet 4.5 · Thinking models"},
		MeasuredAt: &now,
	}
}

// MARK: - Claude Desktop & Code

func (f *Fetcher) Claude() Snapshot {
	historyPath := filepath.Join(f.AppData, "Claude", "plan-usage-history.json")
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

	return StatusSnapshot(VendorClaude, StateMissing, "Claude Desktop or Code not detected")
}

// MARK: - OpenAI Codex & ChatGPT

func (f *Fetcher) Codex() Snapshot {
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
		f, err := os.Open(newestFile)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
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

	return StatusSnapshot(VendorCodex, StateMissing, "ChatGPT or Codex logs not found")
}

// MARK: - Grok Bot

func (f *Fetcher) Grok() Snapshot {
	dir := filepath.Join(f.AppData, "Grok Bot", "sand-client-persistence")
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".blob") {
				p := filepath.Join(dir, e.Name())
				data, err := os.ReadFile(p)
				if err != nil || !bytes.Contains(data, []byte("percentUsed")) {
					continue
				}
				var obj map[string]any
				if json.Unmarshal(data, &obj) == nil {
					if val, ok := obj["value"].(map[string]any); ok {
						if reading, ok := val["reading"].(map[string]any); ok {
							if usage, ok := reading["usage"].(map[string]any); ok {
								pct, _ := usage["percentUsed"].(float64)
								var resetDate *time.Time
								if nextMs, ok := usage["nextResetMs"].(float64); ok {
									t := time.Unix(int64(nextMs/1000), 0)
									resetDate = &t
								}
								plan := "Grok Bot Plan"
								if pl, ok := usage["grokPlanLabel"].(string); ok && pl != "" {
									plan = pl
								}
								var notes []string
								if onDemand, ok := usage["onDemand"].(map[string]any); ok {
									usedCents, _ := onDemand["usedCents"].(float64)
									limitCents, _ := onDemand["limitCents"].(float64)
									notes = append(notes, fmt.Sprintf("$%.2f / $%.2f on-demand used", usedCents/100, limitCents/100))
								}
								w := Window{
									Label:    "Weekly credits",
									Short:    "w",
									Used:     Percent(pct),
									ResetsAt: resetDate,
								}
								now := time.Now()
								return Snapshot{
									Vendor:     VendorGrok,
									State:      StateOK,
									Plan:       plan,
									Bar:        &w,
									Windows:    []Window{w},
									Notes:      notes,
									MeasuredAt: &now,
								}
							}
						}
					}
				}
			}
		}
	}

	return StatusSnapshot(VendorGrok, StateMissing, "Grok Bot not installed")
}

// MARK: - GitHub Copilot CLI

func (f *Fetcher) Copilot() Snapshot {
	copilotBin, err := exec.LookPath("copilot")
	if err != nil {
		copilotBin, err = exec.LookPath("copilot.cmd")
	}
	if err != nil {
		return StatusSnapshot(VendorCopilot, StateMissing, "GitHub Copilot CLI not installed")
	}

	cmd := exec.Command(copilotBin, "-p", "/usage", "--output-format", "json", "--allow-all-tools")
	out, err := cmd.Output()
	if err != nil {
		return StatusSnapshot(VendorCopilot, StateFailed, "Could not run copilot CLI")
	}

	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.Contains(line, "quotaSnapshots") || strings.Contains(line, "premium_interactions") {
			var o map[string]any
			if json.Unmarshal([]byte(line), &o) == nil {
				data := o
				if d, ok := o["data"].(map[string]any); ok {
					data = d
				}
				if qs, ok := data["quotaSnapshots"].(map[string]any); ok {
					if pi, ok := qs["premium_interactions"].(map[string]any); ok {
						entitlement, _ := pi["entitlementRequests"].(float64)
						usedRequests, _ := pi["usedRequests"].(float64)
						remPct, _ := pi["remainingPercentage"].(float64)
						usedPct := 100.0 - remPct
						if entitlement > 0 && usedRequests > 0 {
							usedPct = (usedRequests / entitlement) * 100.0
						}
						w := Window{
							Label:    "Premium requests",
							Short:    "m",
							Used:     Percent(usedPct),
							ResetsAt: parseTime(pi["resetDate"]),
						}
						now := time.Now()
						return Snapshot{
							Vendor:     VendorCopilot,
							State:      StateOK,
							Plan:       "Copilot",
							Bar:        &w,
							Windows:    []Window{w},
							Notes:      []string{fmt.Sprintf("%d / %d requests used", int(usedRequests), int(entitlement))},
							MeasuredAt: &now,
						}
					}
				}
			}
		}
	}

	return StatusSnapshot(VendorCopilot, StateFailed, "No quota data in copilot output")
}

// MARK: - DeepSeek API

func (f *Fetcher) DeepSeekKey() string {
	if k := os.Getenv("DEEPSEEK_API_KEY"); k != "" {
		return strings.TrimSpace(k)
	}
	candidates := []string{
		filepath.Join(f.HomeDir, ".deepseek", "api_key"),
		filepath.Join(f.HomeDir, ".config", "deepseek", "api_key"),
		filepath.Join(f.HomeDir, ".reasonix", ".env"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "DEEPSEEK_API_KEY=") {
					val := strings.TrimPrefix(line, "DEEPSEEK_API_KEY=")
					val = strings.Trim(val, "\"' ")
					if val != "" {
						return val
					}
				} else if strings.HasPrefix(line, "sk-") {
					return line
				}
			}
		}
	}
	return ""
}

func (f *Fetcher) DeepSeek() Snapshot {
	key := f.DeepSeekKey()
	if key == "" {
		return StatusSnapshot(VendorDeepSeek, StateMissing, "Configure DeepSeek API key")
	}

	req, _ := http.NewRequest("GET", "https://api.deepseek.com/user/balance", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return StatusSnapshot(VendorDeepSeek, StateFailed, "Could not fetch balance")
	}
	defer resp.Body.Close()

	var obj map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&obj); err != nil {
		return StatusSnapshot(VendorDeepSeek, StateFailed, "Invalid response")
	}

	if infos, ok := obj["balance_infos"].([]any); ok && len(infos) > 0 {
		first := infos[0].(map[string]any)
		totalStr, _ := first["total_balance"].(string)
		curr, _ := first["currency"].(string)
		if curr == "" {
			curr = "USD"
		}
		total, _ := strconv.ParseFloat(totalStr, 64)
		sym := "$"
		if curr == "CNY" {
			sym = "¥"
		}
		isLow := total <= 2.0
		barWin := Window{
			Label: "Balance",
			Short: sym,
		}
		note := fmt.Sprintf("%s%.2f %s balance", sym, total, curr)
		now := time.Now()
		return Snapshot{
			Vendor:          VendorDeepSeek,
			State:           StateOK,
			Plan:            "DeepSeek API",
			Bar:             &barWin,
			Windows:         []Window{},
			Notes:           []string{note},
			MeasuredAt:      &now,
			BalanceAmount:   &total,
			BalanceCurrency: curr,
			BalanceIsLow:    &isLow,
		}
	}

	return StatusSnapshot(VendorDeepSeek, StateFailed, "No balance info")
}

// MARK: - Kimi Code CLI

func (f *Fetcher) Kimi() Snapshot {
	configPath := filepath.Join(f.HomeDir, ".kimi-code", "config.toml")
	binPath := filepath.Join(f.HomeDir, ".kimi-code", "bin", "kimi.exe")
	if !fileExists(configPath) && !fileExists(binPath) {
		return StatusSnapshot(VendorKimi, StateMissing, "Kimi Code CLI not installed")
	}

	var apiKey, model string
	model = "KIMI-K3"

	if data, err := os.ReadFile(configPath); err == nil {
		reModel := regexp.MustCompile(`default_model\s*=\s*"([^"]+)"`)
		if m := reModel.FindStringSubmatch(string(data)); len(m) > 1 {
			parts := strings.Split(m[1], "/")
			model = strings.ToUpper(parts[len(parts)-1])
		}
		reKey := regexp.MustCompile(`api_key\s*=\s*"([^"]+)"`)
		if m := reKey.FindStringSubmatch(string(data)); len(m) > 1 {
			apiKey = m[1]
		}
	}

	now := time.Now()
	if apiKey != "" {
		req, _ := http.NewRequest("GET", "https://api.moonshot.ai/v1/users/me/balance", nil)
		req.Header.Set("Authorization", "Bearer "+apiKey)
		resp, err := f.HTTPClient.Do(req)
		if err == nil {
			defer resp.Body.Close()
			var obj map[string]any
			if json.NewDecoder(resp.Body).Decode(&obj) == nil {
				if d, ok := obj["data"].(map[string]any); ok {
					avail, _ := d["available_balance"].(float64)
					cash, _ := d["cash_balance"].(float64)
					voucher, _ := d["voucher_balance"].(float64)
					total := avail
					if total == 0 {
						total = cash + voucher
					}
					isLow := total <= 0
					barWin := Window{
						Label: "Balance",
						Short: "¥",
					}
					return Snapshot{
						Vendor:          VendorKimi,
						State:           StateOK,
						Plan:            fmt.Sprintf("Kimi Code (%s)", model),
						Bar:             &barWin,
						Windows:         []Window{},
						Notes:           []string{fmt.Sprintf("¥%.2f CNY · %s", total, model)},
						MeasuredAt:      &now,
						BalanceAmount:   &total,
						BalanceCurrency: "CNY",
						BalanceIsLow:    &isLow,
					}
				}
			}
		}
	}

	w := Window{Label: model, Short: "k", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorKimi,
		State:      StateOK,
		Plan:       fmt.Sprintf("Kimi Code (%s)", model),
		Bar:        &w,
		Windows:    []Window{w},
		Notes:      []string{"Active model: " + model},
		MeasuredAt: &now,
	}
}

// MARK: - Reasonix CLI

func (f *Fetcher) Reasonix() Snapshot {
	root := filepath.Join(f.HomeDir, ".reasonix")
	if !dirExists(root) {
		return StatusSnapshot(VendorReasonix, StateMissing, "Reasonix not configured")
	}

	statsDir := filepath.Join(root, "stats")
	entries, err := os.ReadDir(statsDir)
	var latestFile string
	if err == nil {
		var files []string
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".jsonl") {
				files = append(files, e.Name())
			}
		}
		sort.Strings(files)
		if len(files) > 0 {
			latestFile = filepath.Join(statsDir, files[len(files)-1])
		}
	}

	model := "deepseek-v4-pro"
	todayCost := 0.0
	now := time.Now()

	if latestFile != "" {
		f, err := os.Open(latestFile)
		if err == nil {
			defer f.Close()
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				var obj map[string]any
				if json.Unmarshal(scanner.Bytes(), &obj) == nil {
					if c, ok := obj["selected_cost"].(float64); ok {
						todayCost += c
					} else if cs, ok := obj["cost_amount"].(string); ok {
						if parsed, err := strconv.ParseFloat(cs, 64); err == nil {
							todayCost += parsed
						}
					}
					if m, ok := obj["model"].(string); ok && m != "" {
						parts := strings.Split(m, "/")
						model = parts[len(parts)-1]
					}
				}
			}
		}
	}

	note := fmt.Sprintf("Ready · %s", model)
	if todayCost > 0 {
		note = fmt.Sprintf("Session: $%.2f USD · %s", todayCost, model)
	}

	barWin := Window{Label: "Spend", Short: "$"}
	isLow := false
	return Snapshot{
		Vendor:          VendorReasonix,
		State:           StateOK,
		Plan:            fmt.Sprintf("Reasonix (%s)", model),
		Bar:             &barWin,
		Windows:         []Window{},
		Notes:           []string{note},
		MeasuredAt:      &now,
		BalanceAmount:   &todayCost,
		BalanceCurrency: "USD",
		BalanceIsLow:    &isLow,
	}
}

// MARK: - Muse Code

func (f *Fetcher) Muse() Snapshot {
	configPath := filepath.Join(f.HomeDir, ".config", "muse", "settings.json")
	binPath := filepath.Join(f.HomeDir, ".local", "bin", "muse.exe")
	if !fileExists(configPath) && !fileExists(binPath) {
		return StatusSnapshot(VendorMuse, StateMissing, "Muse Code not installed")
	}

	provider := "Meta"
	model := "muse-spark-1.3-contributor"
	authPath := filepath.Join(f.HomeDir, ".config", "muse", "auth.json")
	if data, err := os.ReadFile(authPath); err == nil {
		var obj map[string]any
		if json.Unmarshal(data, &obj) == nil {
			if provs, ok := obj["providers"].(map[string]any); ok {
				for k := range provs {
					provider = strings.Title(k)
					break
				}
			}
		}
	}
	if data, err := os.ReadFile(configPath); err == nil {
		var obj map[string]any
		if json.Unmarshal(data, &obj) == nil {
			if m, ok := obj["model"].(string); ok && m != "" {
				model = m
			}
		}
	}

	now := time.Now()
	w := Window{Label: "Unmetered", Short: "m"}
	return Snapshot{
		Vendor:     VendorMuse,
		State:      StateOK,
		Plan:       fmt.Sprintf("Muse Code (%s)", model),
		Bar:        &w,
		Windows:    []Window{},
		Notes:      []string{fmt.Sprintf("%s · Unmetered Preview", provider)},
		MeasuredAt: &now,
	}
}

// MARK: - Ollama

func (f *Fetcher) Ollama() Snapshot {
	resp, err := f.HTTPClient.Get("http://127.0.0.1:11434/api/ps")
	if err != nil {
		return StatusSnapshot(VendorOllama, StateMissing, "Ollama daemon not running")
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var obj map[string]any
	now := time.Now()

	if json.Unmarshal(body, &obj) == nil {
		if models, ok := obj["models"].([]any); ok && len(models) > 0 {
			first := models[0].(map[string]any)
			name, _ := first["name"].(string)
			vram, _ := first["size_vram"].(float64)
			vramGB := vram / (1024 * 1024 * 1024)
			w := Window{Label: name, Short: "loc", Used: Percent(100.0)}
			return Snapshot{
				Vendor:     VendorOllama,
				State:      StateOK,
				Plan:       "Ollama Local",
				Bar:        &w,
				Windows:    []Window{w},
				Notes:      []string{fmt.Sprintf("%.1f GB VRAM · Local model active", vramGB)},
				MeasuredAt: &now,
			}
		}
	}

	w := Window{Label: "Idle", Short: "loc", Used: Percent(0.0)}
	return Snapshot{
		Vendor:     VendorOllama,
		State:      StateOK,
		Plan:       "Ollama Ready",
		Bar:        &w,
		Windows:    []Window{w},
		Notes:      []string{"No model currently in VRAM"},
		MeasuredAt: &now,
	}
}
