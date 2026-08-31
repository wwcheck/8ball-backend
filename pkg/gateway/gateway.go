// Package gateway is the WebSocket front door. It owns identity/handshake,
// lobby-level messages (match, create/join room, reconnect, heartbeat) and the
// routing of in-game messages to the owning room goroutine.
//
// Layering:
//
//	transport  ->  gateway  ->  room (one goroutine per match)  ->  rules
//	(sockets)      (routing)     (authoritative state)             (arbitration)
//
// The gateway never touches game state directly; anything gameplay related is
// posted to the room's inbox so the room's single goroutine stays the only
// writer (see pkg/room/room.go).
package gateway

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/yourgame/8ball-backend/pkg/monitor"
	"github.com/yourgame/8ball-backend/pkg/protocol"
	"github.com/yourgame/8ball-backend/pkg/room"
	"github.com/yourgame/8ball-backend/pkg/transport"
)

// Config bundles the tunables the gateway advertises in WELCOME.
type Config struct {
	Transport transport.Config
	Room      room.Options

	// ClientHeartbeat is the PING cadence the client should use.
	// ARCHITECTURE.md §心跳机制 specifies 5s.
	ClientHeartbeat time.Duration
}

// DefaultConfig returns the documented defaults.
func DefaultConfig() Config {
	return Config{
		Transport:       transport.DefaultConfig(),
		Room:            room.DefaultOptions(),
		ClientHeartbeat: 5 * time.Second,
	}
}

// Gateway implements transport.MessageHandler.
type Gateway struct {
	cfg     Config
	hub     *transport.Hub
	rooms   *room.Manager
	metrics *monitor.Metrics
}

// New wires a gateway. The room manager's PairNotify hook is installed here so
// quick-matched players receive MATCH_FOUND before their room snapshot.
func New(cfg Config, hub *transport.Hub, rooms *room.Manager, m *monitor.Metrics) *Gateway {
	g := &Gateway{cfg: cfg, hub: hub, rooms: rooms, metrics: m}
	rooms.PairNotify = g.onPair
	return g
}

// ---------------------------------------------------------------------------
// HTTP handshake
// ---------------------------------------------------------------------------

// HandleWS upgrades an HTTP request to a WebSocket session and serves it until
// the socket dies. Identity is taken from the query string:
//
//	GET /ws?playerId=<id>&name=<display>&token=<sessionToken>
//
// playerId is optional (a guest id is minted when absent). token is required
// only when the player still holds a live room seat - that guards an in-progress
// match against seat theft. Real authentication (JWT from the account service)
// is Phase 3 work; see docs/PROTOCOL.md "待对齐点".
func (g *Gateway) HandleWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	playerID := strings.TrimSpace(q.Get("playerId"))
	name := strings.TrimSpace(q.Get("name"))
	avatar := strings.TrimSpace(q.Get("avatar"))
	token := strings.TrimSpace(q.Get("token"))

	// Sanity limits: the id/name end up as map keys and in every log line, so
	// bound them before anything else. A longer id is treated as absent.
	if len(playerID) > 64 {
		http.Error(w, `{"errorCode":"BAD_REQUEST","message":"playerId too long (max 64)"}`,
			http.StatusBadRequest)
		return
	}
	if len(name) > 64 {
		name = name[:64]
	}
	if len(avatar) > 512 {
		avatar = avatar[:512]
	}
	if playerID == "" {
		playerID = "guest_" + transport.NewToken()[:10]
	}
	if name == "" {
		name = playerID
	}

	// A player with a live seat must prove they are the same client.
	resumable := g.rooms.ResumableRoomID(playerID)
	if resumable != "" && !g.hub.ValidToken(playerID, token) {
		http.Error(w, `{"errorCode":"UNAUTHORIZED","message":"sessionToken required to resume this player"}`,
			http.StatusUnauthorized)
		log.Printf("[ws] rejected resume attempt for %s (bad token)", playerID)
		return
	}

	// Reuse the remembered token so a RECONNECT with the same token still
	// validates; otherwise mint a fresh one.
	if !g.hub.ValidToken(playerID, token) {
		token = transport.NewToken()
	}

	conn, err := transport.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[ws] upgrade failed for %s: %v", playerID, err)
		return
	}

	s := transport.NewSession(playerID, name, avatar, token, conn, g.cfg.Transport, g.hub)
	g.hub.Register(s)
	g.metrics.ConnectionOpened()
	log.Printf("[ws] %s connected from %s (resumable=%q)", playerID, r.RemoteAddr, resumable)

	g.welcome(s, "")
	s.Serve(g) // blocks; calls OnMessage / OnDisconnect
}

