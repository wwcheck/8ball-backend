// Package transport owns the WebSocket plumbing: connection lifecycle, framing,
// heartbeat, rate limiting and message fan-out. It knows nothing about 8-ball
// rules; it only routes protocol envelopes to a MessageHandler.
package transport

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// Config tunes the transport layer. Defaults follow ARCHITECTURE.md §心跳机制
// (5s client heartbeat, 30s server timeout) and MULTIPLAYER_TECH.md §2.1.
type Config struct {
	ReadTimeout    time.Duration // no traffic for this long -> drop
	WriteTimeout   time.Duration // single frame write budget
	PingInterval   time.Duration // server-initiated ws ping cadence
	MaxMessageSize int64         // inbound frame cap
	SendBuffer     int           // per-session outbound queue depth
	MaxMsgPerSec   int           // inbound rate limit (0 = unlimited)
}

// DefaultConfig returns production-sane transport settings.
func DefaultConfig() Config {
	return Config{
		ReadTimeout:    60 * time.Second,
		WriteTimeout:   10 * time.Second,
		PingInterval:   25 * time.Second,
		MaxMessageSize: 64 * 1024,
		SendBuffer:     256,
		MaxMsgPerSec:   80,
	}
}

// MessageHandler consumes decoded envelopes from a session.
type MessageHandler interface {
	// OnMessage is called for every inbound frame, serially per session.
	OnMessage(s *Session, env protocol.Envelope, raw []byte)
	// OnDisconnect is called exactly once per session after the read loop ends.
	OnDisconnect(s *Session)
}

// Upgrader is the shared gorilla upgrader. CheckOrigin is permissive because
// Unity clients do not send a browser Origin header.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// Session is one live WebSocket connection belonging to one player.
type Session struct {
	playerID string
	token    string

	// nameMu guards name and avatar: SetName/SetAvatar run on this session's
	// read-pump goroutine (via OnMessage handlers), while Name/Avatar are read
	// from other goroutines (the room loop in onJoin, the opponent's pairing
	// path in onPair).
	nameMu sync.RWMutex
	name   string
	avatar string

	conn *websocket.Conn
	cfg  Config
	hub  *Hub

	out       chan []byte
	done      chan struct{} // closed exactly once; terminates writePump
	closeOnce sync.Once
	closed    atomic.Bool
	closeMsg  atomic.Value // string

	roomID atomic.Value // string
	lastRX atomic.Int64 // unix ms of last inbound frame

	rateWindow atomic.Int64 // unix second
	rateCount  atomic.Int64

	// writeMu serialises every conn.WriteMessage call. gorilla/websocket
	// forbids concurrent writers, and writePump + Close both write to the
	// connection, so both paths must hold this lock.
	writeMu sync.Mutex
}

// NewSession wires a fresh session around an upgraded connection.
func NewSession(playerID, name, avatar, token string, conn *websocket.Conn, cfg Config, hub *Hub) *Session {
	s := &Session{
		playerID: playerID,
		token:    token,
		conn:     conn,
		cfg:      cfg,
		hub:      hub,
		out:      make(chan []byte, cfg.SendBuffer),
		done:     make(chan struct{}),
	}
	s.nameMu.Lock()
	s.name = name
	s.avatar = avatar
	s.nameMu.Unlock()
	s.roomID.Store("")
	s.closeMsg.Store("")
	s.lastRX.Store(time.Now().UnixMilli())
	conn.SetReadLimit(cfg.MaxMessageSize)
	return s
}

// PlayerID returns the stable player identifier of this session.
func (s *Session) PlayerID() string { return s.playerID }

// Name returns the display name.
func (s *Session) Name() string {
	s.nameMu.RLock()
	defer s.nameMu.RUnlock()
	return s.name
}

// SetName updates the display name (e.g. after HELLO). Bounded to keep log
// lines and wire payloads sane.
func (s *Session) SetName(n string) {
	if n == "" {
		return
	}
	if len(n) > 64 {
		n = n[:64]
	}
	s.nameMu.Lock()
	s.name = n
	s.nameMu.Unlock()
}

// Avatar returns the player's avatar URL or identifier.
func (s *Session) Avatar() string {
	s.nameMu.RLock()
	defer s.nameMu.RUnlock()
	return s.avatar
}

// SetAvatar updates the avatar (e.g. after HELLO). Bounded to keep wire
// payloads sane.
func (s *Session) SetAvatar(a string) {
	if a == "" {
		return
	}
	if len(a) > 512 {
		a = a[:512]
	}
	s.nameMu.Lock()
	s.avatar = a
	s.nameMu.Unlock()
}

