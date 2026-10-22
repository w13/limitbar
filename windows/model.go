package main

import (
	"fmt"
	"math"
	"time"
)

type Vendor string

const (
	VendorClaude    Vendor = "claude"
	VendorGemini    Vendor = "gemini"
	VendorCodex     Vendor = "codex"
	VendorGrok      Vendor = "grok"
	VendorCursor    Vendor = "cursor"
	VendorCopilot   Vendor = "copilot"
	VendorDeepSeek  Vendor = "deepseek"
	VendorKimi      Vendor = "kimi"
	VendorReasonix  Vendor = "reasonix"
	VendorMuse      Vendor = "muse"
	VendorOllama    Vendor = "ollama"
)

var AllVendors = []Vendor{
	VendorClaude, VendorGemini, VendorCodex, VendorGrok, VendorCursor,
	VendorCopilot, VendorDeepSeek, VendorKimi, VendorReasonix, VendorMuse, VendorOllama,
}

func (v Vendor) Name() string {
	switch v {
	case VendorClaude:
		return "Claude"
	case VendorGemini:
		return "Antigravity"
	case VendorCodex:
		return "Codex"
	case VendorGrok:
		return "Grok"
	case VendorCursor:
		return "Cursor"
	case VendorCopilot:
		return "Copilot"
	case VendorDeepSeek:
		return "DeepSeek"
	case VendorKimi:
		return "Kimi"
	case VendorReasonix:
		return "Reasonix"
	case VendorMuse:
		return "Muse"
	case VendorOllama:
		return "Ollama"
	default:
		return string(v)
	}
}

func (v Vendor) Product() string {
	switch v {
	case VendorClaude:
		return "Claude Code & Desktop"
	case VendorGemini:
		return "Google Antigravity & Gemini"
	case VendorCodex:
		return "Codex CLI & ChatGPT"
	case VendorGrok:
		return "Grok Bot & xAI"
	case VendorCursor:
		return "Cursor IDE"
	case VendorCopilot:
		return "GitHub Copilot CLI"
	case VendorDeepSeek:
		return "DeepSeek API"
	case VendorKimi:
		return "Kimi Code CLI"
	case VendorReasonix:
		return "Reasonix CLI"
	case VendorMuse:
		return "Muse Code"
	case VendorOllama:
		return "Ollama Local AI"
	default:
		return string(v)
	}
}

type VendorState string

const (
	StateOK        VendorState = "ok"
	StateMissing   VendorState = "missing"
	StateUntrusted VendorState = "untrusted"
	StateSignedOut VendorState = "signedOut"
	StateFailed    VendorState = "failed"
)

type Window struct {
	Label    string     `json:"label"`
	Short    string     `json:"short"`
	Used     *float64   `json:"used,omitempty"`
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
}

func (w Window) ResetPassed() bool {
	if w.ResetsAt == nil {
		return false
	}
	return w.ResetsAt.Before(time.Now())
}

type Snapshot struct {
	Vendor     Vendor      `json:"vendor"`
	State      VendorState `json:"state"`
	Plan       string      `json:"plan,omitempty"`
	Bar        *Window     `json:"bar,omitempty"`
	Windows    []Window    `json:"windows"`
	Notes      []string    `json:"notes"`
	MeasuredAt *time.Time  `json:"measuredAt,omitempty"`
	Error      string      `json:"error,omitempty"`
}

func (s Snapshot) OtherHot() bool {
	for _, w := range s.Windows {
		if s.Bar != nil && w.Label == s.Bar.Label {
			continue
		}
		if !w.ResetPassed() && w.Used != nil && *w.Used >= 90.0 {
			return true
		}
	}
	return false
}

func StatusSnapshot(v Vendor, st VendorState, errStr string) Snapshot {
	return Snapshot{
		Vendor:  v,
		State:   st,
		Windows: []Window{},
		Notes:   []string{},
		Error:   errStr,
	}
}

func Percent(val any) *float64 {
	var num float64
	switch v := val.(type) {
	case float64:
		num = v
	case float32:
		num = float64(v)
	case int:
		num = float64(v)
	case int64:
		num = float64(v)
	default:
		return nil
	}
	if math.IsNaN(num) || math.IsInf(num, 0) || num < 0 || num > 100 {
		return nil
	}
	return &num
}

func PercentText(v *float64) string {
	if v == nil {
		return "—"
	}
	val := *v
	r := math.Round(val)
	if r >= 100 && val < 100 {
		return "99"
	}
	return fmt.Sprintf("%d", int(r))
}
