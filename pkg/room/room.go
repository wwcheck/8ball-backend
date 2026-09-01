// Package room owns the lifecycle of a single 8-ball match.
//
// Concurrency model: every Room runs exactly one goroutine (Room.run). All
// mutable game state - the rules.Game, the seat map, deadlines - is touched only
// by that goroutine, so no locks are needed around gameplay. Other goroutines
// (WebSocket read loops, the matchmaker, HTTP handlers) interact by posting
// events onto the room's inbox channel. The only shared, lock-protected data is
// the read-only Summary used by the REST endpoints.
package room

import (
	"log"
	"sort"
	"sync"
	"time"

	"github.com/yourgame/8ball-backend/pkg/monitor"
	"github.com/yourgame/8ball-backend/pkg/protocol"
	"github.com/yourgame/8ball-backend/pkg/rules"
)

// Client is the subset of transport.Session the room needs. Keeping it an
// interface lets the room be unit-tested without a real WebSocket.
type Client interface {
	PlayerID() string
	Name() string
	Avatar() string
	Send(v any)
	SetRoomID(id string)
	Close(reason string)
	Closed() bool
}

// Options tunes room and game timing.
type Options struct {
	TurnTimeout     time.Duration // budget for Aiming / BallInHand
	ShotTimeout     time.Duration // budget for Moving -> SHOT_RESULT
	ReconnectWindow time.Duration // REQ-010: 30s
	WaitingTTL      time.Duration // REQ-010: close empty rooms after 5min
	FinishedTTL     time.Duration // keep finished rooms briefly for result UI
	StateUpdateHz   int
	InboxSize       int
	Rules           rules.Options
}

// DefaultOptions returns the documented defaults.
func DefaultOptions() Options {
	return Options{
		TurnTimeout:     60 * time.Second,
		ShotTimeout:     25 * time.Second,
		ReconnectWindow: 30 * time.Second,
		WaitingTTL:      5 * time.Minute,
		FinishedTTL:     90 * time.Second,
		StateUpdateHz:   20,
		InboxSize:       512,
		Rules:           rules.DefaultOptions(),
	}
}

// Summary is the lock-protected, REST-friendly view of a room.
type Summary struct {
	RoomID          string    `json:"roomId"`
	InviteCode      string    `json:"inviteCode,omitempty"`
	IsPublic        bool      `json:"isPublic"`
	Status          string    `json:"status"`
	PlayerCount     int       `json:"playerCount"` // total members: seated + spectators
	SeatedCount     int       `json:"seatedCount"` // 0..2
	Players         []string  `json:"players"`     // all member ids
	GamePhase       string    `json:"gamePhase"`
	CurrentPlayerID string    `json:"currentPlayerId,omitempty"`
	ShotNumber      int       `json:"shotNumber"`
	CreatedAt       time.Time `json:"createdAt"`
}

type eventKind int

const (
	evtJoin eventKind = iota
	evtMessage
	evtDisconnect
	evtShutdown
)

type joinResult struct {
	Seat    int
	Role    string
	Resumed bool
	Err     error
}

// joinMode tells onJoin whether a brand-new member should be seated or watched.
type joinMode int

const (
	// joinAuto resumes a known member (seated or spectator) and defaults new
	// members to spectator. Used by JOIN_ROOM / RECONNECT.
	joinAuto joinMode = iota
	// joinSeated seats a new member in the first free seat. Used by the room
	// creator (CREATE_ROOM) and the quick-match pairing.
	joinSeated
)

// spectator is one non-seated member watching the match. It is loop-owned, like
// every other piece of mutable room state.
type spectator struct {
	PlayerID  string
	Name      string
	Avatar    string
	Connected bool
}

// info projects a spectator to its wire representation.
func (s *spectator) info() protocol.PlayerInfo {
	return protocol.PlayerInfo{
		PlayerID:  s.PlayerID,
		Name:      s.Name,
		Avatar:    s.Avatar,
		Role:      protocol.RoleSpectator,
		Position:  0,
		Connected: s.Connected,
	}
}

type event struct {
	kind     eventKind
	playerID string
	client   Client
	env      protocol.Envelope
	raw      []byte
	reason   string
	mode     joinMode
	reply    chan joinResult
}

// Room is one match: two seats, one authoritative game, one goroutine.
type Room struct {
	ID         string
	InviteCode string
	IsPublic   bool
	OwnerID    string
	CreatedAt  time.Time

	opts    Options
	mgr     *Manager
	metrics *monitor.Metrics

	inbox     chan event
	quit      chan struct{}
	closeOnce sync.Once

	// --- loop-owned state (no locking) ---
	status       string
	game         *rules.Game
	clients      map[string]Client
	spectators   map[string]*spectator
	dcDeadline   map[string]time.Time
	seq          uint64
	turnDeadline time.Time
	shotDeadline time.Time
	expiresAt    time.Time
	startedAt    time.Time
	closed       bool

	summaryMu sync.RWMutex
	summary   Summary
}

func newRoom(id, invite string, isPublic bool, opts Options, mgr *Manager, m *monitor.Metrics) *Room {
	now := time.Now()
	r := &Room{
		ID:         id,
		InviteCode: invite,
		IsPublic:   isPublic,
		CreatedAt:  now,
		opts:       opts,
		mgr:        mgr,
		metrics:    m,
		inbox:      make(chan event, opts.InboxSize),
		quit:       make(chan struct{}),
		status:     protocol.RoomStatusWaiting,
		game:       rules.NewGame(opts.Rules),
		clients:    make(map[string]Client, 8),
		spectators: make(map[string]*spectator, 8),
		dcDeadline: make(map[string]time.Time, 2),
		expiresAt:  now.Add(opts.WaitingTTL),
	}
	r.syncSummary()
	go r.run()
	return r
}

// ---------------------------------------------------------------------------
// external API (called from other goroutines)
// ---------------------------------------------------------------------------

