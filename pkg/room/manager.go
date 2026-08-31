package room

import (
	"crypto/rand"
	"log"
	"strings"
	"sync"

	"github.com/yourgame/8ball-backend/pkg/monitor"
	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// inviteAlphabet excludes 0/O/1/I/S/5/2/Z/8/B so invite codes can be read out
// loud without ambiguity.
const inviteAlphabet = "ACDEFGHJKLMNPQRTUVWXY34679"

const (
	inviteCodeLen = 6
	roomIDLen     = 12
)

// fixedRoomID is the well-known room number the client hard-codes ("1000").
// It is a fixed *room number*, not a fixed *room instance*: the room may TTL
// close when empty and be lazily recreated the next time someone joins "1000".
const fixedRoomID = "1000"

// Manager is the process-wide room registry plus the quick-match queue.
//
// Locking: Manager.mu protects the maps and the queue only. Room.join blocks on
// the room's goroutine, so it is ALWAYS called with the lock released - holding
// mu across a join would let a busy room stall every other lobby operation.
type Manager struct {
	opts    Options
	metrics *monitor.Metrics

	// PairNotify, when set, is invoked by QuickMatch after the waiting player
	// has been seated but before the caller is, so the gateway can emit
	// MATCH_FOUND before the caller's ROOM_JOINED snapshot arrives. Seats are
	// already final at that point. Optional.
	PairNotify func(waiter Client, waiterSeat int, caller Client, callerSeat int, r *Room)

	mu      sync.RWMutex
	rooms   map[string]*Room       // roomID -> room
	invites map[string]string      // inviteCode -> roomID
	bound   map[string]string      // playerID -> roomID (survives disconnects)
	queue   []*queueEntry          // FIFO quick-match queue
	queued  map[string]*queueEntry // playerID -> queue entry
}

type queueEntry struct {
	client Client
}

// MatchOutcome is the result of a QUICK_MATCH request: either the player was
// queued, or a pairing happened and both seats are already joined.
type MatchOutcome struct {
	Queued        bool
	QueuePosition int
	QueueSize     int

	Room         *Room
	You          Client
	YourSeat     int
	Opponent     Client
	OpponentSeat int
}

// NewManager creates an empty room manager.
func NewManager(opts Options, m *monitor.Metrics) *Manager {
	if m == nil {
		m = monitor.New()
	}
	return &Manager{
		opts:    opts,
		metrics: m,
		rooms:   make(map[string]*Room),
		invites: make(map[string]string),
		bound:   make(map[string]string),
		queued:  make(map[string]*queueEntry),
	}
}

// ---------------------------------------------------------------------------
// lookups
// ---------------------------------------------------------------------------

// Room returns a room by id.
func (m *Manager) Room(id string) (*Room, bool) {
	m.mu.RLock()
	r, ok := m.rooms[id]
	m.mu.RUnlock()
	return r, ok
}

// RoomOf returns the room a player currently belongs to. The binding survives a
// disconnect, which is exactly what makes reconnect possible.
func (m *Manager) RoomOf(playerID string) (*Room, bool) {
	m.mu.RLock()
	id := m.bound[playerID]
	r, ok := m.rooms[id]
	m.mu.RUnlock()
	return r, ok
}

// ResumableRoomID returns the room id a returning player can resume ("" if none).
func (m *Manager) ResumableRoomID(playerID string) string {
	m.mu.RLock()
	id := m.bound[playerID]
	_, alive := m.rooms[id]
	m.mu.RUnlock()
	if !alive {
		return ""
	}
	return id
}

// Count returns the number of live rooms.
func (m *Manager) Count() int {
	m.mu.RLock()
	n := len(m.rooms)
	m.mu.RUnlock()
	return n
}

// QueueSize returns the current quick-match queue depth.
func (m *Manager) QueueSize() int {
	m.mu.RLock()
	n := len(m.queue)
	m.mu.RUnlock()
	return n
}

// Summaries lists every live room (used by GET /rooms).
func (m *Manager) Summaries() []Summary {
	m.mu.RLock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.RUnlock()

	out := make([]Summary, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, r.Summary())
	}
	return out
}

