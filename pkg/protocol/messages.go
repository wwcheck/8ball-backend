// Package protocol defines the complete JSON wire protocol spoken between the
// Unity client and the 8Ball game server.
//
// Design notes
//
//  1. The envelope is *flat*: type-specific fields live at the top level of the
//     JSON object, next to "type"/"playerId"/"timestamp". This matches the
//     examples in D:/UnityProjects/8Ball_PhysX/MULTIPLAYER_TECH.md §2.2 which
//     the Unity client was designed against. (REQ-010 shows a nested "data"
//     wrapper - see docs/PROTOCOL.md "待对齐点" for that difference.)
//  2. Every server -> client message carries serverTime (unix ms) and, for
//     room-scoped messages, a monotonically increasing per-room seq so the
//     client can detect gaps / out-of-order application.
//  3. The server is authoritative for *rules arbitration*. Ball physics is
//     simulated by the shooting client and reported back via SHOT_RESULT, which
//     the server sanity-checks before arbitrating (see pkg/rules).
package protocol

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is bumped on any breaking wire change. Reported in WELCOME.
const ProtocolVersion = "1.0.0"

// ---------------------------------------------------------------------------
// Message types: client -> server
// ---------------------------------------------------------------------------

const (
	TypeHello            = "HELLO"            // identify / (stub) auth
	TypePing             = "PING"             // heartbeat + RTT probe
	TypeQuickMatch       = "QUICK_MATCH"      // enter matchmaking queue
	TypeCancelMatch      = "CANCEL_MATCH"     // leave matchmaking queue
	TypeCreateRoom       = "CREATE_ROOM"      // create a private room
	TypeJoinRoom         = "JOIN_ROOM"        // join by roomId or inviteCode
	TypeLeaveRoom        = "LEAVE_ROOM"       // leave current room
	TypeReady            = "READY"            // ready / unready in room
	TypeReconnect        = "RECONNECT"        // resume a session after drop
	TypeRequestSnapshot  = "REQUEST_SNAPSHOT" // ask for authoritative snapshot
	TypeShoot            = "SHOOT"            // shot intent (angle/power/spin)
	TypeStateFrame       = "STATE_FRAME"      // shooter's 20Hz sim frame
	TypeShotResult       = "SHOT_RESULT"      // shooter's settled sim result
	TypeCueBallPlacement = "CUE_BALL_PLACEMENT"
	TypeConcede          = "CONCEDE"    // forfeit the game
	TypeJoinGame         = "JOIN_GAME"  // grab a free seat (spectator -> player)
	TypeStandUp          = "STAND_UP"   // vacate a seat (player -> spectator)
	TypeDisconnect       = "DISCONNECT" // graceful goodbye
)

// ---------------------------------------------------------------------------
// Message types: server -> client
// ---------------------------------------------------------------------------

const (
	TypeWelcome             = "WELCOME"
	TypePong                = "PONG"
	TypeMatchQueued         = "MATCH_QUEUED"
	TypeMatchFound          = "MATCH_FOUND"
	TypeMatchCancelled      = "MATCH_CANCELLED"
	TypeRoomCreated         = "ROOM_CREATED"
	TypeRoomJoined          = "ROOM_JOINED"
	TypeRoomState           = "ROOM_STATE"
	TypePlayerJoined        = "PLAYER_JOINED"
	TypePlayerLeft          = "PLAYER_LEFT"
	TypeGameStart           = "GAME_START"
	TypeShootAck            = "SHOOT_ACK"
	TypeShotBroadcast       = "SHOT_BROADCAST"
	TypeStateUpdate         = "STATE_UPDATE"
	TypeBallsStopped        = "BALLS_STOPPED"
	TypeCueBallPlacementAck = "CUE_BALL_PLACEMENT_ACK"
	TypeTurnChange          = "TURN_CHANGE"
	TypeGameOver            = "GAME_OVER"
	TypeSnapshot            = "SNAPSHOT"
	TypePlayerDisconnected  = "PLAYER_DISCONNECTED"
	TypePlayerReconnected   = "PLAYER_RECONNECTED"
	TypeJoinGameAck         = "JOIN_GAME_ACK"
	TypeStandUpAck          = "STAND_UP_ACK"
	TypeError               = "ERROR"
)