// Summary returns a consistent snapshot for REST endpoints.
func (r *Room) Summary() Summary {
	r.summaryMu.RLock()
	defer r.summaryMu.RUnlock()
	return r.summary
}

// PostMessage hands an inbound protocol frame to the room loop.
func (r *Room) PostMessage(playerID string, env protocol.Envelope, raw []byte) {
	ev := event{kind: evtMessage, playerID: playerID, env: env, raw: raw}
	select {
	case r.inbox <- ev:
	case <-r.quit:
	default:
		// The inbox is full. STATE_FRAME is best-effort (MULTIPLAYER_TECH.md
		// §2.3) so drop it; anything else is reliable and must not be lost, so
		// block briefly before giving up.
		if env.Type == protocol.TypeStateFrame {
			return
		}
		select {
		case r.inbox <- ev:
		case <-r.quit:
		case <-time.After(500 * time.Millisecond):
			log.Printf("[room %s] inbox congested, dropped %s from %s", r.ID, env.Type, playerID)
		}
	}
}

// PostDisconnect notifies the room that a player's socket died.
func (r *Room) PostDisconnect(playerID string) {
	select {
	case r.inbox <- event{kind: evtDisconnect, playerID: playerID}:
	case <-r.quit:
	}
}

// Shutdown asks the room to close (server shutdown / admin action).
func (r *Room) Shutdown(reason string) {
	select {
	case r.inbox <- event{kind: evtShutdown, reason: reason}:
	case <-r.quit:
	}
}

// join adds or resumes a player. Blocking, with a hard timeout so a wedged room
// can never stall a WebSocket read loop.
//
// Consistency contract: the send-phase timeout is safe because an event that
// was never delivered touches no state. The reply wait, however, has NO
// timeout: once the evtJoin event is accepted into the inbox the room loop WILL
// process it (every handler is non-blocking), so giving up early would tell the
// caller "busy, retry" while the player is in fact already seated - and the
// retry would then fail with ALREADY_IN_ROOM. The only legitimate
// non-delivery case is the room dying, which the quit channel covers.
func (r *Room) join(c Client, mode joinMode) joinResult {
	reply := make(chan joinResult, 1)
	ev := event{kind: evtJoin, playerID: c.PlayerID(), client: c, mode: mode, reply: reply}

	select {
	case r.inbox <- ev:
	case <-r.quit:
		return joinResult{Err: protocol.Errf(protocol.ErrRoomClosed, "房间已关闭")}
	case <-time.After(2 * time.Second):
		// The event was never enqueued: no state was touched, so failing
		// here is consistent.
		return joinResult{Err: protocol.Errf(protocol.ErrInternal, "房间繁忙，请重试")}
	}

	select {
	case res := <-reply:
		return res
	case <-r.quit:
		return joinResult{Err: protocol.Errf(protocol.ErrRoomClosed, "房间已关闭")}
	}
}

// ---------------------------------------------------------------------------
// the loop
// ---------------------------------------------------------------------------

func (r *Room) run() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	defer r.teardown()

	for {
		select {
		case ev := <-r.inbox:
			switch ev.kind {
			case evtJoin:
				res := r.onJoin(ev.client, ev.mode)
				if ev.reply != nil {
					ev.reply <- res
				}
			case evtMessage:
				r.onMessage(ev.playerID, ev.env, ev.raw)
			case evtDisconnect:
				r.onDisconnect(ev.playerID)
			case evtShutdown:
				r.closeRoom(ev.reason)
			}
		case now := <-ticker.C:
			r.onTick(now)
		}
		if r.closed {
			return
		}
	}
}

func (r *Room) teardown() {
	r.closeOnce.Do(func() { close(r.quit) })
	for id, c := range r.clients {
		if c != nil {
			c.SetRoomID("")
		}
		r.mgr.unbind(id, r.ID)
	}
	r.mgr.remove(r.ID, r.InviteCode)
	r.metrics.RoomClosed()
	log.Printf("[room %s] closed (status=%s)", r.ID, r.status)
}

// ---------------------------------------------------------------------------
// messaging helpers
// ---------------------------------------------------------------------------

func (r *Room) stamp(msg protocol.Message) {
	h := msg.Head()
	h.RoomID = r.ID
	r.seq++
	h.Seq = r.seq
	h.ServerTime = time.Now().UnixMilli()
	if h.Timestamp == 0 {
		h.Timestamp = h.ServerTime
	}
}

func (r *Room) deliver(playerID string, msg protocol.Message) {
	if c := r.clients[playerID]; c != nil && !c.Closed() {
		c.Send(msg)
		r.metrics.MessageOut()
	}
}

func (r *Room) sendTo(playerID string, msg protocol.Message) {
	r.stamp(msg)
	r.deliver(playerID, msg)
}

func (r *Room) broadcast(msg protocol.Message) {
	r.stamp(msg)
	for id := range r.clients {
		r.deliver(id, msg)
	}
}

func (r *Room) sendExcept(exclude string, msg protocol.Message) {
	r.stamp(msg)
	for id := range r.clients {
		if id != exclude {
			r.deliver(id, msg)
		}
	}
}

func (r *Room) fail(playerID, messageID, code, message string) {
	e := protocol.NewError(code, message)
	e.MessageID = messageID
	e.PlayerID = playerID
	r.metrics.ErrorSent()
	r.sendTo(playerID, e)
}

// failErr unwraps a *protocol.GameError into an ERROR frame.
func (r *Room) failErr(playerID, messageID string, err error) {
	if ge, ok := err.(*protocol.GameError); ok {
		r.fail(playerID, messageID, ge.Code, ge.Message)
		return
	}
	r.fail(playerID, messageID, protocol.ErrInternal, err.Error())
}

