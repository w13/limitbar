package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const Version = "1.2.0"

func main() {
	probeFlag := flag.Bool("probe", false, "Ask every tool once and print full JSON snapshot")
	versionFlag := flag.Bool("version", false, "Print version")
	barFlag := flag.Bool("bar", false, "Print one-line status string (for polybar, tmux, i3blocks)")
	polybarFlag := flag.Bool("polybar", false, "Alias for -bar")
	tmuxFlag := flag.Bool("tmux", false, "Alias for -bar")
	waybarFlag := flag.Bool("waybar", false, "Print Waybar-compatible JSON with tooltip")
	watchFlag := flag.Bool("watch", false, "Watch live dashboard in terminal with auto-refresh")
	daemonFlag := flag.Bool("daemon", false, "Run in background and maintain ~/.cache/limitbar/bar.txt")
	intervalSec := flag.Int("interval", 30, "Refresh interval in seconds for -watch or -daemon")

	claudePath := flag.String("claudePath", "", "Override path to claude executable")
	codexPath := flag.String("codexPath", "", "Override path to codex executable")
	agyPath := flag.String("agyPath", "", "Override path to agy executable")
	grokPath := flag.String("grokPath", "", "Override path to grok executable")

	flag.Parse()

	if *versionFlag {
		fmt.Printf("LimitBar FreeBSD v%s\n", Version)
		return
	}

	overrides := CLIOverrides{
		ClaudePath: *claudePath,
		CodexPath:  *codexPath,
		AgyPath:    *agyPath,
		GrokPath:   *grokPath,
	}

	store := NewStore(overrides)

	if *probeFlag {
		store.RefreshAll()
		all := store.All()
		data, err := json.MarshalIndent(all, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error encoding probe JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
		return
	}

	if *barFlag || *polybarFlag || *tmuxFlag {
		store.RefreshAll()
		fmt.Println(RenderBarLine(store))
		return
	}

	if *waybarFlag {
		store.RefreshAll()
		fmt.Println(RenderWaybarJSON(store))
		return
	}

	dur := time.Duration(*intervalSec) * time.Second
	if dur < 2*time.Second {
		dur = 2 * time.Second
	}

	if *daemonFlag {
		RunDaemon(store, dur)
		return
	}

	if *watchFlag {
		// Clear terminal and watch loop
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)

		ticker := time.NewTicker(dur)
		defer ticker.Stop()

		for {
			store.RefreshAll()
			fmt.Print("\033[H\033[2J") // clear screen
			RenderDashboard(store)
			fmt.Printf("\nAuto-refreshing every %v (Press Ctrl+C to quit)\n", dur)

			select {
			case <-ticker.C:
			case <-sig:
				fmt.Println("\nExiting LimitBar.")
				return
			}
		}
	}

	// Default mode: single-shot dashboard render
	store.RefreshAll()
	RenderDashboard(store)
}