// ---------------------------------------------------------------------------
// Enumerations shared with the client
// ---------------------------------------------------------------------------

// Game phases. Mirrors Unity's PoolGameManager.GameState plus the two network
// specific phases (Waiting / BallInHand).
const (
	PhaseWaiting    = "Waiting"    // room not full / not both ready
	PhaseBallInHand = "BallInHand" // cue ball must be placed before aiming
	PhaseAiming     = "Aiming"     // waiting for SHOOT from current player
	PhaseMoving     = "Moving"     // balls rolling, waiting for SHOT_RESULT
	PhaseResolving  = "Resolving"  // server arbitrating
	PhaseDecision   = "Decision"   // result published, transitioning
	PhaseGameOver   = "GameOver"
)

// Ball group names as seen on the wire.
const (
	BallTypeSolid  = "solid"  // 1-7
	BallTypeStripe = "stripe" // 9-15
	BallTypeBlack  = "black"  // 8
	BallTypeCue    = "cue"    // 0
)

// Player role within a room. A room holds at most two seated players (the
// match); everyone else is a spectator who watches but cannot act.
const (
	RoleSeated    = "seated"    // occupies one of the two match seats
	RoleSpectator = "spectator" // watches the match, cannot SHOOT/READY/etc.
)

// Foul codes carried in StrikeResult.FoulType. The first five are the codes
// already declared in MULTIPLAYER_TECH.md §2.2.4; the rest are additive
// extensions required by GAME_RULES.md.
const (
	FoulNone               = ""
	FoulNoContact          = "NO_CONTACT"             // cue ball touched nothing
	FoulWrongBall          = "WRONG_BALL"             // first contact not own group
	FoulCueBallPocketed    = "CUE_BALL_POCKETED"      // cue ball in a pocket
	FoulBlackPocketedEarly = "BLACK_POCKETED_EARLY"   // 8 down too soon -> loss
	FoulBlackWithCue       = "BLACK_WITH_CUE"         // 8 + cue ball -> loss
	FoulCueBallOutOfBounds = "CUE_BALL_OUT_OF_BOUNDS" // cue ball left the table
	FoulBlackOutOfBounds   = "BLACK_OUT_OF_BOUNDS"    // 8 left the table -> loss
	FoulNoShot             = "NO_SHOT"                // cue ball never moved
	FoulTurnTimeout        = "TURN_TIMEOUT"           // player ran out of time
	FoulShotTimeout        = "SHOT_RESULT_TIMEOUT"    // no SHOT_RESULT in time
	FoulIllegalReport      = "ILLEGAL_RESULT_REPORT"  // sim result failed checks
	FoulNoBankContact      = "NO_BANK_CONTACT"        // must hit bank if no pocketed balls
	FoulBlackWrongPocket   = "BLACK8_WRONG_POCKET"    // 8 pocketed but wrong pocket -> loss
)

// Overall game status, as in MULTIPLAYER_TECH.md §2.2.4.
const (
	GameStatusPlaying = "playing"
	GameStatusP1Wins  = "player1_wins"
	GameStatusP2Wins  = "player2_wins"
	GameStatusDraw    = "draw"
)

// Room status, as in REQ-010 §数据库设计 (Room.status).
const (
	RoomStatusWaiting  = "WAITING"
	RoomStatusReady    = "READY"
	RoomStatusInGame   = "IN_GAME"
	RoomStatusFinished = "FINISHED"
	RoomStatusClosed   = "CLOSED"
)

// Reasons reported in GAME_OVER / PLAYER_LEFT.
const (
	ReasonNormal              = "normal"
	ReasonConcede             = "concede"
	ReasonOpponentLeft        = "opponent_left"
	ReasonOpponentDisconnect  = "opponent_disconnect_timeout"
	ReasonIllegalEightBall    = "illegal_eight_ball"
	ReasonLegalEightBall      = "legal_eight_ball"
	ReasonEightBallOutOfTable = "eight_ball_out_of_table"
	ReasonWrongPocket         = "wrong_pocket"          // 8 in wrong pocket
	ReasonRoomClosed          = "room_closed"
)