// snapshot builds the authoritative GameStateDTO with room-level fields filled.
func (r *Room) Snapshot() protocol.GameStateDTO {
	s := r.game.Snapshot()
	s.RoomID = r.ID
	s.RoomStatus = r.status
	s.InviteCode = r.InviteCode
	s.Spectators = r.spectatorInfos()
	s.Seq = r.seq
	return s
}

// spectatorInfos returns the spectator list, ordered by join sequence (the map
// is not ordered; sort by player id for stable output).
func (r *Room) spectatorInfos() []protocol.PlayerInfo {
	out := make([]protocol.PlayerInfo, 0, len(r.spectators))
	for _, sp := range r.spectators {
		out = append(out, sp.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlayerID < out[j].PlayerID })
	return out
}

func (r *Room) syncSummary() {
	seated := make([]string, 0, 2)
	for _, p := range r.game.Players {
		if p != nil {
			seated = append(seated, p.ID)
		}
	}
	all := make([]string, 0, len(seated)+len(r.spectators))
	all = append(all, seated...)
	for id := range r.spectators {
		all = append(all, id)
	}
	sort.Strings(all)
	r.summaryMu.Lock()
	r.summary = Summary{
		RoomID:          r.ID,
		InviteCode:      r.InviteCode,
		IsPublic:        r.IsPublic,
		Status:          r.status,
		PlayerCount:     len(all),
		SeatedCount:     len(seated),
		Players:         all,
		GamePhase:       r.game.Phase,
		CurrentPlayerID: r.game.CurrentTurn,
		ShotNumber:      r.game.ShotNumber,
		CreatedAt:       r.CreatedAt,
	}
	r.summaryMu.Unlock()
}

// ---------------------------------------------------------------------------
// join / leave / disconnect
// ---------------------------------------------------------------------------

func (r *Room) onJoin(c Client, mode joinMode) joinResult {
	if c == nil {
		return joinResult{Err: protocol.Errf(protocol.ErrInternal, "无效连接")}
	}
	if r.closed || r.status == protocol.RoomStatusClosed {
		return joinResult{Err: protocol.Errf(protocol.ErrRoomClosed, "房间已关闭")}
	}
	pid := c.PlayerID()

	// --- resume an existing seat -----------------------------------------
	if p := r.game.PlayerByID(pid); p != nil {
		r.clients[pid] = c
		delete(r.dcDeadline, pid)
		p.Connected = true
		c.SetRoomID(r.ID)
		r.mgr.bind(pid, r.ID)
		r.metrics.Reconnected()

		snap := &protocol.SnapshotResp{
			Envelope:  protocol.Envelope{Type: protocol.TypeSnapshot, PlayerID: pid},
			GameState: r.Snapshot(),
			Resumed:   true,
		}
		r.sendTo(pid, snap)

		if opp := r.game.Opponent(pid); opp != nil {
			r.sendExcept(pid, &protocol.PlayerEventResp{
				Envelope: protocol.Envelope{Type: protocol.TypePlayerReconnected, PlayerID: pid},
				Player:   r.game.PlayerInfo(p),
			})
		}
		// Give the returning player a fresh clock for the current turn.
		if r.game.CurrentTurn == pid && r.isTurnPhase() {
			r.turnDeadline = time.Now().Add(r.opts.TurnTimeout)
		}
		r.syncSummary()
		log.Printf("[room %s] player %s resumed seat %d", r.ID, pid, p.Seat)
		return joinResult{Seat: p.Seat, Role: protocol.RoleSeated, Resumed: true}
	}

	// --- resume a spectator ----------------------------------------------
	if sp, ok := r.spectators[pid]; ok {
		r.clients[pid] = c
		delete(r.dcDeadline, pid)
		sp.Connected = true
		c.SetRoomID(r.ID)
		r.mgr.bind(pid, r.ID)
		r.metrics.Reconnected()

		r.sendTo(pid, &protocol.SnapshotResp{
			Envelope:  protocol.Envelope{Type: protocol.TypeSnapshot, PlayerID: pid},
			GameState: r.Snapshot(),
			Resumed:   true,
		})
		r.syncSummary()
		r.broadcastRoomState()
		log.Printf("[room %s] spectator %s resumed", r.ID, pid)
		return joinResult{Role: protocol.RoleSpectator, Resumed: true}
	}

	// --- brand new member ------------------------------------------------
	if mode == joinSeated {
		return r.seatNewMember(c)
	}
	return r.addSpectator(c)
}

// seatNewMember seats a brand-new member in the first free seat.
func (r *Room) seatNewMember(c Client) joinResult {
	pid := c.PlayerID()
	if r.game.Seated() >= 2 {
		return joinResult{Err: protocol.Errf(protocol.ErrRoomFull, "房间已满")}
	}
	if r.status == protocol.RoomStatusInGame || r.status == protocol.RoomStatusFinished {
		return joinResult{Err: protocol.Errf(protocol.ErrRoomClosed, "对局已开始，无法加入")}
	}

	p, err := r.game.AddPlayer(pid, c.Name(), c.Avatar())
	if err != nil {
		return joinResult{Err: protocol.Errf(protocol.ErrRoomFull, "房间已满")}
	}
	r.clients[pid] = c
	c.SetRoomID(r.ID)
	r.mgr.bind(pid, r.ID)
	if r.OwnerID == "" {
		r.OwnerID = pid
	}
	if r.game.Seated() == 2 {
		r.status = protocol.RoomStatusReady
		r.expiresAt = time.Now().Add(r.opts.WaitingTTL)
	}
	r.syncSummary()

	r.sendTo(pid, &protocol.RoomJoinedResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeRoomJoined, PlayerID: pid},
		Status:    "success",
		YourSeat:  p.Seat,
		Role:      protocol.RoleSeated,
		GameState: r.Snapshot(),
	})
	r.sendExcept(pid, &protocol.PlayerEventResp{
		Envelope: protocol.Envelope{Type: protocol.TypePlayerJoined, PlayerID: pid},
		Player:   r.game.PlayerInfo(p),
	})
	r.broadcastRoomState()
	log.Printf("[room %s] player %s joined seat %d (%d/2)", r.ID, pid, p.Seat, r.game.Seated())
	return joinResult{Seat: p.Seat, Role: protocol.RoleSeated}
}