// ---------------------------------------------------------------------------
// send helpers
// ---------------------------------------------------------------------------

func (g *Gateway) send(s *transport.Session, msg protocol.Message) {
	h := msg.Head()
	if h.PlayerID == "" {
		h.PlayerID = s.PlayerID()
	}
	if h.RoomID == "" {
		h.RoomID = s.RoomID()
	}
	h.ServerTime = time.Now().UnixMilli()
	if h.Timestamp == 0 {
		h.Timestamp = h.ServerTime
	}
	s.Send(msg)
	g.metrics.MessageOut()
}

// push is send() for a room.Client (used when the target is the *other* player).
func (g *Gateway) push(c room.Client, msg protocol.Message, roomID string) {
	h := msg.Head()
	if h.PlayerID == "" {
		h.PlayerID = c.PlayerID()
	}
	h.RoomID = roomID
	h.ServerTime = time.Now().UnixMilli()
	if h.Timestamp == 0 {
		h.Timestamp = h.ServerTime
	}
	c.Send(msg)
	g.metrics.MessageOut()
}

func (g *Gateway) fail(s *transport.Session, messageID, code, message string) {
	e := protocol.NewError(code, message)
	e.MessageID = messageID
	g.metrics.ErrorSent()
	g.send(s, e)
}

func (g *Gateway) failErr(s *transport.Session, messageID string, err error) {
	if ge, ok := err.(*protocol.GameError); ok {
		g.fail(s, messageID, ge.Code, ge.Message)
		return
	}
	g.fail(s, messageID, protocol.ErrInternal, err.Error())
}

func (g *Gateway) welcome(s *transport.Session, messageID string) {
	g.send(s, &protocol.WelcomeResp{
		Envelope:          protocol.Envelope{Type: protocol.TypeWelcome, MessageID: messageID},
		SessionToken:      s.Token(),
		ProtocolVersion:   protocol.ProtocolVersion,
		PlayerNickname:    s.Name(),
		PlayerAvatar:      s.Avatar(),
		HeartbeatInterval: int(g.cfg.ClientHeartbeat.Milliseconds()),
		ReadTimeout:       int(g.cfg.Transport.ReadTimeout.Milliseconds()),
		ReconnectWindow:   int(g.cfg.Room.ReconnectWindow.Milliseconds()),
		StateUpdateHz:     g.cfg.Room.StateUpdateHz,
		Table:             protocol.DefaultTableInfo(),
		ResumableRoomID:   g.rooms.ResumableRoomID(s.PlayerID()),
	})
}

// ---------------------------------------------------------------------------
// transport.MessageHandler
// ---------------------------------------------------------------------------

