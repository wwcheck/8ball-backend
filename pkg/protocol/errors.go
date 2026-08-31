package protocol

import "fmt"

// Error codes carried in ErrorResp.ErrorCode (string, stable) and .Code
// (numeric, grouped by domain). The string form is what Unity should switch on;
// the numeric form exists for logging / metrics aggregation.
//
//	1xxx - transport / request level
//	2xxx - room & matchmaking
//	3xxx - gameplay & rules
//	5xxx - session / reconnect
//	9xxx - server internal
const (
	ErrBadRequest    = "BAD_REQUEST"
	ErrUnknownType   = "UNKNOWN_MESSAGE_TYPE"
	ErrUnauthorized  = "UNAUTHORIZED"
	ErrRateLimited   = "RATE_LIMITED"
	ErrPayloadTooBig = "PAYLOAD_TOO_LARGE"

	ErrRoomNotFound  = "ROOM_NOT_FOUND"
	ErrRoomFull      = "ROOM_FULL"
	ErrAlreadyInRoom = "ALREADY_IN_ROOM"
	ErrNotInRoom     = "NOT_IN_ROOM"
	ErrInvalidInvite = "INVALID_INVITE_CODE"
	ErrRoomClosed    = "ROOM_CLOSED"
	ErrAlreadyQueued = "MATCH_ALREADY_QUEUED"
	ErrNotQueued     = "MATCH_NOT_QUEUED"
	ErrNotSeated     = "NOT_SEATED"     // spectator tried a seated-only action
	ErrAlreadySeated = "ALREADY_SEATED" // player already occupies a match seat

	ErrNotYourTurn       = "NOT_YOUR_TURN"
	ErrInvalidPhase      = "INVALID_PHASE"
	ErrInvalidShot       = "INVALID_SHOT"
	ErrInvalidPlacement  = "INVALID_PLACEMENT"
	ErrNotBallInHand     = "NOT_BALL_IN_HAND"
	ErrInvalidShotResult = "INVALID_SHOT_RESULT"
	ErrDuplicateShot     = "DUPLICATE_SHOT"
	ErrGameNotStarted    = "GAME_NOT_STARTED"
	ErrGameFinished      = "GAME_FINISHED"

	ErrSessionInvalid   = "SESSION_INVALID"
	ErrReconnectExpired = "RECONNECT_EXPIRED"
	ErrNothingToResume  = "NOTHING_TO_RESUME"

	ErrInternal = "INTERNAL_ERROR"
)

// numericCodes maps the stable string codes to their numeric counterparts.
var numericCodes = map[string]int{
	ErrBadRequest:    1001,
	ErrUnknownType:   1002,
	ErrUnauthorized:  1003,
	ErrRateLimited:   1004,
	ErrPayloadTooBig: 1005,

	ErrRoomNotFound:  2001,
	ErrRoomFull:      2002,
	ErrAlreadyInRoom: 2003,
	ErrNotInRoom:     2004,
	ErrInvalidInvite: 2005,
	ErrRoomClosed:    2006,
	ErrAlreadyQueued: 2007,
	ErrNotQueued:     2008,
	ErrNotSeated:     2009,
	ErrAlreadySeated: 2010,

	ErrNotYourTurn:       3001,
	ErrInvalidPhase:      3002,
	ErrInvalidShot:       3003,
	ErrInvalidPlacement:  3004,
	ErrNotBallInHand:     3005,
	ErrInvalidShotResult: 3006,
	ErrDuplicateShot:     3007,
	ErrGameNotStarted:    3008,
	ErrGameFinished:      3009,

	ErrSessionInvalid:   5001,
	ErrReconnectExpired: 5002,
	ErrNothingToResume:  5003,

	ErrInternal: 9001,
}

// NumericCode returns the numeric form of a string error code (0 if unknown).
func NumericCode(code string) int {
	return numericCodes[code]
}

// NewError builds a ready-to-send ERROR message.
func NewError(code, message string) *ErrorResp {
	return &ErrorResp{
		Envelope:  Envelope{Type: TypeError},
		ErrorCode: code,
		Code:      NumericCode(code),
		Message:   message,
	}
}

// NewErrorf is NewError with printf-style formatting.
func NewErrorf(code, format string, args ...any) *ErrorResp {
	return NewError(code, fmt.Sprintf(format, args...))
}

// GameError is a typed error that handlers can return; the transport layer
// turns it into an ERROR frame.
type GameError struct {
	Code    string
	Message string
}

func (e *GameError) Error() string { return e.Code + ": " + e.Message }

// Errf constructs a *GameError.
func Errf(code, format string, args ...any) *GameError {
	return &GameError{Code: code, Message: fmt.Sprintf(format, args...)}
}