// ---------------------------------------------------------------------------
// create / join
// ---------------------------------------------------------------------------

// CreateRoom builds a private room. The creator enters as a spectator by
// default ("统一进房都观众") and must send JOIN_GAME to grab a seat.
func (m *Manager) CreateRoom(c Client, isPublic bool) (*Room, int, error) {
	pid := c.PlayerID()

	m.mu.Lock()
	if rid, ok := m.bound[pid]; ok {
		m.mu.Unlock()
		return nil, 0, protocol.Errf(protocol.ErrAlreadyInRoom, "你已在房间 %s 中，请先退出", rid)
	}
	m.dequeueLocked(pid)
	r := m.newRoomLocked(m.freshInviteLocked(), isPublic)
	m.mu.Unlock()

	res := r.join(c, joinAuto)
	if res.Err != nil {
		r.Shutdown(protocol.ReasonRoomClosed)
		return nil, 0, res.Err
	}
	log.Printf("[mgr] room %s created by %s (invite=%s public=%v)", r.ID, pid, r.InviteCode, isPublic)
	return r, res.Seat, nil
}

// JoinRoom joins (or resumes) a player in an existing room. Exactly one of
// roomID / inviteCode needs to be supplied; roomID wins when both are present.
//
// The fixed room number "1000" is special-cased: if it does not exist yet, it is
// lazily created so clients that hard-code roomId:"1000" can enter without an
// invite code. The fixed number stays stable across room close/recreate.
func (m *Manager) JoinRoom(c Client, roomID, inviteCode string) (*Room, int, bool, error) {
	pid := c.PlayerID()
	code := strings.ToUpper(strings.TrimSpace(inviteCode))

	m.mu.RLock()
	var r *Room
	switch {
	case roomID != "":
		r = m.rooms[roomID]
	case code != "":
		if rid, ok := m.invites[code]; ok {
			r = m.rooms[rid]
		}
	}
	bound := m.bound[pid]
	m.mu.RUnlock()

	if r == nil && roomID == fixedRoomID {
		// 懒创建固定房间号 "1000"：固定的是房间号，不是房间实例。
		m.mu.Lock()
		r = m.rooms[fixedRoomID]
		if r == nil {
			if b := m.bound[pid]; b != "" && b != fixedRoomID {
				m.mu.Unlock()
				return nil, 0, false, protocol.Errf(protocol.ErrAlreadyInRoom, "你已在房间 %s 中，请先退出", b)
			}
			r = m.newRoomWithIDLocked(fixedRoomID, "", false)
			log.Printf("[mgr] lazily created fixed room %s", fixedRoomID)
		}
		bound = m.bound[pid]
		m.mu.Unlock()
	}

	if r == nil {
		if roomID == "" && code != "" {
			return nil, 0, false, protocol.Errf(protocol.ErrInvalidInvite, "邀请码 %s 无效或房间已关闭", code)
		}
		return nil, 0, false, protocol.Errf(protocol.ErrRoomNotFound, "房间不存在或已关闭")
	}
	if bound != "" && bound != r.ID {
		return nil, 0, false, protocol.Errf(protocol.ErrAlreadyInRoom, "你已在房间 %s 中，请先退出", bound)
	}

	m.mu.Lock()
	m.dequeueLocked(pid)
	m.mu.Unlock()

	res := r.join(c, joinAuto)
	if res.Err != nil {
		return nil, 0, false, res.Err
	}
	return r, res.Seat, res.Resumed, nil
}

// ---------------------------------------------------------------------------
// quick match
// ---------------------------------------------------------------------------