// OnMessage dispatches one inbound frame. Called serially per session.
func (g *Gateway) OnMessage(s *transport.Session, env protocol.Envelope, raw []byte) {
	g.metrics.MessageIn()

	switch env.Type {
	case protocol.TypePing:
		g.send(s, &protocol.PongResp{Envelope: protocol.Envelope{
			Type:       protocol.TypePong,
			MessageID:  env.MessageID,
			ClientTime: pickClientTime(env),
		}})

	case protocol.TypeHello:
		if req, err := protocol.Decode[protocol.HelloReq](raw); err == nil {
			s.SetName(req.Name)
			s.SetAvatar(req.Avatar)
		}
		g.welcome(s, env.MessageID)

	case protocol.TypeQuickMatch:
		g.handleQuickMatch(s, env, raw)

	case protocol.TypeCancelMatch:
		if g.rooms.CancelMatch(s.PlayerID()) {
			g.send(s, &protocol.MatchCancelledResp{
				Envelope: protocol.Envelope{Type: protocol.TypeMatchCancelled, MessageID: env.MessageID},
				Reason:   "cancelled_by_player",
			})
		} else {
			g.fail(s, env.MessageID, protocol.ErrNotQueued, "你当前不在匹配队列中")
		}

	case protocol.TypeCreateRoom:
		g.handleCreateRoom(s, env, raw)

	case protocol.TypeJoinRoom:
		g.handleJoinRoom(s, env, raw)

	case protocol.TypeReconnect:
		g.handleReconnect(s, env, raw)

	case protocol.TypeDisconnect:
		// Graceful goodbye. The reconnect window still applies: a client that
		// backgrounds the app should be able to come back to its seat.
		s.Close("client_disconnect")

	case protocol.TypeReady,
		protocol.TypeLeaveRoom,
		protocol.TypeRequestSnapshot,
		protocol.TypeJoinGame,
		protocol.TypeStandUp,
		protocol.TypeShoot,
		protocol.TypeStateFrame,
		protocol.TypeShotResult,
		protocol.TypeCueBallPlacement,
		protocol.TypeConcede:
		g.routeToRoom(s, env, raw)

	default:
		g.fail(s, env.MessageID, protocol.ErrUnknownType, "未知消息类型: "+env.Type)
	}
}

// OnDisconnect starts the reconnect window for the player's room, if any.
func (g *Gateway) OnDisconnect(s *transport.Session) {
	pid := s.PlayerID()
	g.metrics.ConnectionClosed()

	// If a newer session already took over this player id, this teardown belongs
	// to the displaced socket and must not disturb the live one. The stale
	// socket may still own the player's quick-match queue entry (queue entries
	// are keyed by player id but hold the client that queued), so drop it too -
	// otherwise the replacement session gets MATCH_ALREADY_QUEUED until it
	// explicitly cancels.
	if cur, ok := g.hub.Session(pid); ok && cur != s {
		g.rooms.CancelMatchIf(s)
		log.Printf("[ws] %s: stale session closed (%s), superseded", pid, s.CloseReason())
		return
	}
	g.hub.Unregister(s)
	g.rooms.CancelMatch(pid)

	if r, ok := g.rooms.RoomOf(pid); ok {
		r.PostDisconnect(pid)
	}
	log.Printf("[ws] %s disconnected (%s)", pid, s.CloseReason())
}

// ---------------------------------------------------------------------------
// lobby handlers
// ---------------------------------------------------------------------------

func (g *Gateway) handleQuickMatch(s *transport.Session, env protocol.Envelope, raw []byte) {
	if req, err := protocol.Decode[protocol.QuickMatchReq](raw); err == nil {
		s.SetName(req.Name)
	}
	out, err := g.rooms.QuickMatch(s)
	if err != nil {
		g.failErr(s, env.MessageID, err)
		return
	}
	if out.Queued {
		g.send(s, &protocol.MatchQueuedResp{
			Envelope:      protocol.Envelope{Type: protocol.TypeMatchQueued, MessageID: env.MessageID},
			QueuePosition: out.QueuePosition,
			QueueSize:     out.QueueSize,
			EstimatedWait: 0, // no historical data yet; Phase 3
		})
	}
	// The paired case is announced by onPair (MATCH_FOUND) and by the room
	// itself (ROOM_JOINED + snapshot), so there is nothing more to do here.
}

// onPair is room.Manager.PairNotify: tell both players who they are facing.
func (g *Gateway) onPair(waiter room.Client, waiterSeat int, caller room.Client, callerSeat int, r *room.Room) {
	g.push(waiter, &protocol.MatchFoundResp{
		Envelope: protocol.Envelope{Type: protocol.TypeMatchFound, PlayerID: waiter.PlayerID()},
		Opponent: protocol.PlayerInfo{PlayerID: caller.PlayerID(), Name: caller.Name(), Avatar: caller.Avatar(), Role: protocol.RoleSeated, Position: callerSeat},
		YourSeat: waiterSeat,
	}, r.ID)

	g.push(caller, &protocol.MatchFoundResp{
		Envelope: protocol.Envelope{Type: protocol.TypeMatchFound, PlayerID: caller.PlayerID()},
		Opponent: protocol.PlayerInfo{PlayerID: waiter.PlayerID(), Name: waiter.Name(), Avatar: waiter.Avatar(), Role: protocol.RoleSeated, Position: waiterSeat},
		YourSeat: callerSeat,
	}, r.ID)
}

