package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

const Version = "1.2.0"

func main() {
	probeFlag := flag.Bool("probe", false, "Ask every tool once and print full JSON snapshot")
	versionFlag := flag.Bool("version", false, "Print version")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("LimitBar Windows v%s\n", Version)
		return
	}

	store := NewStore()

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

	// Start background polling loop every 30 seconds
	store.StartBackgroundLoop(30 * time.Second)

	// Run native Windows Taskbar System Tray application
	tray := NewTrayApp(store)
	if err := tray.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Tray error: %v\n", err)
		os.Exit(1)
	}
}
