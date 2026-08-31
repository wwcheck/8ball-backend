package room

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/yourgame/8ball-backend/pkg/monitor"
	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// fakeClient is a room.Client backed by an in-memory mailbox so the room can be
// exercised end-to-end without a real WebSocket.
type fakeClient struct {
	id     string
	name   string
	avatar string

	mu     sync.Mutex
	sent   []protocol.Message
	roomID string
	closed bool
	reason string
}

func newFake(id, name string) *fakeClient {
	return &fakeClient{id: id, name: name}
}

func (c *fakeClient) PlayerID() string { return c.id }
func (c *fakeClient) Name() string     { return c.name }
func (c *fakeClient) Avatar() string   { return c.avatar }

func (c *fakeClient) Send(v any) {
	if m, ok := v.(protocol.Message); ok {
		c.mu.Lock()
		c.sent = append(c.sent, m)
		c.mu.Unlock()
	}
}

func (c *fakeClient) SetRoomID(id string) {
	c.mu.Lock()
	c.roomID = id
	c.mu.Unlock()
}

func (c *fakeClient) Close(reason string) {
	c.mu.Lock()
	c.closed = true
	c.reason = reason
	c.mu.Unlock()
}

func (c *fakeClient) Closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *fakeClient) types() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.sent))
	for i, m := range c.sent {
		out[i] = m.Head().Type
	}
	return out
}

func waitForType(t *testing.T, c *fakeClient, want string) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for _, m := range c.sent {
			if m.Head().Type == want {
				c.mu.Unlock()
				return m
			}
		}
		c.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("client %s did not receive %s; got %v", c.id, want, c.types())
	return nil
}

// post builds an envelope from a type + fields, then hands it to the room loop
// exactly as the gateway would.
func post(t *testing.T, r *Room, pid, typ string, fields map[string]any) {
	t.Helper()
	body := map[string]any{"type": typ}
	for k, v := range fields {
		body[k] = v
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s: %v", typ, err)
	}
	env, err := protocol.ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse %s: %v", typ, err)
	}
	env.PlayerID = pid
	r.PostMessage(pid, env, raw)
}

func newTestManager() *Manager {
	return NewManager(DefaultOptions(), monitor.New())
}

// seat makes a spectator grab the first free seat and asserts success.
func seat(t *testing.T, r *Room, c *fakeClient) *protocol.JoinGameAckResp {
	t.Helper()
	post(t, r, c.PlayerID(), protocol.TypeJoinGame, nil)
	ack := waitForType(t, c, protocol.TypeJoinGameAck).(*protocol.JoinGameAckResp)
	if ack.Status != "seated" {
		t.Fatalf("%s JOIN_GAME_ACK status=%s, want seated", c.id, ack.Status)
	}
	return ack
}

// TestCreateAndJoinAsSpectator: 统一进房都观众——房主与加入者均默认观众，不占座。
func TestCreateAndJoinAsSpectator(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r, seat, err := mgr.CreateRoom(a, false)
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	if seat != 0 {
		t.Fatalf("creator seat = %d, want 0 (spectator)", seat)
	}

	mjA := waitForType(t, a, protocol.TypeRoomJoined).(*protocol.RoomJoinedResp)
	if mjA.Role != protocol.RoleSpectator || mjA.YourSeat != 0 {
		t.Fatalf("creator role=%s seat=%d, want spectator/0", mjA.Role, mjA.YourSeat)
	}

	b := newFake("b", "Bob")
	if _, _, _, err := mgr.JoinRoom(b, r.ID, ""); err != nil {
		t.Fatalf("JoinRoom: %v", err)
	}
	mjB := waitForType(t, b, protocol.TypeRoomJoined).(*protocol.RoomJoinedResp)
	if mjB.Role != protocol.RoleSpectator || mjB.YourSeat != 0 {
		t.Fatalf("b role=%s seat=%d, want spectator/0", mjB.Role, mjB.YourSeat)
	}

	sum := r.Summary()
	if sum.SeatedCount != 0 || sum.PlayerCount != 2 {
		t.Fatalf("summary seated=%d total=%d, want 0/2", sum.SeatedCount, sum.PlayerCount)
	}
}