// addSpectator adds a brand-new member as a viewer (no match seat). Spectators
// may join even while a game is in progress, so they can watch it live.
func (r *Room) addSpectator(c Client) joinResult {
	pid := c.PlayerID()
	sp := &spectator{PlayerID: pid, Name: c.Name(), Avatar: c.Avatar(), Connected: true}
	r.spectators[pid] = sp
	r.clients[pid] = c
	c.SetRoomID(r.ID)
	r.mgr.bind(pid, r.ID)
	if r.OwnerID == "" {
		r.OwnerID = pid
	}
	r.syncSummary()

	r.sendTo(pid, &protocol.RoomJoinedResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeRoomJoined, PlayerID: pid},
		Status:    "success",
		YourSeat:  0,
		Role:      protocol.RoleSpectator,
		GameState: r.Snapshot(),
	})
	r.sendExcept(pid, &protocol.PlayerEventResp{
		Envelope: protocol.Envelope{Type: protocol.TypePlayerJoined, PlayerID: pid},
		Player:   sp.info(),
	})
	r.broadcastRoomState()
	log.Printf("[room %s] spectator %s joined (%d watching)", r.ID, pid, len(r.spectators))
	return joinResult{Role: protocol.RoleSpectator}
}

func (r *Room) broadcastRoomState() {
	r.broadcast(&protocol.RoomStateResp{
		Envelope:   protocol.Envelope{Type: protocol.TypeRoomState},
		RoomStatus: r.status,
		Players:    r.game.PlayerInfos(),
		Spectators: r.spectatorInfos(),
		GamePhase:  r.game.Phase,
	})
}

func (r *Room) onDisconnect(playerID string) {
	p := r.game.PlayerByID(playerID)
	if p == nil {
		// Spectators have no seat to protect: drop them immediately. They may
		// re-JOIN_ROOM later to watch again.
		if sp, ok := r.spectators[playerID]; ok {
			delete(r.spectators, playerID)
			delete(r.clients, playerID)
			delete(r.dcDeadline, playerID)
			r.mgr.unbind(playerID, r.ID)
			r.sendExcept(playerID, &protocol.PlayerEventResp{
				Envelope: protocol.Envelope{Type: protocol.TypePlayerLeft, PlayerID: playerID},
				Player:   sp.info(),
			})
			r.syncSummary()
			r.broadcastRoomState()
		} else {
			delete(r.clients, playerID)
		}
		return
	}
	delete(r.clients, playerID)
	p.Connected = false

	if r.game.Finished() || r.status == protocol.RoomStatusFinished {
		r.syncSummary()
		if len(r.clients) == 0 {
			r.closeRoom(protocol.ReasonRoomClosed)
		}
		return
	}

	deadline := time.Now().Add(r.opts.ReconnectWindow)
	r.dcDeadline[playerID] = deadline
	r.syncSummary()

	r.sendExcept(playerID, &protocol.PlayerDisconnectedResp{
		Envelope:          protocol.Envelope{Type: protocol.TypePlayerDisconnected, PlayerID: playerID},
		Player:            r.game.PlayerInfo(p),
		ReconnectWindowMs: int(r.opts.ReconnectWindow.Milliseconds()),
		DeadlineUnixMs:    deadline.UnixMilli(),
	})
	log.Printf("[room %s] player %s disconnected, reconnect window %s", r.ID, playerID, r.opts.ReconnectWindow)
}

// leaveRoom is a voluntary exit (LEAVE_ROOM). A seated player leaving mid-game
// forfeits; a spectator leaving is simply removed from the audience.
func (r *Room) leaveRoom(playerID string) {
	p := r.game.PlayerByID(playerID)
	if p == nil {
		// Spectator leave.
		if sp, ok := r.spectators[playerID]; ok {
			delete(r.spectators, playerID)
			if c := r.clients[playerID]; c != nil {
				c.SetRoomID("")
			}
			delete(r.clients, playerID)
			delete(r.dcDeadline, playerID)
			r.mgr.unbind(playerID, r.ID)
			r.sendExcept(playerID, &protocol.PlayerEventResp{
				Envelope: protocol.Envelope{Type: protocol.TypePlayerLeft, PlayerID: playerID},
				Player:   sp.info(),
			})
			r.syncSummary()
			if r.game.Seated() == 0 && len(r.spectators) == 0 {
				r.closeRoom(protocol.ReasonRoomClosed)
				return
			}
			r.broadcastRoomState()
		}
		return
	}

	inPlay := r.status == protocol.RoomStatusInGame && !r.game.Finished()

	r.sendExcept(playerID, &protocol.PlayerEventResp{
		Envelope: protocol.Envelope{Type: protocol.TypePlayerLeft, PlayerID: playerID},
		Player:   r.game.PlayerInfo(p),
		Reason:   protocol.ReasonOpponentLeft,
	})

	if c := r.clients[playerID]; c != nil {
		c.SetRoomID("")
	}
	delete(r.clients, playerID)
	delete(r.dcDeadline, playerID)
	r.mgr.unbind(playerID, r.ID)

	if inPlay {
		// 对局中离开 = 认负；随后释放座位，供观众下一局抢座。
		r.forfeitSeat(playerID, protocol.ReasonOpponentLeft)
		return
	}

	r.game.RemovePlayer(playerID)
	if r.game.Seated() == 0 && len(r.spectators) == 0 {
		r.closeRoom(protocol.ReasonRoomClosed)
		return
	}
	r.status = protocol.RoomStatusWaiting
	// A remaining player must re-ready once the opponent slot refills.
	for _, pl := range r.game.Players {
		if pl != nil {
			pl.Ready = false
		}
	}
	r.game.Phase = protocol.PhaseWaiting
	r.expiresAt = time.Now().Add(r.opts.WaitingTTL)
	r.syncSummary()
	r.broadcastRoomState()
}

