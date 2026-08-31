// Command smoke is a manual QA client for the minimal multiplayer loop. It
// spins up two virtual players against a running server and walks the whole
// happy path:
//
//	QUICK_MATCH x2 -> READY x2 -> GAME_START -> CUE_BALL_PLACEMENT -> SHOOT ->
//	SHOT_RESULT (BALLS_STOPPED + TURN_CHANGE) -> drop p1 -> PLAYER_DISCONNECTED
//	-> RECONNECT p1 -> SNAPSHOT(resumed)
//
// Usage: go run ./cmd/smoke [-addr localhost:8080]
// The server must already be running (go run ./cmd/server).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

type frame struct {
	env protocol.Envelope
	raw []byte
}

// player is one virtual client: a socket plus a reader goroutine.
type player struct {
	name  string
	id    string
	token string
	conn  *websocket.Conn
	in    chan frame
}

func connect(addr, id, name, token string) *player {
	url := fmt.Sprintf("ws://%s/ws?playerId=%s&name=%s", addr, id, name)
	if token != "" {
		url += "&token=" + token
	}
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		log.Fatalf("[%s] dial: %v", id, err)
	}
	p := &player{name: name, id: id, conn: conn, in: make(chan frame, 256)}
	go p.read()
	return p
}

func (p *player) read() {
	defer close(p.in)
	for {
		_, raw, err := p.conn.ReadMessage()
		if err != nil {
			return
		}
		env, err := protocol.ParseEnvelope(raw)
		if err != nil {
			continue
		}
		if env.PlayerID == "" {
			env.PlayerID = p.id // S->C frames may omit the actor
		}
		p.in <- frame{env: env, raw: raw}
	}
}

func (p *player) send(msg protocol.Message) {
	if h := msg.Head(); h.PlayerID == "" {
		h.PlayerID = p.id
	}
	if err := p.conn.WriteJSON(msg); err != nil {
		log.Fatalf("[%s] send %s: %v", p.id, msg.Head().Type, err)
	}
}

// waitFor blocks until one of the wanted message types arrives (others are
// logged and skipped). Fails the run on timeout.
func (p *player) waitFor(timeout time.Duration, want ...string) frame {
	deadline := time.After(timeout)
	for {
		select {
		case f, ok := <-p.in:
			if !ok {
				log.Fatalf("[%s] connection closed while waiting for %v", p.id, want)
			}
			for _, w := range want {
				if f.env.Type == w {
					fmt.Printf("  [%s] <- %s\n", p.id, f.env.Type)
					return f
				}
			}
			fmt.Printf("  [%s] -- (ignored) %s\n", p.id, f.env.Type)
		case <-deadline:
			log.Fatalf("[%s] timed out waiting for %v", p.id, want)
		}
	}
}

// waitForAll blocks until every wanted message type has been seen once
// (order-insensitive; unrelated frames are logged and skipped).
func (p *player) waitForAll(timeout time.Duration, want ...string) map[string]frame {
	got := make(map[string]frame, len(want))
	deadline := time.After(timeout)
	for len(got) < len(want) {
		select {
		case f, ok := <-p.in:
			if !ok {
				log.Fatalf("[%s] connection closed while waiting for %v", p.id, want)
			}
			matched := false
			for _, w := range want {
				if f.env.Type == w {
					got[w] = f
					matched = true
				}
			}
			if matched {
				fmt.Printf("  [%s] <- %s\n", p.id, f.env.Type)
			} else {
				fmt.Printf("  [%s] -- (ignored) %s\n", p.id, f.env.Type)
			}
		case <-deadline:
			seen := make([]string, 0, len(got))
			for k := range got {
				seen = append(seen, k)
			}
			log.Fatalf("[%s] timed out waiting for %v (got %v)", p.id, want, seen)
		}
	}
	return got
}

var step = 0

func next(label string) {
	step++
	fmt.Printf("\n== step %d: %s ==\n", step, label)
}