// ---------------------------------------------------------------------------
// Envelope + shared DTOs
// ---------------------------------------------------------------------------

// Envelope holds the fields common to every message. It is embedded (not
// nested) so the resulting JSON stays flat.
type Envelope struct {
	Type       string `json:"type"`
	MessageID  string `json:"messageId,omitempty"`  // client-generated, echoed in ACKs
	RoomID     string `json:"roomId,omitempty"`     // always set on room-scoped S->C
	PlayerID   string `json:"playerId,omitempty"`   // actor of the message
	Seq        uint64 `json:"seq,omitempty"`        // per-room monotonic (S->C)
	Timestamp  int64  `json:"timestamp,omitempty"`  // unix ms, sender's clock
	ClientTime int64  `json:"clientTime,omitempty"` // echoed for RTT math
	ServerTime int64  `json:"serverTime,omitempty"` // unix ms, server clock
}

// Head exposes the embedded envelope so generic helpers can stamp routing
// fields (roomId / seq / serverTime) on any message without a type switch.
func (e *Envelope) Head() *Envelope { return e }

// Message is satisfied by every *Resp type in this package, because they all
// embed Envelope by value.
type Message interface {
	Head() *Envelope
}

// Vector3 mirrors UnityEngine.Vector3 (metres, table-local coordinates).
type Vector3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

// BallState is the authoritative per-ball snapshot. BallID follows the game's
// numbering: 0 = cue, 1-7 = solid, 8 = black, 9-15 = stripe.
type BallState struct {
	BallID          int     `json:"ballId"`
	Position        Vector3 `json:"position"`
	Velocity        Vector3 `json:"velocity"`
	AngularVelocity Vector3 `json:"angularVelocity"`
	InPocket        bool    `json:"inPocket"`
	IsMoving        bool    `json:"isMoving"`
	OutOfBounds     bool    `json:"outOfBounds"`
}

// PlayerInfo is the per-player view shared in room/game snapshots. It describes
// both seated players (Role = "seated", Position 1|2) and spectators
// (Role = "spectator", Position 0).
type PlayerInfo struct {
	PlayerID       string  `json:"playerId"`
	Name           string  `json:"name"`
	Avatar         string  `json:"avatar,omitempty"`
	Role           string  `json:"role"`     // "seated" | "spectator"
	Position       int     `json:"position"` // seat: 1 or 2 (0 for spectators)
	BallType       *string `json:"ballType"` // null until groups are assigned
	Ready          bool    `json:"ready"`
	Connected      bool    `json:"connected"`
	PocketedBalls  []int   `json:"pocketedBalls"` // own group balls already down
	RemainingBalls int     `json:"remainingBalls"`
	OnEightBall    bool    `json:"onEightBall"` // group cleared, shooting for the 8
}

// TableInfo is the geometry contract sent to clients in WELCOME.table and
// GameStateDTO.table. Mirrors Unity's NetTableInfo (Assets/Networking/
// NetMessages.cs) - keep the two in sync.
type TableInfo struct {
	HalfX         float64 `json:"halfX"`         // 台面半宽 (cushion face, X)
	HalfZ         float64 `json:"halfZ"`         // 台面半长 (cushion face, Z)
	LimitX        float64 `json:"limitX"`        // X 边界（球心可达）
	LimitZ        float64 `json:"limitZ"`        // Z 边界（球心可达）
	BallRadius    float64 `json:"ballRadius"`    // 球半径
	HeadStringX   float64 `json:"headStringX"`   // 开球线
	KitchenMinX   float64 `json:"kitchenMinX"`   // 厨房区最小 X
	KitchenMaxX   float64 `json:"kitchenMaxX"`   // 厨房区最大 X
	PowerMinSpeed float64 `json:"powerMinSpeed"` // 力度→速度下界 (m/s)
	PowerMaxSpeed float64 `json:"powerMaxSpeed"` // 力度→速度上界 (m/s)

	// PocketGeometry carries the corrected WPA pocket geometry. Optional so
	// that older clients that do not know the field keep working.
	PocketGeometry *PocketGeometryConfig `json:"pocketGeometry,omitempty"`
}