// forfeitSeat ends the game in favour of the opponent and frees the forfeiting
// player's seat so a spectator may claim it for the next round. Callers must
// have already removed the player's client from r.clients.
func (r *Room) forfeitSeat(playerID, reason string) {
	r.game.ForfeitByAbandon(playerID, reason)
	r.metrics.Forfeit()
	r.publishGameOver(reason)
	r.game.RemovePlayer(playerID)
	r.mgr.unbind(playerID, r.ID)
	delete(r.dcDeadline, playerID)

	if r.game.Seated() == 0 && len(r.spectators) == 0 {
		r.closeRoom(protocol.ReasonRoomClosed)
		return
	}
	r.status = protocol.RoomStatusWaiting
	r.syncSummary()
	r.broadcastRoomState()
}

func (r *Room) closeRoom(reason string) {
	if r.closed {
		return
	}
	r.closed = true
	r.status = protocol.RoomStatusClosed
	r.syncSummary()
	if reason == "" {
		reason = protocol.ReasonRoomClosed
	}
	r.broadcast(&protocol.ErrorResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeError},
		ErrorCode: protocol.ErrRoomClosed,
		Code:      protocol.NumericCode(protocol.ErrRoomClosed),
		Message:   "房间已关闭：" + reason,
		Fatal:     true,
	})
}

// ---------------------------------------------------------------------------
// message dispatch
// ---------------------------------------------------------------------------

func (r *Room) onMessage(playerID string, env protocol.Envelope, raw []byte) {
	if !r.isMember(playerID) {
		r.fail(playerID, env.MessageID, protocol.ErrNotInRoom, "你不在该房间内")
		return
	}
	// 诊断日志：记录所有进入的消息
	if env.Type == protocol.TypeShotResult {
		log.Printf("[room %s] onMessage: 收到 %s from %s", r.ID, env.Type, playerID)
	}
	switch env.Type {
	case protocol.TypeJoinGame:
		r.handleJoinGame(playerID, env)
	case protocol.TypeStandUp:
		r.handleStandUp(playerID, env)
	case protocol.TypeLeaveRoom:
		r.leaveRoom(playerID)
	case protocol.TypeRequestSnapshot:
		r.sendTo(playerID, &protocol.SnapshotResp{
			Envelope:  protocol.Envelope{Type: protocol.TypeSnapshot, PlayerID: playerID, MessageID: env.MessageID},
			GameState: r.Snapshot(),
		})
	case protocol.TypeReady,
		protocol.TypeShoot,
		protocol.TypeStateFrame,
		protocol.TypeShotResult,
		protocol.TypeCueBallPlacement,
		protocol.TypeConcede:
		if r.game.PlayerByID(playerID) == nil {
			r.fail(playerID, env.MessageID, protocol.ErrNotSeated, "你当前是观众，请先上桌（JOIN_GAME）")
			return
		}
		switch env.Type {
		case protocol.TypeReady:
			r.handleReady(playerID, raw, env)
		case protocol.TypeShoot:
			r.handleShoot(playerID, raw, env)
		case protocol.TypeStateFrame:
			r.handleStateFrame(playerID, raw, env)
		case protocol.TypeShotResult:
			r.handleShotResult(playerID, raw, env)
		case protocol.TypeCueBallPlacement:
			r.handlePlacement(playerID, raw, env)
		case protocol.TypeConcede:
			r.handleConcede(playerID, env)
		}
	default:
		r.fail(playerID, env.MessageID, protocol.ErrUnknownType,
			"房间内不支持的消息类型: "+env.Type)
	}
}

// isMember reports whether playerID is either seated or a spectator.
func (r *Room) isMember(playerID string) bool {
	if r.game.PlayerByID(playerID) != nil {
		return true
	}
	_, ok := r.spectators[playerID]
	return ok
}

// handleJoinGame moves a spectator into a free match seat.
func (r *Room) handleJoinGame(playerID string, env protocol.Envelope) {
	if r.game.PlayerByID(playerID) != nil {
		r.fail(playerID, env.MessageID, protocol.ErrAlreadySeated, "你已在上桌对战")
		return
	}
	if r.game.Seated() >= 2 {
		r.fail(playerID, env.MessageID, protocol.ErrRoomFull, "对战座位已满，请观战")
		return
	}
	if r.status == protocol.RoomStatusInGame || r.status == protocol.RoomStatusFinished {
		r.fail(playerID, env.MessageID, protocol.ErrInvalidPhase, "对局进行中，无法上桌")
		return
	}

	sp, ok := r.spectators[playerID]
	if !ok {
		r.fail(playerID, env.MessageID, protocol.ErrNotInRoom, "你不在该房间内")
		return
	}
	delete(r.spectators, playerID)

	p, err := r.game.AddPlayer(playerID, sp.Name, sp.Avatar)
	if err != nil {
		// Re-add as spectator so the room state stays consistent.
		r.spectators[playerID] = sp
		r.fail(playerID, env.MessageID, protocol.ErrRoomFull, "对战座位已满，请观战")
		return
	}
	if r.game.Seated() == 2 {
		r.status = protocol.RoomStatusReady
	}
	r.expiresAt = time.Now().Add(r.opts.WaitingTTL)
	r.syncSummary()

	r.sendTo(playerID, &protocol.JoinGameAckResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeJoinGameAck, PlayerID: playerID, MessageID: env.MessageID},
		Status:    "seated",
		Seat:      p.Seat,
		GameState: r.Snapshot(),
	})
	r.broadcastRoomState()
	log.Printf("[room %s] player %s grabbed seat %d (%d/2)", r.ID, playerID, p.Seat, r.game.Seated())
}