// QuickMatch pairs the caller with the longest-waiting opponent, or enqueues
// them. On a successful pairing BOTH players are already seated in the returned
// room, so the caller only has to emit MATCH_FOUND.
func (m *Manager) QuickMatch(c Client) (*MatchOutcome, error) {
	pid := c.PlayerID()

	for {
		m.mu.Lock()
		if rid, ok := m.bound[pid]; ok {
			m.mu.Unlock()
			return nil, protocol.Errf(protocol.ErrAlreadyInRoom, "你已在房间 %s 中，请先退出", rid)
		}
		if _, ok := m.queued[pid]; ok {
			m.mu.Unlock()
			return nil, protocol.Errf(protocol.ErrAlreadyQueued, "你已在匹配队列中")
		}

		opp := m.popCandidateLocked()
		if opp == nil {
			e := &queueEntry{client: c}
			m.queue = append(m.queue, e)
			m.queued[pid] = e
			size := len(m.queue)
			m.metrics.SetQueueSize(size)
			m.mu.Unlock()
			log.Printf("[mgr] %s queued for quick match (queue=%d)", pid, size)
			return &MatchOutcome{Queued: true, QueuePosition: size, QueueSize: size}, nil
		}

		// A matched room needs no invite code and is never listed publicly.
		r := m.newRoomLocked("", false)
		m.metrics.SetQueueSize(len(m.queue))
		m.mu.Unlock()

		// Seat the waiting player first so they take seat 1 (= the breaker).
		oppRes := r.join(opp.client, joinSeated)
		if oppRes.Err != nil {
			log.Printf("[mgr] quick match: opponent %s could not be seated (%v), retrying",
				opp.client.PlayerID(), oppRes.Err)
			r.Shutdown(protocol.ReasonRoomClosed)
			continue
		}

		// Seat 2 is the only one left, so the caller's seat is already known.
		callerSeat := 3 - oppRes.Seat
		if m.PairNotify != nil {
			m.PairNotify(opp.client, oppRes.Seat, c, callerSeat, r)
		}

		selfRes := r.join(c, joinSeated)
		if selfRes.Err != nil {
			// Give the opponent their queue slot back rather than punishing them.
			r.Shutdown(protocol.ReasonRoomClosed)
			return nil, selfRes.Err
		}

		m.metrics.MatchMade()
		log.Printf("[mgr] matched %s (seat %d) vs %s (seat %d) in room %s",
			opp.client.PlayerID(), oppRes.Seat, pid, selfRes.Seat, r.ID)
		return &MatchOutcome{
			Room:         r,
			You:          c,
			YourSeat:     selfRes.Seat,
			Opponent:     opp.client,
			OpponentSeat: oppRes.Seat,
		}, nil
	}
}

// CancelMatch removes a player from the queue. Reports whether they were queued.
func (m *Manager) CancelMatch(playerID string) bool {
	m.mu.Lock()
	ok := m.dequeueLocked(playerID)
	m.metrics.SetQueueSize(len(m.queue))
	m.mu.Unlock()
	return ok
}

// CancelMatchIf removes the player's queue entry only if it still refers to
// exactly the given client. A session that was displaced by a newer one for the
// same player id uses this for cleanup, so a stale socket's teardown can never
// drop the replacement session's fresh queue entry.
func (m *Manager) CancelMatchIf(c Client) bool {
	if c == nil {
		return false
	}
	m.mu.Lock()
	e, ok := m.queued[c.PlayerID()]
	if !ok || e.client != c {
		m.mu.Unlock()
		return false
	}
	m.dequeueLocked(c.PlayerID())
	m.metrics.SetQueueSize(len(m.queue))
	m.mu.Unlock()
	return true
}

// popCandidateLocked pops the first still-usable queue entry.
func (m *Manager) popCandidateLocked() *queueEntry {
	for len(m.queue) > 0 {
		e := m.queue[0]
		m.queue = m.queue[1:]
		pid := e.client.PlayerID()
		delete(m.queued, pid)

		if e.client.Closed() {
			continue // player vanished while waiting
		}
		if _, busy := m.bound[pid]; busy {
			continue // player joined a room by other means
		}
		return e
	}
	return nil
}

