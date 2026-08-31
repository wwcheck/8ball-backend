// Command smoketest exercises the minimal multiplayer loop end-to-end:
//
//	connect A  -> QUICK_MATCH  -> MATCH_QUEUED
//	connect B  -> QUICK_MATCH  -> ROOM_JOINED (both A and B)
//	A READY + B READY          -> GAME_START (broadcast)
//
// It can either spawn the server itself (default) or connect to a server that
// is already running (set SERVER_URL, e.g. ws://localhost:8080/ws).
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// client is a minimal WebSocket test client: it reads every frame, records the
// wire "type", and lets the test assert that an expected message arrived.
type client struct {
	conn *websocket.Conn

	mu    sync.Mutex
	types []string
	ch    chan string // signalled on every newly received type
}

func newClient(serverURL, playerID, name string) (*client, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("playerId", playerID)
	q.Set("name", name)
	u.RawQuery = q.Encode()

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	c := &client{conn: conn, ch: make(chan string, 256)}
	go c.readLoop()
	return c, nil
}

func (c *client) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		var env struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &env) != nil || env.Type == "" {
			continue
		}
		c.mu.Lock()
		c.types = append(c.types, env.Type)
		c.mu.Unlock()
		select {
		case c.ch <- env.Type:
		default:
		}
	}
}

// waitFor blocks until an envelope of the wanted type is observed, or the
// timeout elapses.
func (c *client) waitFor(want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		for _, t := range c.types {
			if t == want {
				c.mu.Unlock()
				return true
			}
		}
		c.mu.Unlock()

		select {
		case <-c.ch:
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return false
		}
	}
}

func (c *client) send(msg any) error {
	return c.conn.WriteJSON(msg)
}

func (c *client) close() { _ = c.conn.Close() }

// repoRoot returns the project root by walking up from this source file.
// main.go lives at <root>/cmd/smoketest/main.go, so three Dir calls land on root.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// spawnServer builds the server to a temp binary and runs it directly, so the
// returned stop function kills exactly one process. (Running `go run ./cmd/server`
// spawns a grandchild binary that would survive the parent kill and keep port
// 8080 busy for the next run.)
func spawnServer() (string, func(), error) {
	root := repoRoot()
	exe := filepath.Join(os.TempDir(), "8ball-server.exe")
	build := exec.Command("go", "build", "-o", exe, "./cmd/server")
	build.Dir = root
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return "", func() {}, fmt.Errorf("build server: %w", err)
	}
	cmd := exec.Command(exe)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", func() {}, err
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	return "ws://localhost:8080/ws", stop, nil
}

// waitServerReady retries dialing until the server accepts a connection.
func waitServerReady(serverURL string) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		// We only need a successful handshake; close immediately after.
		c, err := newClient(serverURL, "_probe", "probe")
		if err == nil {
			c.close()
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("server did not become reachable at %s", serverURL)
}

func main() {
	log.SetFlags(0)

	serverURL := os.Getenv("SERVER_URL")
	stop := func() {}
	if serverURL == "" {
		log.Println("[smoke] no SERVER_URL set; spawning server via `go run ./cmd/server`")
		wsURL, s, err := spawnServer()
		if err != nil {
			log.Fatalf("[smoke] failed to start server: %v", err)
		}
		serverURL = wsURL
		stop = s
		defer stop()
	}

	if err := waitServerReady(serverURL); err != nil {
		log.Fatalf("[smoke] %v", err)
	}
	log.Printf("[smoke] server reachable at %s", serverURL)

	// --- connect player A and enter the matchmaking queue -------------------
	a, err := newClient(serverURL, "SmokeA", "Alice")
	if err != nil {
		log.Fatalf("[smoke] A connect: %v", err)
	}
	defer a.close()
	if err := a.send(map[string]any{"type": "QUICK_MATCH"}); err != nil {
		log.Fatalf("[smoke] A QUICK_MATCH: %v", err)
	}
	if !a.waitFor("MATCH_QUEUED", 10*time.Second) {
		log.Fatalf("[smoke] A never received MATCH_QUEUED")
	}
	log.Println("[smoke] A received MATCH_QUEUED")

	// --- connect player B; the pair forms immediately -----------------------
	b, err := newClient(serverURL, "SmokeB", "Bob")
	if err != nil {
		log.Fatalf("[smoke] B connect: %v", err)
	}
	defer b.close()
	if err := b.send(map[string]any{"type": "QUICK_MATCH"}); err != nil {
		log.Fatalf("[smoke] B QUICK_MATCH: %v", err)
	}
	if !b.waitFor("ROOM_JOINED", 10*time.Second) {
		log.Fatalf("[smoke] B never received ROOM_JOINED")
	}
	log.Println("[smoke] B received ROOM_JOINED")
	if !a.waitFor("ROOM_JOINED", 10*time.Second) {
		log.Fatalf("[smoke] A never received ROOM_JOINED (matchmaking pair notify)")
	}
	log.Println("[smoke] A received ROOM_JOINED")

	// --- both players ready -> game should start ---------------------------
	if err := a.send(map[string]any{"type": "READY", "ready": true}); err != nil {
		log.Fatalf("[smoke] A READY: %v", err)
	}
	if err := b.send(map[string]any{"type": "READY", "ready": true}); err != nil {
		log.Fatalf("[smoke] B READY: %v", err)
	}
	if !a.waitFor("GAME_START", 10*time.Second) {
		log.Fatalf("[smoke] A never received GAME_START")
	}
	if !b.waitFor("GAME_START", 10*time.Second) {
		log.Fatalf("[smoke] B never received GAME_START")
	}
	log.Println("[smoke] both players received GAME_START")

	fmt.Println("\nSMOKE TEST PASSED: connect -> quick match -> both ready -> GAME_START")
}