// handleStandUp vacates a match seat and turns the player back into a spectator.
// Mid-game it is treated as a forfeit.
func (r *Room) handleStandUp(playerID string, env protocol.Envelope) {
	p := r.game.PlayerByID(playerID)
	if p == nil {
		r.fail(playerID, env.MessageID, protocol.ErrNotSeated, "你未上桌，无需离座")
		return
	}

	if r.status == protocol.RoomStatusInGame && !r.game.Finished() {
		// 对局中离座 = 认负，胜者留桌，离座者回到观众席。
		r.game.ForfeitByAbandon(playerID, protocol.ReasonOpponentLeft)
		r.metrics.Forfeit()
		r.publishGameOver(protocol.ReasonOpponentLeft)
		r.game.RemovePlayer(playerID)
		r.spectators[playerID] = &spectator{
			PlayerID:  playerID,
			Name:      p.Name,
			Avatar:    p.Avatar,
			Connected: true,
		}
		r.status = protocol.RoomStatusWaiting
		r.syncSummary()
		r.sendTo(playerID, &protocol.StandUpAckResp{
			Envelope:  protocol.Envelope{Type: protocol.TypeStandUpAck, PlayerID: playerID, MessageID: env.MessageID},
			Status:    "stood_down",
			GameState: r.Snapshot(),
		})
		r.broadcastRoomState()
		return
	}

	r.game.RemovePlayer(playerID)
	r.spectators[playerID] = &spectator{
		PlayerID:  playerID,
		Name:      p.Name,
		Avatar:    p.Avatar,
		Connected: true,
	}
	r.status = protocol.RoomStatusWaiting
	for _, pl := range r.game.Players {
		if pl != nil {
			pl.Ready = false
		}
	}
	r.game.Phase = protocol.PhaseWaiting
	r.expiresAt = time.Now().Add(r.opts.WaitingTTL)
	r.syncSummary()

	r.sendTo(playerID, &protocol.StandUpAckResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeStandUpAck, PlayerID: playerID, MessageID: env.MessageID},
		Status:    "stood_down",
		GameState: r.Snapshot(),
	})
	r.broadcastRoomState()
	log.Printf("[room %s] player %s stood up (now spectator)", r.ID, playerID)
}

func (r *Room) handleReady(playerID string, raw []byte, env protocol.Envelope) {
	req, err := protocol.Decode[protocol.ReadyReq](raw)
	if err != nil {
		r.fail(playerID, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	if r.status == protocol.RoomStatusInGame || r.game.Finished() {
		r.fail(playerID, env.MessageID, protocol.ErrInvalidPhase, "对局已开始")
		return
	}
	p := r.game.PlayerByID(playerID)
	p.Ready = req.Ready
	log.Printf("[room %s] player %s ready=%v (p1.Ready=%v, p2.Ready=%v)", 
		r.ID, playerID, req.Ready, r.game.Players[0].Ready, r.game.Players[1].Ready)
	r.syncSummary()
	r.broadcastRoomState()

	if r.game.BothReady() {
		log.Printf("[room %s] both players ready, starting game", r.ID)
		r.startGame()
	}
}

func (r *Room) startGame() {
	now := time.Now()
	r.game.Start(now)
	r.status = protocol.RoomStatusInGame
	r.startedAt = now
	r.turnDeadline = now.Add(r.opts.TurnTimeout)
	r.shotDeadline = time.Time{}
	r.expiresAt = time.Time{}
	r.metrics.GameStarted()
	r.syncSummary()

	r.broadcast(&protocol.GameStartResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeGameStart},
		GameState: r.Snapshot(),
		BreakerID: r.game.CurrentTurn,
	})
	r.broadcastTurnChange()
	log.Printf("[room %s] game started, breaker=%s", r.ID, r.game.CurrentTurn)
}

func (r *Room) broadcastTurnChange() {
	r.broadcast(&protocol.TurnChangeResp{
		Envelope:        protocol.Envelope{Type: protocol.TypeTurnChange},
		CurrentPlayerID: r.game.CurrentTurn,
		GamePhase:       r.game.Phase,
		BallInHand:      r.game.BallInHand,
		KitchenOnly:     r.game.KitchenOnly,
		TurnTimeoutMs:   int(r.opts.TurnTimeout.Milliseconds()),
		BallStates:      r.game.BallStates(),
	})
}

