package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ANSI color escape sequences
const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiRed     = "\033[31m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiBlue    = "\033[34m"
	ansiMagenta = "\033[35m"
	ansiCyan    = "\033[36m"
	ansiGray    = "\033[90m"
)

func progressBar(pct float64, width int) string {
	if width <= 0 {
		width = 12
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}

	filled := int(math.Round(float64(width) * (pct / 100.0)))
	if filled > width {
		filled = width
	}
	empty := width - filled

	color := ansiGreen
	if pct >= 90 {
		color = ansiRed
	} else if pct >= 70 {
		color = ansiYellow
	}

	bar := color + strings.Repeat("█", filled) + ansiGray + strings.Repeat("░", empty) + ansiReset
	return fmt.Sprintf("[%s] %s%3.0f%%%s", bar, color, pct, ansiReset)
}

func formatResetCountdown(t *time.Time) string {
	if t == nil {
		return ""
	}
	dur := time.Until(*t)
	if dur <= 0 {
		return "resetting"
	}
	hrs := int(dur.Hours())
	mins := int(dur.Minutes()) % 60
	if hrs > 24 {
		days := hrs / 24
		return fmt.Sprintf("in %dd %dh", days, hrs%24)
	}
	if hrs > 0 {
		return fmt.Sprintf("in %dh %dm", hrs, mins)
	}
	return fmt.Sprintf("in %dm", mins)
}

func RenderBarLine(store *Store) string {
	installed := store.Installed()
	if len(installed) == 0 {
		return "LimitBar: no active providers"
	}

	var parts []string
	for _, v := range installed {
		snap, ok := store.Get(v)
		if !ok || snap.State == StateMissing {
			continue
		}

		if snap.State != StateOK {
			parts = append(parts, fmt.Sprintf("%s: %s", v.Name(), snap.State))
			continue
		}

		if snap.IsBalanceQuota() && snap.BalanceAmount != nil {
			sym := "$"
			if snap.BalanceCurrency == "CNY" {
				sym = "¥"
			}
			parts = append(parts, fmt.Sprintf("%s: %s%.2f", v.Name(), sym, *snap.BalanceAmount))
		} else if snap.Bar != nil && snap.Bar.Used != nil {
			short := snap.Bar.Short
			if short != "" {
				short = " (" + short + ")"
			}
			parts = append(parts, fmt.Sprintf("%s: %.0f%%%s", v.Name(), *snap.Bar.Used, short))
		} else {
			parts = append(parts, fmt.Sprintf("%s: OK", v.Name()))
		}
	}

	if len(parts) == 0 {
		return "LimitBar: idle"
	}
	return strings.Join(parts, " · ")
}

type WaybarOutput struct {
	Text    string `json:"text"`
	Tooltip string `json:"tooltip"`
	Class   string `json:"class"`
}

func RenderWaybarJSON(store *Store) string {
	text := RenderBarLine(store)
	var tooltipLines []string
	tooltipLines = append(tooltipLines, "<b>LimitBar AI Quota Monitor (FreeBSD)</b>\n")

	hasAlert := false
	for _, v := range store.Installed() {
		snap, ok := store.Get(v)
		if !ok {
			continue
		}
		if snap.OtherHot() || (snap.Bar != nil && snap.Bar.Used != nil && *snap.Bar.Used >= 90) {
			hasAlert = true
		}
		status := string(snap.State)
		if snap.Plan != "" {
			status += " (" + snap.Plan + ")"
		}
		tooltipLines = append(tooltipLines, fmt.Sprintf("<b>%s</b>: %s", v.Name(), status))
		for _, w := range snap.Windows {
			if w.Used != nil {
				resetStr := ""
				if w.ResetsAt != nil {
					resetStr = " · reset " + formatResetCountdown(w.ResetsAt)
				}
				tooltipLines = append(tooltipLines, fmt.Sprintf("  • %s: %.1f%%%s", w.Label, *w.Used, resetStr))
			}
		}
	}

	class := "normal"
	if hasAlert {
		class = "critical"
	}

	out := WaybarOutput{
		Text:    text,
		Tooltip: strings.Join(tooltipLines, "\n"),
		Class:   class,
	}

	data, _ := json.Marshal(out)
	return string(data)
}