// TestJoinGameSeatAndFull: 抢座成功 + 第三人抢座被拒。
func TestJoinGameSeatAndFull(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r, _, _ := mgr.CreateRoom(a, false)
	seat(t, r, a) // a 抢到 seat1

	b := newFake("b", "Bob")
	if _, _, _, err := mgr.JoinRoom(b, r.ID, ""); err != nil {
		t.Fatalf("b JoinRoom: %v", err)
	}
	ackB := seat(t, r, b)
	if ackB.Seat != 2 {
		t.Fatalf("b seat=%d, want 2", ackB.Seat)
	}

	c := newFake("c", "Carol")
	if _, _, _, err := mgr.JoinRoom(c, r.ID, ""); err != nil {
		t.Fatalf("c JoinRoom: %v", err)
	}
	post(t, r, "c", protocol.TypeJoinGame, nil)

	errF := waitForType(t, c, protocol.TypeError).(*protocol.ErrorResp)
	if errF.ErrorCode != protocol.ErrRoomFull {
		t.Fatalf("c got error %s, want %s", errF.ErrorCode, protocol.ErrRoomFull)
	}
}

// TestStandUp: 离座让位，玩家回到观众席。
func TestStandUp(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r, _, _ := mgr.CreateRoom(a, false)
	seat(t, r, a)

	b := newFake("b", "Bob")
	if _, _, _, err := mgr.JoinRoom(b, r.ID, ""); err != nil {
		t.Fatalf("b JoinRoom: %v", err)
	}
	seat(t, r, b)

	post(t, r, "b", protocol.TypeStandUp, nil)
	ack := waitForType(t, b, protocol.TypeStandUpAck).(*protocol.StandUpAckResp)
	if ack.Status != "stood_down" {
		t.Fatalf("b STAND_UP_ACK status=%s, want stood_down", ack.Status)
	}

	sum := r.Summary()
	if sum.SeatedCount != 1 || sum.PlayerCount != 2 {
		t.Fatalf("summary seated=%d total=%d, want 1/2", sum.SeatedCount, sum.PlayerCount)
	}
}

// TestSpectatorBroadcastAndGuard: 观众完整观战（GAME_START / 摆位 / 出杆广播），
// 且观众不能发 SHOOT。
func TestSpectatorBroadcastAndGuard(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r, _, _ := mgr.CreateRoom(a, false)
	seat(t, r, a) // a 为 seat1（breaker）

	b := newFake("b", "Bob")
	if _, _, _, err := mgr.JoinRoom(b, r.ID, ""); err != nil {
		t.Fatalf("b JoinRoom: %v", err)
	}
	seat(t, r, b)

	c := newFake("c", "Carol")
	if _, _, _, err := mgr.JoinRoom(c, r.ID, ""); err != nil {
		t.Fatalf("c JoinRoom: %v", err)
	}

	post(t, r, "a", protocol.TypeReady, map[string]any{"ready": true})
	post(t, r, "b", protocol.TypeReady, map[string]any{"ready": true})
	waitForType(t, a, protocol.TypeGameStart)
	waitForType(t, b, protocol.TypeGameStart)
	waitForType(t, c, protocol.TypeGameStart)

	post(t, r, "a", protocol.TypeCueBallPlacement, map[string]any{
		"position": map[string]any{"x": protocol.HeadStringX, "y": 0, "z": 0},
	})
	waitForType(t, c, protocol.TypeCueBallPlacementAck)

	post(t, r, "a", protocol.TypeShoot, map[string]any{"cueAngle": 0, "power": 0.5})
	waitForType(t, c, protocol.TypeShotBroadcast)

	post(t, r, "c", protocol.TypeShoot, map[string]any{"cueAngle": 0, "power": 0.9})
	errF := waitForType(t, c, protocol.TypeError).(*protocol.ErrorResp)
	if errF.ErrorCode != protocol.ErrNotSeated {
		t.Fatalf("spectator SHOOT error=%s, want %s", errF.ErrorCode, protocol.ErrNotSeated)
	}
}