// PocketGeometryConfig defines pocket dimensions for client-side validation
// Allows clients to sync with backend pocket geometry definitions
type PocketGeometryConfig struct {
	CornerMouthWidth        float64 `json:"cornerMouthWidth"`        // Corner pocket opening
	SideMouthWidth          float64 `json:"sideMouthWidth"`          // Side pocket opening
	CornerCutX              float64 `json:"cornerCutX"`              // Corner cushion cut point X
	CornerCutY              float64 `json:"cornerCutY"`              // Corner cushion cut point Y
	SideCutHalfX            float64 `json:"sideCutHalfX"`            // Side pocket cut point half-width
	JawRadius               float64 `json:"jawRadius"`               // Pocket jaw corner radius
	ThroatDepth             float64 `json:"throatDepth"`             // Pocket depth
	CushionSegmentLongInner float64 `json:"cushionSegmentLongInner"` // Long-rail inner boundary
	CushionSegmentLongOuter float64 `json:"cushionSegmentLongOuter"` // Long-rail outer boundary
	CushionSegmentShortHalf float64 `json:"cushionSegmentShortHalf"` // Short-rail half-width
}

// GameStateDTO is the full authoritative snapshot. Sent inside ROOM_JOINED,
// GAME_START and SNAPSHOT; a client that applies it verbatim is fully in sync.
type GameStateDTO struct {
	RoomID          string         `json:"roomId"`
	RoomStatus      string         `json:"roomStatus"`
	InviteCode      string         `json:"inviteCode,omitempty"`
	Players         []PlayerInfo   `json:"players"`
	Spectators      []PlayerInfo   `json:"spectators"`
	GamePhase       string         `json:"gamePhase"`
	CurrentPlayerID string         `json:"currentPlayerId"`
	BallStates      []BallState    `json:"ballStates"`
	Score           map[string]int `json:"score"`
	IsBreakShot     bool           `json:"isBreakShot"`
	BallInHand      bool           `json:"ballInHand"`
	KitchenOnly     bool           `json:"kitchenOnly"` // ball-in-hand restricted to kitchen
	ShotNumber      int            `json:"shotNumber"`  // increments per accepted SHOOT
	GameStatus      string         `json:"gameStatus"`
	WinnerID        string         `json:"winnerId,omitempty"`
	Seq             uint64         `json:"seq"`
	Table           TableInfo      `json:"table"`
	Timestamp       int64          `json:"timestamp"`
}

// StrikeResult is the server's arbitration verdict for one shot.
// Field names/semantics follow MULTIPLAYER_TECH.md §2.2.4.
type StrikeResult struct {
	StrikePlayerID   string  `json:"strikePlayerId"`
	BallType         *string `json:"ballType"` // shooter's group after this shot
	FoulType         *string `json:"foulType"` // null when the shot was legal
	FoulMessage      string  `json:"foulMessage,omitempty"`
	IsContinuing     bool    `json:"isContinuing"` // same player shoots again
	GameStatus       string  `json:"gameStatus"`   // playing / playerN_wins / draw
	NextPlayerID     string  `json:"nextPlayerId"`
	NextPhase        string  `json:"nextPhase"` // Aiming | BallInHand | GameOver
	BallInHand       bool    `json:"ballInHand"`
	KitchenOnly      bool    `json:"kitchenOnly"`
	FirstContactBall int     `json:"firstContactBall"` // 0 = nothing was hit
	GroupAssigned    bool    `json:"groupAssigned"`    // groups were decided this shot
	WinnerID         string  `json:"winnerId,omitempty"`
	Reason           string  `json:"reason,omitempty"`
}

// ---------------------------------------------------------------------------
// Client -> server payloads
// ---------------------------------------------------------------------------

// HelloReq: optional first message; the server also accepts identity via the
// /ws query string (?playerId=&name=&token=).
type HelloReq struct {
	Envelope
	Name      string `json:"name"`
	Avatar    string `json:"avatar,omitempty"`
	Token     string `json:"token,omitempty"`
	ClientVer string `json:"clientVersion,omitempty"`
}

// PingReq -> PongResp.
type PingReq struct {
	Envelope
}

