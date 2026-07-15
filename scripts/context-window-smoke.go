//go:build cwsmoke

// context-window-smoke.go — WebSocket client for context_window_snapshot smoke.
//
// Run via: go run -tags cwsmoke scripts/context-window-smoke.go <wsURL> <output.json>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type Event struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: go run -tags cwsmoke scripts/context-window-smoke.go <wsURL> <output.json>")
		os.Exit(2)
	}
	wsURL := os.Args[1]
	outFile := os.Args[2]

	// Gorilla requires ws:// or wss:// scheme
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	if !strings.HasPrefix(wsURL, "ws://") && !strings.HasPrefix(wsURL, "wss://") {
		wsURL = "ws://" + wsURL
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ws dial failed: %v\n", err)
		os.Exit(9)
	}
	defer conn.Close()

	var events []Event
	deadline := time.Now().Add(20 * time.Second)
	conn.SetReadDeadline(deadline)

	for time.Now().Before(deadline) {
		var ev Event
		if err := conn.ReadJSON(&ev); err != nil {
			break
		}
		if ev.Type == "context_window_snapshot" {
			events = append(events, ev)
		}
	}

	f, err := os.Create(outFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create output: %v\n", err)
		os.Exit(9)
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(events)
	fmt.Printf("Collected %d context_window_snapshot events\n", len(events))
}