// dequeueLocked drops a player's queue entry if present.
func (m *Manager) dequeueLocked(playerID string) bool {
	e, ok := m.queued[playerID]
	if !ok {
		return false
	}
	delete(m.queued, playerID)
	for i, q := range m.queue {
		if q == e {
			m.queue = append(m.queue[:i], m.queue[i+1:]...)
			break
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// room bookkeeping (called from room goroutines)
// ---------------------------------------------------------------------------

// newRoomLocked registers and starts a room with a freshly generated id.
// Caller holds mu.
func (m *Manager) newRoomLocked(invite string, isPublic bool) *Room {
	return m.newRoomWithIDLocked(m.freshRoomIDLocked(), invite, isPublic)
}

// newRoomWithIDLocked registers and starts a room with a caller-chosen id
// (used by the fixed room number "1000"). Caller holds mu.
func (m *Manager) newRoomWithIDLocked(id, invite string, isPublic bool) *Room {
	r := newRoom(id, invite, isPublic, m.opts, m, m.metrics)
	m.rooms[id] = r
	if invite != "" {
		m.invites[invite] = id
	}
	m.metrics.RoomCreated()
	return r
}

// bind records that a player belongs to a room.
func (m *Manager) bind(playerID, roomID string) {
	m.mu.Lock()
	m.bound[playerID] = roomID
	m.dequeueLocked(playerID)
	m.metrics.SetQueueSize(len(m.queue))
	m.mu.Unlock()
}

// unbind clears a player's room binding (only if it still points at roomID).
func (m *Manager) unbind(playerID, roomID string) {
	m.mu.Lock()
	if cur, ok := m.bound[playerID]; ok && cur == roomID {
		delete(m.bound, playerID)
	}
	m.mu.Unlock()
}

// remove deletes a torn-down room and sweeps any stale bindings pointing at it.
func (m *Manager) remove(roomID, inviteCode string) {
	m.mu.Lock()
	delete(m.rooms, roomID)
	if inviteCode != "" {
		if rid, ok := m.invites[inviteCode]; ok && rid == roomID {
			delete(m.invites, inviteCode)
		}
	}
	for pid, rid := range m.bound {
		if rid == roomID {
			delete(m.bound, pid)
		}
	}
	m.mu.Unlock()
}

// Shutdown closes every room (graceful server shutdown).
func (m *Manager) Shutdown(reason string) {
	m.mu.Lock()
	rooms := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.queue = nil
	m.queued = make(map[string]*queueEntry)
	m.metrics.SetQueueSize(0)
	m.mu.Unlock()

	for _, r := range rooms {
		r.Shutdown(reason)
	}
}

// ---------------------------------------------------------------------------
// id generation
// ---------------------------------------------------------------------------

func (m *Manager) freshRoomIDLocked() string {
	for i := 0; i < 32; i++ {
		id := "room_" + randomString(roomIDLen, "abcdefghijklmnopqrstuvwxyz0123456789")
		if _, clash := m.rooms[id]; !clash {
			return id
		}
	}
	// Astronomically unlikely; keep the server alive rather than panicking.
	return "room_" + randomString(roomIDLen+8, "abcdefghijklmnopqrstuvwxyz0123456789")
}

func (m *Manager) freshInviteLocked() string {
	for i := 0; i < 64; i++ {
		code := randomString(inviteCodeLen, inviteAlphabet)
		if _, clash := m.invites[code]; !clash {
			return code
		}
	}
	return randomString(inviteCodeLen+2, inviteAlphabet)
}

// randomString draws n characters from alphabet using crypto/rand.
func randomString(n int, alphabet string) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// A deterministic fallback would make room ids and invite codes
		// predictable (an attacker could guess invite codes to gatecrash
		// private rooms). The system CSPRNG being unavailable is unrecoverable:
		// fail hard rather than silently degrade.
		panic("room: crypto/rand unavailable, refusing to generate ids: " + err.Error())
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}