func (r *Room) handleShoot(playerID string, raw []byte, env protocol.Envelope) {
	req, err := protocol.Decode[protocol.ShootReq](raw)
	if err != nil {
		r.fail(playerID, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	if err := r.game.ValidateShoot(playerID, req.CueAngle, req.Power, req.Spin); err != nil {
		r.metrics.ShotRejected()
		code, msg := protocol.ErrInvalidShot, err.Error()
		if ge, ok := err.(*protocol.GameError); ok {
			code, msg = ge.Code, ge.Message
		}
		r.sendTo(playerID, &protocol.ShootAckResp{
			Envelope:   protocol.Envelope{Type: protocol.TypeShootAck, PlayerID: playerID, MessageID: env.MessageID},
			Status:     "rejected",
			ShotNumber: r.game.ShotNumber,
			GamePhase:  r.game.Phase,
			ErrorCode:  code,
			Message:    msg,
		})
		r.fail(playerID, env.MessageID, code, msg)
		return
	}

	shotNo := r.game.ApplyShoot(playerID, req.CueAngle, req.Power, req.Spin)
	r.metrics.ShotAccepted()
	r.turnDeadline = time.Time{}
	r.shotDeadline = time.Now().Add(r.opts.ShotTimeout)
	r.syncSummary()

	r.sendTo(playerID, &protocol.ShootAckResp{
		Envelope:   protocol.Envelope{Type: protocol.TypeShootAck, PlayerID: playerID, MessageID: env.MessageID},
		Status:     "accepted",
		ShotNumber: shotNo,
		GamePhase:  r.game.Phase,
	})
	r.broadcast(&protocol.ShotBroadcastResp{
		Envelope:          protocol.Envelope{Type: protocol.TypeShotBroadcast, PlayerID: playerID},
		ShooterID:         playerID,
		ShotNumber:        shotNo,
		CueAngle:          req.CueAngle,
		Power:             req.Power,
		Spin:              req.Spin,
		InitialSpeed:      protocol.SpeedForPower(req.Power),
		GamePhase:         r.game.Phase,
		BallStates:        r.game.BallStates(),
		SimulatorPlayerID: playerID,
		NextStateUpdateIn: 1000 / max(1, r.opts.StateUpdateHz),
	})
}

func (r *Room) handleStateFrame(playerID string, raw []byte, env protocol.Envelope) {
	req, err := protocol.Decode[protocol.StateFrameReq](raw)
	if err != nil {
		return // best-effort channel: never answer with an error
	}
	if err := r.game.ValidateStateFrame(playerID, req.ShotNumber, req.BallStates); err != nil {
		return
	}
	// Relayed, not committed: mid-flight frames are presentation only.
	r.sendExcept(playerID, &protocol.StateUpdateResp{
		Envelope:   protocol.Envelope{Type: protocol.TypeStateUpdate, PlayerID: playerID, Timestamp: req.Timestamp},
		GamePhase:  r.game.Phase,
		ShotNumber: req.ShotNumber,
		BallStates: req.BallStates,
	})
}

func (r *Room) handleShotResult(playerID string, raw []byte, env protocol.Envelope) {
	req, err := protocol.Decode[protocol.ShotResultReq](raw)
	if err != nil {
		log.Printf("[room %s] handleShotResult: 解析失败 %v", r.ID, err)
		r.fail(playerID, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	log.Printf("[room %s] 接收到 SHOT_RESULT shot#%d from %s firstContact=%d pocketed=%v outOfBounds=%v cueMoved=%v",
		r.ID, req.ShotNumber, playerID, req.FirstContactBall, req.PocketedBalls, req.OutOfBoundsBalls, req.CueBallMoved)
	rep := rules.ShotReport{
		ShooterID:           playerID,
		ShotNumber:          req.ShotNumber,
		FirstContactBall:    req.FirstContactBall,
		PocketedBalls:       req.PocketedBalls,
		OutOfBoundsBalls:    req.OutOfBoundsBalls,
		CueBallMoved:        req.CueBallMoved,
		CushionAfterContact: req.CushionAfterContact,
		FinalBalls:          req.BallStates,
	}
	if err := r.game.ValidateShotResult(playerID, rep); err != nil {
		log.Printf("[room %s] ValidateShotResult 失败: %v", r.ID, err)
		r.metrics.ShotRejected()
		r.failErr(playerID, env.MessageID, err)
		// Resync the cheater/buggy client to the authoritative state.
		r.sendTo(playerID, &protocol.SnapshotResp{
			Envelope:  protocol.Envelope{Type: protocol.TypeSnapshot, PlayerID: playerID},
			GameState: r.Snapshot(),
		})
		return
	}
	log.Printf("[room %s] ValidateShotResult 通过", r.ID)

	res, err := r.game.ApplyShotResult(rep)
	if err != nil {
		log.Printf("[room %s] ApplyShotResult 失败: %v", r.ID, err)
		r.failErr(playerID, env.MessageID, err)
		return
	}
	log.Printf("[room %s] ApplyShotResult 成功 gameStatus=%s", r.ID, res.GameStatus)
	r.publishStrikeResult(req.PocketedBalls, res)
}

// publishStrikeResult broadcasts BALLS_STOPPED and advances the room clocks.
func (r *Room) publishStrikeResult(pocketed []int, res *protocol.StrikeResult) {
	if res.FoulType != nil {
		r.metrics.Foul()
	}
	if pocketed == nil {
		pocketed = []int{}
	}
	r.shotDeadline = time.Time{}
	r.syncSummary()

	ballStates := r.game.BallStates()
	// 诊断日志：球位数据
	log.Printf("[room %s] BALLS_STOPPED: shot=%d, phase=%s, balls=%d", r.ID, r.game.ShotNumber, r.game.Phase, len(ballStates))
	for _, bs := range ballStates {
		if !bs.InPocket {
			log.Printf("  ball[%d]: pos=(%.4f, %.4f) vel=(%.4f, %.4f)", bs.BallID, bs.Position.X, bs.Position.Y, bs.Velocity.X, bs.Velocity.Y)
		} else {
			log.Printf("  ball[%d]: POCKETED", bs.BallID)
		}
	}

	r.broadcast(&protocol.BallsStoppedResp{
		Envelope:      protocol.Envelope{Type: protocol.TypeBallsStopped, PlayerID: res.StrikePlayerID},
		ShotNumber:    r.game.ShotNumber,
		BallStates:    ballStates,
		PocketedBalls: pocketed,
		StrikeResult:  *res,
		GamePhase:     r.game.Phase,
		Players:       r.game.PlayerInfos(),
		Score:         r.game.Score(),
	})

	if r.game.Finished() {
		r.publishGameOver(r.game.EndReason)
		return
	}
	r.turnDeadline = time.Now().Add(r.opts.TurnTimeout)
	r.broadcastTurnChange()
}

func (r *Room) handlePlacement(playerID string, raw []byte, env protocol.Envelope) {
	req, err := protocol.Decode[protocol.CueBallPlacementReq](raw)
	if err != nil {
		r.fail(playerID, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	if err := r.game.ValidatePlacement(playerID, req.Position); err != nil {
		r.failErr(playerID, env.MessageID, err)
		return
	}
	r.game.ApplyPlacement(req.Position)
	r.turnDeadline = time.Now().Add(r.opts.TurnTimeout)
	r.syncSummary()

	ack := &protocol.CueBallPlacementAckResp{
		Envelope:        protocol.Envelope{Type: protocol.TypeCueBallPlacementAck, PlayerID: playerID, MessageID: env.MessageID},
		Status:          "accepted",
		GamePhase:       r.game.Phase,
		CurrentPlayerID: r.game.CurrentTurn,
		BallStates:      r.game.BallStates(),
	}
	// Both clients need the confirmed cue ball position, not just the placer.
	r.broadcast(ack)
}

func (r *Room) handleConcede(playerID string, env protocol.Envelope) {
	if r.game.Finished() || r.status != protocol.RoomStatusInGame {
		r.fail(playerID, env.MessageID, protocol.ErrInvalidPhase, "当前无进行中的对局")
		return
	}
	res, err := r.game.Concede(playerID)
	if err != nil {
		r.failErr(playerID, env.MessageID, err)
		return
	}
	r.publishStrikeResult(nil, res)
}

func (r *Room) publishGameOver(reason string) {
	r.status = protocol.RoomStatusFinished
	r.turnDeadline = time.Time{}
	r.shotDeadline = time.Time{}
	r.metrics.GameFinished()
	r.syncSummary()

	var dur int64
	if !r.startedAt.IsZero() {
		dur = time.Since(r.startedAt).Milliseconds()
	}
	r.broadcast(&protocol.GameOverResp{
		Envelope:   protocol.Envelope{Type: protocol.TypeGameOver},
		GameStatus: r.game.GameStatus,
		WinnerID:   r.game.WinnerID,
		LoserID:    r.game.LoserID,
		Reason:     reason,
		Score:      r.game.Score(),
		DurationMs: dur,
		Players:    r.game.PlayerInfos(),
	})
	log.Printf("[room %s] game over: status=%s winner=%s reason=%s",
		r.ID, r.game.GameStatus, r.game.WinnerID, reason)

	// 局后留桌：不关房，回到 准备 -> 开始 的下一局等待态。
	r.returnToReady()
}

// returnToReady resets the finished game and drops the room back to the
// "both ready -> start" lobby, keeping the two seats occupied.
func (r *Room) returnToReady() {
	r.game.ResetForNextRound()
	r.status = protocol.RoomStatusWaiting
	if r.game.Seated() == 2 {
		r.status = protocol.RoomStatusReady
	}
	r.startedAt = time.Time{}
	r.expiresAt = time.Now().Add(r.opts.WaitingTTL)
	r.syncSummary()
	r.broadcastRoomState()
}

// ---------------------------------------------------------------------------
// timers
// ---------------------------------------------------------------------------

func (r *Room) isTurnPhase() bool {
	return r.game.Phase == protocol.PhaseAiming || r.game.Phase == protocol.PhaseBallInHand
}

func (r *Room) onTick(now time.Time) {
	// 1. reconnect deadlines
	for pid, deadline := range r.dcDeadline {
		if now.Before(deadline) {
			continue
		}
		delete(r.dcDeadline, pid)
		if r.status == protocol.RoomStatusInGame && !r.game.Finished() {
			log.Printf("[room %s] player %s failed to reconnect, forfeiting", r.ID, pid)
			r.forfeitSeat(pid, protocol.ReasonOpponentDisconnect)
		} else {
			r.game.RemovePlayer(pid)
			r.mgr.unbind(pid, r.ID)
			if r.game.Seated() == 0 && len(r.spectators) == 0 {
				r.closeRoom(protocol.ReasonRoomClosed)
				return
			}
			r.status = protocol.RoomStatusWaiting
			r.game.Phase = protocol.PhaseWaiting
			for _, pl := range r.game.Players {
				if pl != nil {
					pl.Ready = false
				}
			}
			r.expiresAt = now.Add(r.opts.WaitingTTL)
			r.syncSummary()
			r.broadcastRoomState()
		}
	}

	// 2. room TTL (waiting rooms nobody joins / finished rooms nobody reads)
	if !r.expiresAt.IsZero() && now.After(r.expiresAt) {
		switch r.status {
		case protocol.RoomStatusWaiting, protocol.RoomStatusReady, protocol.RoomStatusFinished:
			r.closeRoom(protocol.ReasonRoomClosed)
			return
		}
	}

	if r.status != protocol.RoomStatusInGame || r.game.Finished() {
		return
	}

	// 3. turn timeout - skipped while the player is disconnected (the reconnect
	//    window already governs that case).
	if r.isTurnPhase() && !r.turnDeadline.IsZero() && now.After(r.turnDeadline) {
		if _, dc := r.dcDeadline[r.game.CurrentTurn]; !dc {
			r.applyTimeout(r.game.CurrentTurn, protocol.FoulTurnTimeout)
			return
		}
	}

	// 4. the shooter never reported a settled result: roll the shot back.
	if r.game.Phase == protocol.PhaseMoving && !r.shotDeadline.IsZero() && now.After(r.shotDeadline) {
		if _, dc := r.dcDeadline[r.game.CurrentTurn]; !dc {
			r.applyTimeout(r.game.CurrentTurn, protocol.FoulShotTimeout)
		}
	}
}

func (r *Room) applyTimeout(playerID, foulCode string) {
	res, err := r.game.ApplyTurnTimeout(playerID, foulCode)
	if err != nil {
		log.Printf("[room %s] timeout handling failed: %v", r.ID, err)
		return
	}
	log.Printf("[room %s] %s for %s", r.ID, foulCode, playerID)
	r.publishStrikeResult(nil, res)
	// Balls were rolled back to their pre-shot state; force a resync.
	r.broadcast(&protocol.SnapshotResp{
		Envelope:  protocol.Envelope{Type: protocol.TypeSnapshot},
		GameState: r.Snapshot(),
	})
}