func formatCard(v Vendor, s Snapshot, width int) []string {
	var lines []string

	// Header: Name + Plan
	planStr := s.Plan
	if planStr == "" {
		planStr = v.Product()
	}
	header := fmt.Sprintf("%s%s%s %s(%s)%s", ansiBold, v.Name(), ansiReset, ansiDim, planStr, ansiReset)
	lines = append(lines, header)

	if s.State != StateOK {
		stateColor := ansiYellow
		if s.State == StateUntrusted || s.State == StateFailed {
			stateColor = ansiRed
		}
		lines = append(lines, fmt.Sprintf("  %s● %s%s", stateColor, s.State, ansiReset))
		if s.Error != "" {
			lines = append(lines, fmt.Sprintf("  %s%s%s", ansiDim, s.Error, ansiReset))
		}
		return lines
	}

	// Balance / Currency Quotas
	if s.IsBalanceQuota() && s.BalanceAmount != nil {
		curr := s.BalanceCurrency
		sym := "$"
		if curr == "CNY" {
			sym = "¥"
		}
		color := ansiGreen
		if s.BalanceIsLow != nil && *s.BalanceIsLow {
			color = ansiRed
		}
		lines = append(lines, fmt.Sprintf("  Balance: %s%s%.2f %s%s", color, sym, *s.BalanceAmount, curr, ansiReset))
		for _, note := range s.Notes {
			lines = append(lines, fmt.Sprintf("  %s%s%s", ansiDim, note, ansiReset))
		}
		return lines
	}

	// Rate Limits / Windows (e.g. 5-hour and weekly)
	if len(s.Windows) > 0 {
		for _, w := range s.Windows {
			if w.Used != nil {
				bar := progressBar(*w.Used, 10)
				resetStr := ""
				if w.ResetsAt != nil {
					resetStr = fmt.Sprintf(" %s(%s)%s", ansiDim, formatResetCountdown(w.ResetsAt), ansiReset)
				}
				lines = append(lines, fmt.Sprintf("  %-14s %s%s", w.Label+":", bar, resetStr))
			}
		}
	} else {
		lines = append(lines, fmt.Sprintf("  %sActive & Connected%s", ansiGreen, ansiReset))
	}

	for _, n := range s.Notes {
		lines = append(lines, fmt.Sprintf("  %s%s%s", ansiDim, n, ansiReset))
	}

	return lines
}

func RenderDashboard(store *Store) {
	fmt.Printf("%s%s┌────────────────────────────────────────────────────────┐%s\n", ansiCyan, ansiBold, ansiReset)
	fmt.Printf("%s%s│  LimitBar 1.1.0 · FreeBSD AI Quota & Rate Limit Bar   │%s\n", ansiCyan, ansiBold, ansiReset)
	fmt.Printf("%s%s└────────────────────────────────────────────────────────┘%s\n\n", ansiCyan, ansiBold, ansiReset)

	installed := store.Installed()
	if len(installed) == 0 {
		fmt.Printf("  %sNo AI coding agents or tools currently detected.%s\n\n", ansiYellow, ansiReset)
		fmt.Println("  Supported: Claude Code & Desktop, Google Antigravity & Gemini,")
		fmt.Println("             OpenAI Codex, Cursor, Grok, Copilot, DeepSeek, Kimi, Reasonix, Muse, Ollama.")
		return
	}

	// Render in 2-column layout
	colWidth := 38
	for i := 0; i < len(installed); i += 2 {
		vLeft := installed[i]
		sLeft, _ := store.Get(vLeft)
		linesLeft := formatCard(vLeft, sLeft, colWidth)

		var linesRight []string
		if i+1 < len(installed) {
			vRight := installed[i+1]
			sRight, _ := store.Get(vRight)
			linesRight = formatCard(vRight, sRight, colWidth)
		}

		maxLines := len(linesLeft)
		if len(linesRight) > maxLines {
			maxLines = len(linesRight)
		}

		for lineIdx := 0; lineIdx < maxLines; lineIdx++ {
			left := ""
			if lineIdx < len(linesLeft) {
				left = linesLeft[lineIdx]
			}
			right := ""
			if lineIdx < len(linesRight) {
				right = linesRight[lineIdx]
			}

			// Strip ANSI for width calculation
			cleanLeft := stripANSI(left)
			pad := colWidth - len(cleanLeft)
			if pad < 2 {
				pad = 2
			}
			fmt.Printf("%s%s%s\n", left, strings.Repeat(" ", pad), right)
		}
		fmt.Println()
	}

	fmt.Printf("%sUpdated at %s%s\n", ansiDim, time.Now().Format("15:04:05"), ansiReset)
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		if s[i] == '\033' {
			inEsc = true
		} else if inEsc {
			if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
				inEsc = false
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func RunDaemon(store *Store, interval time.Duration) {
	cacheDir := filepath.Join(store.fetcher.CacheDir, "limitbar")
	_ = os.MkdirAll(cacheDir, 0700)
	barFile := filepath.Join(cacheDir, "bar.txt")
	stateFile := filepath.Join(cacheDir, "state.json")

	fmt.Printf("LimitBar FreeBSD daemon started (refresh interval: %v)\n", interval)
	fmt.Printf("Writing live status bar to: %s\n", barFile)

	alertedVendors := make(map[Vendor]bool)

	for {
		store.RefreshAll()

		// Write bar string
		barText := RenderBarLine(store)
		_ = os.WriteFile(barFile, []byte(barText+"\n"), 0644)

		// Write probe JSON
		all := store.All()
		data, err := json.MarshalIndent(all, "", "  ")
		if err == nil {
			_ = os.WriteFile(stateFile, data, 0644)
		}

		// Check alerts
		for v, s := range all {
			if s.State == StateOK && s.Bar != nil && s.Bar.Used != nil && *s.Bar.Used >= 90 {
				if !alertedVendors[v] {
					alertedVendors[v] = true
					msg := fmt.Sprintf("%s quota at %.0f%%!", v.Name(), *s.Bar.Used)
					_ = exec.Command("notify-send", "-u", "critical", "LimitBar Alert", msg).Run()
				}
			} else {
				alertedVendors[v] = false
			}
		}

		time.Sleep(interval)
	}
}
