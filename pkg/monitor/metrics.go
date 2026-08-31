// Package monitor holds lightweight, dependency-free runtime counters. They are
// exposed as JSON via GET /metrics and summarised in GET /info.
//
// ARCHITECTURE.md §监控指标 calls for Prometheus; that is deliberately deferred
// to Phase 4 so the minimal loop stays dependency-light. The counter names here
// already match the documented metric names to make the swap mechanical.
package monitor

import (
	"sync/atomic"
	"time"
)

// Metrics is the process-wide counter set. All methods are goroutine safe.
type Metrics struct {
	startedAt time.Time

	connectionsOpened  atomic.Int64
	connectionsClosed  atomic.Int64
	connectionsCurrent atomic.Int64

	roomsCreated atomic.Int64
	roomsClosed  atomic.Int64
	roomsActive  atomic.Int64

	gamesStarted  atomic.Int64
	gamesFinished atomic.Int64

	matchesMade atomic.Int64
	queueSize   atomic.Int64
	reconnects  atomic.Int64
	forfeits    atomic.Int64

	messagesIn  atomic.Int64
	messagesOut atomic.Int64

	shotsAccepted atomic.Int64
	shotsRejected atomic.Int64
	fouls         atomic.Int64
	errorsSent    atomic.Int64
}

// New creates a fresh metric set.
func New() *Metrics { return &Metrics{startedAt: time.Now()} }

// Connection lifecycle.
func (m *Metrics) ConnectionOpened() {
	m.connectionsOpened.Add(1)
	m.connectionsCurrent.Add(1)
}

// ConnectionClosed records a terminated session.
func (m *Metrics) ConnectionClosed() {
	m.connectionsClosed.Add(1)
	m.connectionsCurrent.Add(-1)
}

// RoomCreated records a new room.
func (m *Metrics) RoomCreated() {
	m.roomsCreated.Add(1)
	m.roomsActive.Add(1)
}

// RoomClosed records a destroyed room.
func (m *Metrics) RoomClosed() {
	m.roomsClosed.Add(1)
	m.roomsActive.Add(-1)
}

// GameStarted records a game entering play.
func (m *Metrics) GameStarted() { m.gamesStarted.Add(1) }

// GameFinished records a completed game.
func (m *Metrics) GameFinished() { m.gamesFinished.Add(1) }

// MatchMade records a successful pairing from the queue.
func (m *Metrics) MatchMade() { m.matchesMade.Add(1) }

// SetQueueSize publishes the current matchmaking queue depth.
func (m *Metrics) SetQueueSize(n int) { m.queueSize.Store(int64(n)) }

// Reconnected records a resumed session.
func (m *Metrics) Reconnected() { m.reconnects.Add(1) }

// Forfeit records a game lost by abandonment.
func (m *Metrics) Forfeit() { m.forfeits.Add(1) }

// MessageIn records an inbound frame.
func (m *Metrics) MessageIn() { m.messagesIn.Add(1) }

// MessageOut records an outbound frame.
func (m *Metrics) MessageOut() { m.messagesOut.Add(1) }

// ShotAccepted records a validated shot.
func (m *Metrics) ShotAccepted() { m.shotsAccepted.Add(1) }

// ShotRejected records a shot rejected by validation.
func (m *Metrics) ShotRejected() { m.shotsRejected.Add(1) }

// Foul records an arbitrated foul.
func (m *Metrics) Foul() { m.fouls.Add(1) }

// ErrorSent records an ERROR frame sent to a client.
func (m *Metrics) ErrorSent() { m.errorsSent.Add(1) }

// Snapshot renders the counters for the /metrics endpoint.
func (m *Metrics) Snapshot() map[string]any {
	return map[string]any{
		"uptime_seconds":             int64(time.Since(m.startedAt).Seconds()),
		"server.connection_count":    m.connectionsCurrent.Load(),
		"server.connections_opened":  m.connectionsOpened.Load(),
		"server.connections_closed":  m.connectionsClosed.Load(),
		"game.active_rooms":          m.roomsActive.Load(),
		"game.rooms_created":         m.roomsCreated.Load(),
		"game.rooms_closed":          m.roomsClosed.Load(),
		"game.games_started":         m.gamesStarted.Load(),
		"game.games_finished":        m.gamesFinished.Load(),
		"game.matches_total":         m.matchesMade.Load(),
		"game.match_queue_size":      m.queueSize.Load(),
		"game.reconnects_total":      m.reconnects.Load(),
		"game.forfeits_total":        m.forfeits.Load(),
		"game.shots_accepted_total":  m.shotsAccepted.Load(),
		"game.shots_rejected_total":  m.shotsRejected.Load(),
		"game.fouls_total":           m.fouls.Load(),
		"network.messages_in_total":  m.messagesIn.Load(),
		"network.messages_out_total": m.messagesOut.Load(),
		"network.errors_sent_total":  m.errorsSent.Load(),
	}
}