// Token returns the reconnect session token issued in WELCOME.
func (s *Session) Token() string { return s.token }

// RoomID returns the room this session is currently bound to ("" if none).
func (s *Session) RoomID() string {
	v, _ := s.roomID.Load().(string)
	return v
}

// SetRoomID binds/unbinds the session to a room.
func (s *Session) SetRoomID(id string) { s.roomID.Store(id) }

// Closed reports whether the session has been torn down.
func (s *Session) Closed() bool { return s.closed.Load() }

// Send marshals v and enqueues it. It never blocks: if the outbound queue is
// full the session is considered too slow and is closed.
//
// s.out is deliberately NEVER closed: a concurrent Send that already passed the
// closed-check would panic on a closed channel. writePump exits via s.done
// instead, and stale buffered frames are simply dropped with the connection.
func (s *Session) Send(v any) {
	if s.closed.Load() {
		return
	}
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[ws] marshal outbound for %s: %v", s.playerID, err)
		return
	}
	select {
	case s.out <- data:
	default:
		log.Printf("[ws] send buffer full for %s, closing session", s.playerID)
		s.Close("send_buffer_overflow")
	}
}

// SendError is a shorthand for pushing an ERROR frame.
func (s *Session) SendError(messageID, code, message string) {
	e := protocol.NewError(code, message)
	e.MessageID = messageID
	e.PlayerID = s.playerID
	e.RoomID = s.RoomID()
	e.ServerTime = time.Now().UnixMilli()
	s.Send(e)
}

// Close tears the session down once, recording a reason for logging.
//
// The close frame goes through WriteControl, which gorilla/websocket documents
// as safe to call concurrently with all other methods - so it cannot race with
// writePump. s.out is never closed (see Send); writePump terminates on s.done.
func (s *Session) Close(reason string) {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		s.closeMsg.Store(reason)
		_ = s.conn.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason),
			time.Now().Add(time.Second))
		_ = s.conn.Close()
		close(s.done)
	})
}

// CloseReason returns why the session ended.
func (s *Session) CloseReason() string {
	v, _ := s.closeMsg.Load().(string)
	return v
}

// Serve runs the read and write pumps until the connection dies. It blocks and
// is expected to be called from the HTTP handler goroutine.
func (s *Session) Serve(h MessageHandler) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.writePump()
	}()

	s.readPump(h)
	s.Close("read_loop_ended")
	wg.Wait()
	h.OnDisconnect(s)
}

func (s *Session) readPump(h MessageHandler) {
	_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	s.conn.SetPongHandler(func(string) error {
		s.lastRX.Store(time.Now().UnixMilli())
		return s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))
	})

	for {
		msgType, raw, err := s.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("[ws] %s read error: %v", s.playerID, err)
			}
			return
		}
		if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
			continue
		}

		s.lastRX.Store(time.Now().UnixMilli())
		_ = s.conn.SetReadDeadline(time.Now().Add(s.cfg.ReadTimeout))

		if !s.allowRate() {
			s.SendError("", protocol.ErrRateLimited, "消息发送过于频繁")
			continue
		}

		env, err := protocol.ParseEnvelope(raw)
		if err != nil {
			s.SendError("", protocol.ErrBadRequest, err.Error())
			continue
		}
		// The server never trusts a client-declared identity.
		env.PlayerID = s.playerID
		h.OnMessage(s, env, raw)
	}
}

func (s *Session) writePump() {
	ticker := time.NewTicker(s.cfg.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.done:
			return
		case data, ok := <-s.out:
			if !ok {
				return
			}
			s.writeMu.Lock()
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
			err := s.conn.WriteMessage(websocket.TextMessage, data)
			s.writeMu.Unlock()
			if err != nil {
				log.Printf("[ws] %s write error: %v", s.playerID, err)
				// The socket is unusable; tear the whole session down so the
				// read loop does not linger until the read deadline.
				s.Close("write_failed")
				return
			}
		case <-ticker.C:
			s.writeMu.Lock()
			_ = s.conn.SetWriteDeadline(time.Now().Add(s.cfg.WriteTimeout))
			err := s.conn.WriteMessage(websocket.PingMessage, nil)
			s.writeMu.Unlock()
			if err != nil {
				s.Close("ping_failed")
				return
			}
		}
	}
}

// allowRate implements a coarse per-second inbound message cap.
func (s *Session) allowRate() bool {
	if s.cfg.MaxMsgPerSec <= 0 {
		return true
	}
	now := time.Now().Unix()
	if s.rateWindow.Load() != now {
		s.rateWindow.Store(now)
		s.rateCount.Store(0)
	}
	return s.rateCount.Add(1) <= int64(s.cfg.MaxMsgPerSec)
}