// QuickMatchReq enters the matchmaking queue.
type QuickMatchReq struct {
	Envelope
	Name string `json:"name,omitempty"`
	Elo  int    `json:"elo,omitempty"` // reserved: Elo-banded matching (Phase 3)
}

// CreateRoomReq creates a room and returns an invite code.
type CreateRoomReq struct {
	Envelope
	Name     string `json:"name,omitempty"`
	IsPublic bool   `json:"isPublic,omitempty"`
}

// JoinRoomReq joins by roomId (preferred) or inviteCode.
type JoinRoomReq struct {
	Envelope
	InviteCode string `json:"inviteCode,omitempty"`
	Name       string `json:"playerName,omitempty"`
}

// ReadyReq toggles the ready flag. Both players ready -> GAME_START.
type ReadyReq struct {
	Envelope
	Ready bool `json:"ready"`
}

// ReconnectReq resumes a dropped session. SessionToken comes from WELCOME.
type ReconnectReq struct {
	Envelope
	SessionToken string `json:"sessionToken"`
	LastSeq      uint64 `json:"lastSeq,omitempty"`
}

// ShootReq is the shot *intent*. The server validates turn/phase/ranges then
// broadcasts it; it never trusts a client-computed outcome without checks.
type ShootReq struct {
	Envelope
	CueAngle float64  `json:"cueAngle"`       // radians, table-local
	Power    float64  `json:"power"`          // (0,1] -> powerMinSpeed..powerMaxSpeed
	Spin     *Vector3 `json:"spin,omitempty"` // optional english, each axis [-1,1]
}

// StateFrameReq is a 20Hz simulation frame from the shooting client. It is
// relayed to the opponent as STATE_UPDATE. Best-effort: may be dropped.
type StateFrameReq struct {
	Envelope
	ShotNumber int         `json:"shotNumber"`
	Frame      int64       `json:"frame,omitempty"`
	BallStates []BallState `json:"ballStates"`
}

// ShotResultReq is the settled outcome reported by the shooting client.
// PocketedBalls MUST be ordered by pocket time (the 8-ball "last ball in"
// rule from GAME_RULES.md depends on that order).
type ShotResultReq struct {
	Envelope
	ShotNumber          int         `json:"shotNumber"`
	FirstContactBall    int         `json:"firstContactBall"` // 0 = none
	PocketedBalls       []int       `json:"pocketedBalls"`
	OutOfBoundsBalls    []int       `json:"outOfBoundsBalls,omitempty"`
	CueBallMoved        bool        `json:"cueBallMoved"`
	CushionAfterContact bool        `json:"cushionAfterContact,omitempty"` // reserved
	DeclaredPocket      *int        `json:"declaredPocket,omitempty"`      // pocket number (0-5) for 8-ball declaration
	BallStates          []BallState `json:"ballStates"`
}

// CueBallPlacementReq places the cue ball during ball-in-hand.
type CueBallPlacementReq struct {
	Envelope
	Position Vector3 `json:"position"`
}

// ConcedeReq forfeits the current game.
type ConcedeReq struct {
	Envelope
}

// JoinGameReq asks the room to seat the sender in one of the two match seats.
// Only a spectator may send it; on success the server answers JOIN_GAME_ACK and
// broadcasts ROOM_STATE.
type JoinGameReq struct {
	Envelope
}

// StandUpReq vacates the sender's match seat and turns them back into a
// spectator. During an active game this is treated as a forfeit.
type StandUpReq struct {
	Envelope
}

// ---------------------------------------------------------------------------
// Server -> client payloads
// ---------------------------------------------------------------------------

// WelcomeResp is sent immediately after a successful WebSocket upgrade.
type WelcomeResp struct {
	Envelope
	SessionToken      string    `json:"sessionToken"`
	ProtocolVersion   string    `json:"protocolVersion"`
	PlayerNickname    string    `json:"playerNickname"`
	PlayerAvatar      string    `json:"playerAvatar,omitempty"`
	HeartbeatInterval int       `json:"heartbeatIntervalMs"`
	ReadTimeout       int       `json:"readTimeoutMs"`
	ReconnectWindow   int       `json:"reconnectWindowMs"`
	StateUpdateHz     int       `json:"stateUpdateHz"`
	Table             TableInfo `json:"table"`
	ResumableRoomID   string    `json:"resumableRoomId,omitempty"` // set if a game awaits reconnect
}