func (g *Gateway) handleCreateRoom(s *transport.Session, env protocol.Envelope, raw []byte) {
	req, err := protocol.Decode[protocol.CreateRoomReq](raw)
	if err != nil {
		g.fail(s, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	s.SetName(req.Name)

	r, seat, err := g.rooms.CreateRoom(s, req.IsPublic)
	if err != nil {
		g.failErr(s, env.MessageID, err)
		return
	}
	// ROOM_JOINED (with the snapshot) was already emitted by the room; this adds
	// the invite code the owner needs to share. The creator is a spectator by
	// default and must send JOIN_GAME to grab a seat.
	g.send(s, &protocol.RoomCreatedResp{
		Envelope:   protocol.Envelope{Type: protocol.TypeRoomCreated, MessageID: env.MessageID, RoomID: r.ID},
		InviteCode: r.InviteCode,
		YourSeat:   seat,
		Role:       protocol.RoleSpectator,
	})
}

func (g *Gateway) handleJoinRoom(s *transport.Session, env protocol.Envelope, raw []byte) {
	req, err := protocol.Decode[protocol.JoinRoomReq](raw)
	if err != nil {
		g.fail(s, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	s.SetName(req.Name)

	if env.RoomID == "" && req.InviteCode == "" {
		g.fail(s, env.MessageID, protocol.ErrBadRequest, "需要提供 roomId 或 inviteCode")
		return
	}
	// The room emits ROOM_JOINED / SNAPSHOT itself, so nothing to send here.
	if _, _, _, err := g.rooms.JoinRoom(s, env.RoomID, req.InviteCode); err != nil {
		g.failErr(s, env.MessageID, err)
	}
}

func (g *Gateway) handleReconnect(s *transport.Session, env protocol.Envelope, raw []byte) {
	req, err := protocol.Decode[protocol.ReconnectReq](raw)
	if err != nil {
		g.fail(s, env.MessageID, protocol.ErrBadRequest, err.Error())
		return
	}
	if !g.hub.ValidToken(s.PlayerID(), req.SessionToken) {
		e := protocol.NewError(protocol.ErrSessionInvalid, "sessionToken 无效，请重新登录")
		e.MessageID = env.MessageID
		e.Fatal = true
		g.metrics.ErrorSent()
		g.send(s, e)
		return
	}
	roomID := g.rooms.ResumableRoomID(s.PlayerID())
	if roomID == "" {
		g.fail(s, env.MessageID, protocol.ErrNothingToResume, "没有可恢复的对局")
		return
	}
	// JoinRoom resumes the existing seat and makes the room push a full SNAPSHOT
	// (resumed=true), which is all the client needs to continue the match.
	if _, _, _, err := g.rooms.JoinRoom(s, roomID, ""); err != nil {
		g.failErr(s, env.MessageID, err)
	}
}

// routeToRoom forwards a gameplay frame to the owning room goroutine.
func (g *Gateway) routeToRoom(s *transport.Session, env protocol.Envelope, raw []byte) {
	pid := s.PlayerID()

	var r *room.Room
	if id := s.RoomID(); id != "" {
		r, _ = g.rooms.Room(id)
	}
	if r == nil {
		r, _ = g.rooms.RoomOf(pid)
	}
	if r == nil {
		if env.Type == protocol.TypeStateFrame {
			return // best-effort channel: stay silent
		}
		g.fail(s, env.MessageID, protocol.ErrNotInRoom, "你当前不在任何房间中")
		return
	}
	r.PostMessage(pid, env, raw)
}

// pickClientTime prefers the explicit clientTime field, falling back to the
// sender timestamp so RTT still works for minimal clients.
func pickClientTime(env protocol.Envelope) int64 {
	if env.ClientTime != 0 {
		return env.ClientTime
	}
	return env.Timestamp
}