func main() {
	addr := flag.String("addr", "localhost:8080", "server host:port")
	flag.Parse()
	const timeout = 10 * time.Second

	// --- 1. two players connect + quick match ----------------------------
	next("connect + QUICK_MATCH")
	p1 := connect(*addr, "smoke-p1", "Alice", "")
	p1.waitFor(timeout, protocol.TypeWelcome)
	p2 := connect(*addr, "smoke-p2", "Bob", "")
	p2.waitFor(timeout, protocol.TypeWelcome)

	p1.send(&protocol.QuickMatchReq{Envelope: protocol.Envelope{Type: protocol.TypeQuickMatch}})
	p1.waitFor(timeout, protocol.TypeMatchQueued)
	p2.send(&protocol.QuickMatchReq{Envelope: protocol.Envelope{Type: protocol.TypeQuickMatch}})
	// The waiter sees ROOM_JOINED first, the caller sees MATCH_FOUND first -
	// accept either order.
	p1.waitForAll(timeout, protocol.TypeMatchFound, protocol.TypeRoomJoined)
	p2.waitForAll(timeout, protocol.TypeMatchFound, protocol.TypeRoomJoined)

	// remember p1's session token for the reconnect later
	var welcome protocol.WelcomeResp
	// (the welcome was consumed above; re-request via HELLO echo)
	p1.send(&protocol.HelloReq{Envelope: protocol.Envelope{Type: protocol.TypeHello}, Name: "Alice"})
	wf := p1.waitFor(timeout, protocol.TypeWelcome)
	if err := json.Unmarshal(wf.raw, &welcome); err != nil {
		log.Fatalf("decode WELCOME: %v", err)
	}
	p1.token = welcome.SessionToken
	fmt.Printf("  p1 sessionToken=%s resumableRoom=%s\n", p1.token, welcome.ResumableRoomID)

	// --- 2. both ready -> game starts ------------------------------------
	next("READY x2 -> GAME_START")
	p1.send(&protocol.ReadyReq{Envelope: protocol.Envelope{Type: protocol.TypeReady}, Ready: true})
	p2.send(&protocol.ReadyReq{Envelope: protocol.Envelope{Type: protocol.TypeReady}, Ready: true})
	gs := p1.waitFor(timeout, protocol.TypeGameStart)
	p2.waitFor(timeout, protocol.TypeGameStart)

	var start protocol.GameStartResp
	if err := json.Unmarshal(gs.raw, &start); err != nil {
		log.Fatalf("decode GAME_START: %v", err)
	}
	breaker := start.BreakerID
	fmt.Printf("  breaker=%s phase=%s ballInHand=%v\n",
		start.GameState.CurrentPlayerID, start.GameState.GamePhase, start.GameState.BallInHand)
	if start.GameState.GamePhase != protocol.PhaseBallInHand {
		log.Fatalf("expected phase %s, got %s", protocol.PhaseBallInHand, start.GameState.GamePhase)
	}
	brk, opp := p1, p2
	if breaker == p2.id {
		brk, opp = p2, p1
	}

	// --- 3. place the cue ball -------------------------------------------
	next("CUE_BALL_PLACEMENT")
	brk.send(&protocol.CueBallPlacementReq{
		Envelope: protocol.Envelope{Type: protocol.TypeCueBallPlacement},
		Position: protocol.Vector3{X: protocol.HeadStringX, Y: 0, Z: 0},
	})
	brk.waitFor(timeout, protocol.TypeCueBallPlacementAck)
	opp.waitFor(timeout, protocol.TypeCueBallPlacementAck)

	// --- 4. shoot ----------------------------------------------------------
	next("SHOOT (accepted) + SHOT_BROADCAST")
	brk.send(&protocol.ShootReq{
		Envelope: protocol.Envelope{Type: protocol.TypeShoot},
		CueAngle: 0, Power: 0.5,
	})
	ackF := brk.waitFor(timeout, protocol.TypeShootAck)
	var ack protocol.ShootAckResp
	if err := json.Unmarshal(ackF.raw, &ack); err != nil {
		log.Fatalf("decode SHOOT_ACK: %v", err)
	}
	if ack.Status != "accepted" {
		log.Fatalf("SHOOT_ACK status=%s code=%s msg=%s", ack.Status, ack.ErrorCode, ack.Message)
	}
	bcF := brk.waitFor(timeout, protocol.TypeShotBroadcast)
	opp.waitFor(timeout, protocol.TypeShotBroadcast)
	var bc protocol.ShotBroadcastResp
	if err := json.Unmarshal(bcF.raw, &bc); err != nil {
		log.Fatalf("decode SHOT_BROADCAST: %v", err)
	}

	// --- 5. illegal SHOT from the opponent must be rejected ----------------
	next("out-of-turn SHOOT rejected (anti-cheat)")
	opp.send(&protocol.ShootReq{
		Envelope: protocol.Envelope{Type: protocol.TypeShoot},
		CueAngle: 0, Power: 0.9,
	})
	rej := opp.waitFor(timeout, protocol.TypeShootAck)
	var rejAck protocol.ShootAckResp
	if err := json.Unmarshal(rej.raw, &rejAck); err != nil {
		log.Fatalf("decode SHOOT_ACK: %v", err)
	}
	if rejAck.Status != "rejected" {
		log.Fatalf("out-of-turn shoot should be rejected, got %s", rejAck.Status)
	}
	fmt.Printf("  rejected with %s (%s)\n", rejAck.ErrorCode, rejAck.Message)

	// --- 6. report the settled result --------------------------------------
	next("SHOT_RESULT -> BALLS_STOPPED + TURN_CHANGE")
	stopped := make([]protocol.BallState, len(bc.BallStates))
	copy(stopped, bc.BallStates)
	for i := range stopped {
		stopped[i].Velocity = protocol.Vector3{}
		stopped[i].AngularVelocity = protocol.Vector3{}
		stopped[i].IsMoving = false
	}
	// "Move" the cue ball along +X: the server now rejects reports that claim
	// cueBallMoved without an actual displacement (anti-cheat).
	stopped[protocol.CueBallID].Position.X += 0.1
	brk.send(&protocol.ShotResultReq{
		Envelope:         protocol.Envelope{Type: protocol.TypeShotResult},
		ShotNumber:       bc.ShotNumber,
		FirstContactBall: 1,
		PocketedBalls:    []int{},
		CueBallMoved:     true,
		BallStates:       stopped,
	})
	bsF := brk.waitFor(timeout, protocol.TypeBallsStopped)
	opp.waitFor(timeout, protocol.TypeBallsStopped)
	var bs protocol.BallsStoppedResp
	if err := json.Unmarshal(bsF.raw, &bs); err != nil {
		log.Fatalf("decode BALLS_STOPPED: %v", err)
	}
	fmt.Printf("  foul=%v continuing=%v nextPlayer=%s\n",
		bs.StrikeResult.FoulType, bs.StrikeResult.IsContinuing, bs.StrikeResult.NextPlayerID)
	turnF := brk.waitFor(timeout, protocol.TypeTurnChange)
	var turn protocol.TurnChangeResp
	if err := json.Unmarshal(turnF.raw, &turn); err != nil {
		log.Fatalf("decode TURN_CHANGE: %v", err)
	}
	if turn.CurrentPlayerID != opp.id {
		log.Fatalf("expected turn to pass to %s, got %s", opp.id, turn.CurrentPlayerID)
	}

	// --- 7. drop the breaker, opponent is notified ---------------------------
	next("disconnect -> PLAYER_DISCONNECTED")
	if err := brk.conn.Close(); err != nil {
		log.Fatalf("close: %v", err)
	}
	opp.waitFor(timeout, protocol.TypePlayerDisconnected)

	// --- 8. reconnect with the session token --------------------------------
	next("RECONNECT -> SNAPSHOT(resumed)")
	r := connect(*addr, brk.id, brk.name, brk.token)
	r.waitFor(timeout, protocol.TypeWelcome)
	r.send(&protocol.ReconnectReq{
		Envelope:     protocol.Envelope{Type: protocol.TypeReconnect},
		SessionToken: brk.token,
	})
	sn := r.waitFor(timeout, protocol.TypeSnapshot)
	var snap protocol.SnapshotResp
	if err := json.Unmarshal(sn.raw, &snap); err != nil {
		log.Fatalf("decode SNAPSHOT: %v", err)
	}
	if !snap.Resumed {
		log.Fatalf("expected resumed=true snapshot")
	}
	fmt.Printf("  resumed room=%s phase=%s shot=%d balls=%d\n",
		snap.GameState.RoomID, snap.GameState.GamePhase, snap.GameState.ShotNumber, len(snap.GameState.BallStates))

	// --- done ----------------------------------------------------------------
	next("cleanup (CONCEDE + LEAVE_ROOM)")
	r.send(&protocol.ConcedeReq{Envelope: protocol.Envelope{Type: protocol.TypeConcede}})
	opp.waitFor(timeout, protocol.TypeGameOver)
	r.waitFor(timeout, protocol.TypeGameOver)

	fmt.Println("\nSMOKE TEST PASSED: match/ready/break/arbitration/anti-cheat/reconnect all OK")
	_ = os.Exit
}