// PongResp answers PING; ClientTime is echoed so the client can compute RTT.
type PongResp struct {
	Envelope
}

// MatchQueuedResp confirms queue entry.
type MatchQueuedResp struct {
	Envelope
	QueuePosition int `json:"queuePosition"`
	QueueSize     int `json:"queueSize"`
	EstimatedWait int `json:"estimatedWaitMs"`
}

// MatchFoundResp announces a pairing; ROOM_JOINED with the snapshot follows.
type MatchFoundResp struct {
	Envelope
	Opponent PlayerInfo `json:"opponent"`
	YourSeat int        `json:"yourSeat"`
}

// MatchCancelledResp confirms the player left the queue.
type MatchCancelledResp struct {
	Envelope
	Reason string `json:"reason,omitempty"`
}

// RoomCreatedResp returns the new room's identifiers. The creator enters as a
// spectator by default and must send JOIN_GAME to grab a seat.
type RoomCreatedResp struct {
	Envelope
	InviteCode string       `json:"inviteCode"`
	YourSeat   int          `json:"yourSeat"` // 0 (creator is a spectator)
	Role       string       `json:"role"`     // "spectator"
	GameState  GameStateDTO `json:"gameState"`
}

// RoomJoinedResp is the reply to JOIN_ROOM (see MULTIPLAYER_TECH.md §2.2.1).
// A fresh JOIN_ROOM enters as a spectator (YourSeat 0, Role "spectator"); the
// player then sends JOIN_GAME to grab one of the two match seats.
type RoomJoinedResp struct {
	Envelope
	Status    string       `json:"status"`   // "success"
	YourSeat  int          `json:"yourSeat"` // 0 for spectators, 1|2 when seated
	Role      string       `json:"role"`     // "seated" | "spectator"
	GameState GameStateDTO `json:"gameState"`
}

// JoinGameAckResp is the private reply to JOIN_GAME. Rejections are reported as
// ERROR frames (with the request messageId echoed), so this ACK is only sent on
// success.
type JoinGameAckResp struct {
	Envelope
	Status    string       `json:"status"` // "seated"
	Seat      int          `json:"seat"`   // 1 or 2
	GameState GameStateDTO `json:"gameState"`
}

// StandUpAckResp is the private reply to STAND_UP. Rejections are reported as
// ERROR frames.
type StandUpAckResp struct {
	Envelope
	Status    string       `json:"status"` // "stood_down"
	GameState GameStateDTO `json:"gameState"`
}

// RoomStateResp is broadcast whenever membership / ready flags change. Both
// seats and the spectator list are included so viewers stay in sync.
type RoomStateResp struct {
	Envelope
	RoomStatus string       `json:"roomStatus"`
	Players    []PlayerInfo `json:"players"`
	Spectators []PlayerInfo `json:"spectators"`
	GamePhase  string       `json:"gamePhase"`
}

// PlayerEventResp backs PLAYER_JOINED / PLAYER_LEFT / PLAYER_RECONNECTED.
type PlayerEventResp struct {
	Envelope
	Player PlayerInfo `json:"player"`
	Reason string     `json:"reason,omitempty"`
}

// PlayerDisconnectedResp tells the peer how long the drop will be tolerated.
type PlayerDisconnectedResp struct {
	Envelope
	Player            PlayerInfo `json:"player"`
	ReconnectWindowMs int        `json:"reconnectWindowMs"`
	DeadlineUnixMs    int64      `json:"reconnectDeadline"`
}

// GameStartResp opens the game: full rack + who breaks.
type GameStartResp struct {
	Envelope
	GameState GameStateDTO `json:"gameState"`
	BreakerID string       `json:"breakerId"`
}

