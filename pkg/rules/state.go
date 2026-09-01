// Package rules implements the authoritative American 8-ball state machine and
// arbitration logic. It is the single source of truth for "was that shot legal,
// who shoots next, and did anybody win".
//
// The rule set mirrors D:/UnityProjects/8Ball_PhysX/GAME_RULES.md so the
// client's local prediction and the server's verdict never disagree. Places
// where GAME_RULES.md deviates from official WPA rules are marked with
// "PROJECT RULE" and are toggleable via Options.
package rules

import (
	"errors"
	"sort"
	"time"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// Group identifies a player's assigned ball group.
type Group int

const (
	GroupUnassigned Group = iota // open table
	GroupSolid                   // 1-7
	GroupStripe                  // 9-15
	GroupBlack                   // the 8 ball (never assigned to a player)
	GroupCue                     // the cue ball
)

// GroupOf returns the group a ball id belongs to.
func GroupOf(ballID int) Group {
	switch {
	case ballID == protocol.CueBallID:
		return GroupCue
	case ballID == protocol.EightBallID:
		return GroupBlack
	case ballID >= 1 && ballID <= 7:
		return GroupSolid
	case ballID >= 9 && ballID <= protocol.MaxBallID:
		return GroupStripe
	default:
		return GroupUnassigned
	}
}

// Other returns the opposing group for solid/stripe, GroupUnassigned otherwise.
func (g Group) Other() Group {
	switch g {
	case GroupSolid:
		return GroupStripe
	case GroupStripe:
		return GroupSolid
	default:
		return GroupUnassigned
	}
}

// Wire returns the JSON name of the group, or nil when still unassigned.
func (g Group) Wire() *string {
	switch g {
	case GroupSolid:
		s := protocol.BallTypeSolid
		return &s
	case GroupStripe:
		s := protocol.BallTypeStripe
		return &s
	case GroupBlack:
		s := protocol.BallTypeBlack
		return &s
	default:
		return nil
	}
}

// ErrUnknownPlayer is returned when a player id is not seated in the game.
var ErrUnknownPlayer = errors.New("rules: unknown player")

// Player is one of the two seats in a game.
type Player struct {
	ID        string
	Name      string
	Avatar    string
	Seat      int // 1 or 2
	Group     Group
	Ready     bool
	Connected bool
}

// Options carries the tunable rule decisions. Defaults follow GAME_RULES.md.
type Options struct {
	// ContinueOnAnyPot: PROJECT RULE. GAME_RULES.md §特殊情况 2 states that
	// potting an opponent ball (after a legal first contact) still lets the
	// shooter continue. Official WPA rules require potting one of your OWN
	// balls. true = follow GAME_RULES.md.
	ContinueOnAnyPot bool

	// BallInHandOnAnyFoul: PROJECT RULE. GAME_RULES.md §犯规结果 only grants
	// ball-in-hand when the cue ball is pocketed / off table; other fouls just
	// pass the turn. Official WPA rules grant ball-in-hand on every foul.
	// false = follow GAME_RULES.md.
	BallInHandOnAnyFoul bool

	// KitchenOnlyBallInHand: GAME_RULES.md restricts placement to the kitchen
	// (behind the head string) rather than anywhere on the table.
	KitchenOnlyBallInHand bool

	// AssignGroupOnFoul: whether a foul shot that still pots a ball assigns
	// groups. GAME_RULES.md is silent; official rules say no.
	AssignGroupOnFoul bool
}

// DefaultOptions returns the GAME_RULES.md-aligned configuration.
func DefaultOptions() Options {
	return Options{
		ContinueOnAnyPot:      true,
		BallInHandOnAnyFoul:   false,
		KitchenOnlyBallInHand: true,
		AssignGroupOnFoul:     false,
	}
}

// Game is the authoritative per-room game state. It is NOT goroutine safe;
// every room owns exactly one Game and mutates it from a single goroutine.
type Game struct {
	opts Options

	Players [2]*Player
	Balls   [protocol.BallCount]protocol.BallState

	Phase       string
	CurrentTurn string
	IsBreakShot bool
	BallInHand  bool
	KitchenOnly bool
	ShotNumber  int

	GameStatus string
	WinnerID   string
	LoserID    string
	EndReason  string

	StartedAt time.Time
	EndedAt   time.Time
}

// ErrGameFull is returned when both seats are already taken.
var ErrGameFull = errors.New("rules: both seats are occupied")

// NewGame creates an empty, racked game with no players seated yet.
func NewGame(opts Options) *Game {
	g := &Game{
		opts:        opts,
		Phase:       protocol.PhaseWaiting,
		GameStatus:  protocol.GameStatusPlaying,
		IsBreakShot: true,
	}
	g.Balls = NewRack()
	return g
}

// AddPlayer seats a player in the first free seat. Seat 1 breaks
// (GAME_RULES.md §开局: 玩家1先手).
func (g *Game) AddPlayer(id, name, avatar string) (*Player, error) {
	if p := g.PlayerByID(id); p != nil {
		return p, nil
	}
	for i := range g.Players {
		if g.Players[i] == nil {
			p := &Player{ID: id, Name: name, Avatar: avatar, Seat: i + 1, Group: GroupUnassigned, Connected: true}
			g.Players[i] = p
			return p, nil
		}
	}
	return nil, ErrGameFull
}

// RemovePlayer frees a seat. Only meaningful before the game starts.
func (g *Game) RemovePlayer(id string) {
	for i := range g.Players {
		if g.Players[i] != nil && g.Players[i].ID == id {
			g.Players[i] = nil
			return
		}
	}
}

// Seated reports how many seats are occupied.
func (g *Game) Seated() int {
	n := 0
	for _, p := range g.Players {
		if p != nil {
			n++
		}
	}
	return n
}

// BothReady reports whether the game may start.
func (g *Game) BothReady() bool {
	return g.Seated() == 2 && g.Players[0].Ready && g.Players[1].Ready
}

// Start moves a fully-ready game into the opening ball-in-hand phase.
func (g *Game) Start(now time.Time) {
	if g.Seated() != 2 {
		return
	}
	g.StartedAt = now
	g.CurrentTurn = g.Players[0].ID
	g.ShotNumber = 0
	g.IsBreakShot = true
	// GAME_RULES.md §开局 step 2: the breaker places the cue ball in the kitchen.
	g.BallInHand = true
	g.KitchenOnly = true
	g.Phase = protocol.PhaseBallInHand
}

// ResetForNextRound returns a finished game to the pre-game waiting state while
// keeping both seats occupied ("留桌"). Groups, rack, score and phase are all
// cleared so the same two players can READY -> GAME_START again.
func (g *Game) ResetForNextRound() {
	g.Balls = NewRack()
	g.Phase = protocol.PhaseWaiting
	g.CurrentTurn = ""
	g.IsBreakShot = true
	g.BallInHand = false
	g.KitchenOnly = false
	g.ShotNumber = 0
	g.GameStatus = protocol.GameStatusPlaying
	g.WinnerID = ""
	g.LoserID = ""
	g.EndReason = ""
	g.StartedAt = time.Time{}
	g.EndedAt = time.Time{}
	for _, p := range g.Players {
		if p != nil {
			p.Group = GroupUnassigned
			p.Ready = false
		}
	}
}

// Options exposes the active rule configuration.
func (g *Game) Options() Options { return g.opts }

// Finished reports whether the game has concluded.
func (g *Game) Finished() bool { return g.Phase == protocol.PhaseGameOver }

// PlayerByID looks up a seated player.
func (g *Game) PlayerByID(id string) *Player {
	for _, p := range g.Players {
		if p != nil && p.ID == id {
			return p
		}
	}
	return nil
}

// Opponent returns the other seat.
func (g *Game) Opponent(id string) *Player {
	for _, p := range g.Players {
		if p != nil && p.ID != id {
			return p
		}
	}
	return nil
}

// Ball returns a pointer to the authoritative state of one ball.
func (g *Game) Ball(id int) *protocol.BallState {
	if !protocol.IsValidBallID(id) {
		return nil
	}
	return &g.Balls[id]
}

// BallStates returns a copy of every ball's state, ordered by ball id.
func (g *Game) BallStates() []protocol.BallState {
	out := make([]protocol.BallState, 0, protocol.BallCount)
	for i := range g.Balls {
		out = append(out, g.Balls[i])
	}
	return out
}

// offTable reports whether a ball is no longer in play.
func offTable(b protocol.BallState) bool { return b.InPocket || b.OutOfBounds }

// RemainingFor counts how many of a group's balls are still on the table.
func (g *Game) RemainingFor(gr Group) int {
	if gr != GroupSolid && gr != GroupStripe {
		return 0
	}
	n := 0
	for id := 1; id <= protocol.MaxBallID; id++ {
		if id == protocol.EightBallID {
			continue
		}
		if GroupOf(id) == gr && !offTable(g.Balls[id]) {
			n++
		}
	}
	return n
}

// PocketedFor lists the pocketed balls of a group, ascending.
func (g *Game) PocketedFor(gr Group) []int {
	out := []int{}
	if gr != GroupSolid && gr != GroupStripe {
		return out
	}
	for id := 1; id <= protocol.MaxBallID; id++ {
		if id == protocol.EightBallID {
			continue
		}
		if GroupOf(id) == gr && g.Balls[id].InPocket {
			out = append(out, id)
		}
	}
	sort.Ints(out)
	return out
}

// GroupCleared reports whether every ball of a player's group is off the table,
// i.e. the player is "on the 8".
func (g *Game) GroupCleared(p *Player) bool {
	if p == nil || p.Group == GroupUnassigned {
		return false
	}
	return g.RemainingFor(p.Group) == 0
}

// PlayerInfo projects a player into its wire representation.
func (g *Game) PlayerInfo(p *Player) protocol.PlayerInfo {
	if p == nil {
		return protocol.PlayerInfo{}
	}
	return protocol.PlayerInfo{
		PlayerID:       p.ID,
		Name:           p.Name,
		Avatar:         p.Avatar,
		Role:           protocol.RoleSeated,
		Position:       p.Seat,
		BallType:       p.Group.Wire(),
		Ready:          p.Ready,
		Connected:      p.Connected,
		PocketedBalls:  g.PocketedFor(p.Group),
		RemainingBalls: g.RemainingFor(p.Group),
		OnEightBall:    g.GroupCleared(p),
	}
}

// PlayerInfos projects both seats, ordered by seat number.
func (g *Game) PlayerInfos() []protocol.PlayerInfo {
	out := make([]protocol.PlayerInfo, 0, 2)
	for _, p := range g.Players {
		if p != nil {
			out = append(out, g.PlayerInfo(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return out
}

// Score reports pocketed-ball counts keyed by player id.
func (g *Game) Score() map[string]int {
	score := make(map[string]int, 2)
	for _, p := range g.Players {
		if p != nil {
			score[p.ID] = len(g.PocketedFor(p.Group))
		}
	}
	return score
}

// Snapshot builds the full authoritative state payload used by ROOM_JOINED,
// GAME_START and SNAPSHOT (reconnect).
func (g *Game) Snapshot() protocol.GameStateDTO {
	return protocol.GameStateDTO{
		Players:         g.PlayerInfos(),
		GamePhase:       g.Phase,
		CurrentPlayerID: g.CurrentTurn,
		BallStates:      g.BallStates(),
		Score:           g.Score(),
		IsBreakShot:     g.IsBreakShot,
		BallInHand:      g.BallInHand,
		KitchenOnly:     g.KitchenOnly,
		ShotNumber:      g.ShotNumber,
		GameStatus:      g.GameStatus,
		WinnerID:        g.WinnerID,
		Table:           protocol.DefaultTableInfo(),
		Timestamp:       time.Now().UnixMilli(),
	}
}

// statusForWinner converts a winning player into the wire game status.
func (g *Game) statusForWinner(winner *Player) string {
	if winner == nil {
		return protocol.GameStatusDraw
	}
	if winner.Seat == 1 {
		return protocol.GameStatusP1Wins
	}
	return protocol.GameStatusP2Wins
}

// finish terminates the game.
func (g *Game) finish(winner, loser *Player, reason string) {
	g.Phase = protocol.PhaseGameOver
	g.GameStatus = g.statusForWinner(winner)
	if winner != nil {
		g.WinnerID = winner.ID
	}
	if loser != nil {
		g.LoserID = loser.ID
	}
	g.EndReason = reason
	g.BallInHand = false
	g.KitchenOnly = false
	// 终局必须清干净所有进行中状态：Snapshot() 会把 IsBreakShot 带进终局快照
	// 推给客户端（结算画面、观战态）。开球进黑八判负这条路径不经过
	// ApplyShotResult 末尾的 g.IsBreakShot = false（engine.go），若不在终局
	// 统一复位，那一局的终局快照会残留 IsBreakShot=true，客户端据此做的
	// 任何判断（如"是否显示重开按钮"）都会走错分支。
	g.IsBreakShot = false
	g.EndedAt = time.Now()
}

// Concede ends the game in favour of the opponent of playerID.
func (g *Game) Concede(playerID string) (*protocol.StrikeResult, error) {
	loser := g.PlayerByID(playerID)
	if loser == nil {
		return nil, ErrUnknownPlayer
	}
	winner := g.Opponent(playerID)
	g.finish(winner, loser, protocol.ReasonConcede)
	return &protocol.StrikeResult{
		StrikePlayerID: playerID,
		GameStatus:     g.GameStatus,
		WinnerID:       g.WinnerID,
		NextPhase:      protocol.PhaseGameOver,
		Reason:         protocol.ReasonConcede,
	}, nil
}

// ForfeitByAbandon ends the game because a player left / failed to reconnect.
func (g *Game) ForfeitByAbandon(playerID, reason string) {
	loser := g.PlayerByID(playerID)
	winner := g.Opponent(playerID)
	g.finish(winner, loser, reason)
}