// TestGameOverReturnToReady: 局后留桌，两人回 READY 等待下一局。
func TestGameOverReturnToReady(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r, _, _ := mgr.CreateRoom(a, false)
	seat(t, r, a) // a 为 seat1（breaker）

	b := newFake("b", "Bob")
	if _, _, _, err := mgr.JoinRoom(b, r.ID, ""); err != nil {
		t.Fatalf("b JoinRoom: %v", err)
	}
	seat(t, r, b)

	post(t, r, "a", protocol.TypeReady, map[string]any{"ready": true})
	post(t, r, "b", protocol.TypeReady, map[string]any{"ready": true})
	waitForType(t, a, protocol.TypeGameStart)
	waitForType(t, b, protocol.TypeGameStart)

	post(t, r, "a", protocol.TypeConcede, nil)
	waitForType(t, a, protocol.TypeGameOver)
	waitForType(t, b, protocol.TypeGameOver)

	// 局后回到 READY，且两人仍在座（留桌）。summary 由房间 goroutine 在
	// returnToReady 中同步更新，轮询直到状态到位。
	deadline := time.Now().Add(3 * time.Second)
	for {
		sum := r.Summary()
		if sum.Status == protocol.RoomStatusReady && sum.SeatedCount == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("room did not return to READY with 2 seated; got status=%s seated=%d",
				sum.Status, sum.SeatedCount)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestFixedRoomLazyCreate: 固定房间号 "1000" 懒创建、复用、关闭后重建。
func TestFixedRoomLazyCreate(t *testing.T) {
	mgr := newTestManager()
	defer mgr.Shutdown("test")

	a := newFake("a", "Alice")
	r1, seat, resumed, err := mgr.JoinRoom(a, fixedRoomID, "")
	if err != nil {
		t.Fatalf("a JoinRoom(%s): %v", fixedRoomID, err)
	}
	if r1.ID != fixedRoomID {
		t.Fatalf("room id=%s, want %s", r1.ID, fixedRoomID)
	}
	if seat != 0 || resumed {
		t.Fatalf("a seat=%d resumed=%v, want 0/false (default spectator)", seat, resumed)
	}
	mjA := waitForType(t, a, protocol.TypeRoomJoined).(*protocol.RoomJoinedResp)
	if mjA.Role != protocol.RoleSpectator {
		t.Fatalf("a role=%s, want spectator", mjA.Role)
	}

	// 同号进房应复用同一个房间实例。
	b := newFake("b", "Bob")
	r2, _, _, err := mgr.JoinRoom(b, fixedRoomID, "")
	if err != nil {
		t.Fatalf("b JoinRoom(%s): %v", fixedRoomID, err)
	}
	if r2 != r1 || r2.ID != fixedRoomID {
		t.Fatalf("b joined a different room: %p vs %p", r2, r1)
	}
	if got := mgr.Count(); got != 1 {
		t.Fatalf("room count=%d, want 1", got)
	}

	// 清空房间使其关闭，再 JOIN "1000" 应懒创建新实例。
	post(t, r1, "a", protocol.TypeLeaveRoom, nil)
	post(t, r1, "b", protocol.TypeLeaveRoom, nil)
	deadline := time.Now().Add(3 * time.Second)
	for mgr.Count() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if mgr.Count() != 0 {
		t.Fatalf("room should close when empty, count=%d", mgr.Count())
	}

	c := newFake("c", "Carol")
	r3, _, _, err := mgr.JoinRoom(c, fixedRoomID, "")
	if err != nil {
		t.Fatalf("c JoinRoom(%s): %v", fixedRoomID, err)
	}
	if r3 == r1 {
		t.Fatalf("expected a fresh room instance after close")
	}
	if r3.ID != fixedRoomID || mgr.Count() != 1 {
		t.Fatalf("recreated room id=%s count=%d, want %s/1", r3.ID, mgr.Count(), fixedRoomID)
	}
}