// ShootAckResp is the shooter's private acknowledgement.
type ShootAckResp struct {
	Envelope
	Status     string `json:"status"` // "accepted" | "rejected"
	ShotNumber int    `json:"shotNumber"`
	GamePhase  string `json:"gamePhase"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Message    string `json:"message,omitempty"`
}

// ShotBroadcastResp carries the accepted shot to both clients so they can run
// identical local simulations.
type ShotBroadcastResp struct {
	Envelope
	ShooterID         string      `json:"shooterId"`
	ShotNumber        int         `json:"shotNumber"`
	CueAngle          float64     `json:"cueAngle"`
	Power             float64     `json:"power"`
	Spin              *Vector3    `json:"spin"`
	InitialSpeed      float64     `json:"initialSpeed"` // m/s, derived from power
	GamePhase         string      `json:"gamePhase"`
	BallStates        []BallState `json:"ballStates"` // pre-shot authoritative state
	SimulatorPlayerID string      `json:"simulatorPlayerId"`
	NextStateUpdateIn int         `json:"nextStateUpdateIn"`
}

// StateUpdateResp is the relayed 20Hz frame (MULTIPLAYER_TECH.md §2.2.3).
type StateUpdateResp struct {
	Envelope
	GamePhase  string      `json:"gamePhase"`
	ShotNumber int         `json:"shotNumber"`
	BallStates []BallState `json:"ballStates"`
}

// BallsStoppedResp publishes the arbitration verdict (MULTIPLAYER_TECH §2.2.4).
type BallsStoppedResp struct {
	Envelope
	ShotNumber    int            `json:"shotNumber"`
	BallStates    []BallState    `json:"ballStates"`
	PocketedBalls []int          `json:"pocketedBalls"`
	StrikeResult  StrikeResult   `json:"strikeResult"`
	GamePhase     string         `json:"gamePhase"`
	Players       []PlayerInfo   `json:"players"`
	Score         map[string]int `json:"score"`
}

// CueBallPlacementAckResp confirms ball-in-hand placement.
type CueBallPlacementAckResp struct {
	Envelope
	Status          string      `json:"status"` // "accepted"
	GamePhase       string      `json:"gamePhase"`
	CurrentPlayerID string      `json:"currentPlayerId"`
	BallStates      []BallState `json:"ballStates"`
}

// TurnChangeResp is an explicit turn notification (also implied by BALLS_STOPPED).
type TurnChangeResp struct {
	Envelope
	CurrentPlayerID string `json:"currentPlayerId"`
	GamePhase       string `json:"gamePhase"`
	BallInHand      bool   `json:"ballInHand"`
	KitchenOnly     bool   `json:"kitchenOnly"`
	TurnTimeoutMs   int    `json:"turnTimeoutMs"`
}

// GameOverResp ends the game.
type GameOverResp struct {
	Envelope
	GameStatus string         `json:"gameStatus"`
	WinnerID   string         `json:"winnerId"`
	LoserID    string         `json:"loserId,omitempty"`
	Reason     string         `json:"reason"`
	Score      map[string]int `json:"score"`
	DurationMs int64          `json:"durationMs"`
	Players    []PlayerInfo   `json:"players"`
}

// SnapshotResp is the reconnect / resync payload.
type SnapshotResp struct {
	Envelope
	GameState GameStateDTO `json:"gameState"`
	Resumed   bool         `json:"resumed"`
}

// ErrorResp reports a rejected request (MULTIPLAYER_TECH.md §2.2.7).
type ErrorResp struct {
	Envelope
	ErrorCode string `json:"errorCode"`
	Code      int    `json:"code"`
	Message   string `json:"message"`
	Fatal     bool   `json:"fatal,omitempty"`
}

// ---------------------------------------------------------------------------
// Codec helpers
// ---------------------------------------------------------------------------

// ParseEnvelope extracts the common header from a raw frame.
func ParseEnvelope(raw []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return env, fmt.Errorf("protocol: decode envelope: %w", err)
	}
	if env.Type == "" {
		return env, fmt.Errorf("protocol: missing %q field", "type")
	}
	return env, nil
}

// Decode unmarshals a raw frame into a concrete request type. Because payload
// structs embed Envelope, the flat wire format round-trips correctly.
func Decode[T any](raw []byte) (*T, error) {
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("protocol: decode %T: %w", out, err)
	}
	return &out, nil
}

// StringPtr is a convenience helper for the nullable wire fields.
func StringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
