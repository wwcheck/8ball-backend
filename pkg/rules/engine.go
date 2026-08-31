package rules

import (
	"math"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// ShotReport is the settled outcome of one shot, as reported by the shooting
// client and already validated by ValidateShotResult.
//
// PocketedBalls is ordered by pocket time; the "8 must be the last ball to
// drop" condition (GAME_RULES.md §胜负判定 #4) relies on that ordering.
type ShotReport struct {
	ShooterID           string
	ShotNumber          int
	FirstContactBall    int // 0 = the cue ball touched nothing
	PocketedBalls       []int
	OutOfBoundsBalls    []int
	CueBallMoved        bool
	CushionAfterContact bool
	DeclaredPocket      *int // pocket number (0-5) for 8-ball declaration
	FinalBalls          []protocol.BallState
}

// ---------------------------------------------------------------------------
// State transitions driven by client intents
// ---------------------------------------------------------------------------

// ApplyShoot records an accepted shot and moves the game into the Moving phase.
// Callers must have run ValidateShoot first.
func (g *Game) ApplyShoot(playerID string, angle, power float64, spin *protocol.Vector3) int {
	g.ShotNumber++
	g.Phase = protocol.PhaseMoving
	g.BallInHand = false
	g.KitchenOnly = false

	// The cue ball starts moving; mark it so a mid-shot snapshot is coherent.
	cue := &g.Balls[protocol.CueBallID]
	cue.IsMoving = true
	speed := protocol.SpeedForPower(power)
	// Direction is derived on the client from cueAngle; the server keeps the
	// scalar speed only, which is enough for snapshot plausibility checks.
	cue.Velocity = protocol.Vector3{X: speed * math.Cos(angle), Y: 0, Z: speed * math.Sin(angle)}
	_ = spin // english is relayed verbatim; not modelled server-side yet
	return g.ShotNumber
}

// ApplyPlacement commits a ball-in-hand cue ball position and opens aiming.
// Callers must have run ValidatePlacement first.
func (g *Game) ApplyPlacement(pos protocol.Vector3) {
	cue := &g.Balls[protocol.CueBallID]
	cue.Position = pos
	cue.Velocity = protocol.Vector3{}
	cue.AngularVelocity = protocol.Vector3{}
	cue.InPocket = false
	cue.OutOfBounds = false
	cue.IsMoving = false
	g.BallInHand = false
	g.KitchenOnly = false
	g.Phase = protocol.PhaseAiming
}

// ---------------------------------------------------------------------------
// Arbitration
// ---------------------------------------------------------------------------

// ApplyShotResult commits the reported physical state and arbitrates the shot.
// It is the authoritative implementation of GAME_RULES.md.
func (g *Game) ApplyShotResult(rep ShotReport) (*protocol.StrikeResult, error) {
	shooter := g.PlayerByID(rep.ShooterID)
	if shooter == nil {
		return nil, ErrUnknownPlayer
	}
	opponent := g.Opponent(rep.ShooterID)
	if opponent == nil {
		return nil, ErrUnknownPlayer
	}

	g.Phase = protocol.PhaseResolving

	// Facts that must be evaluated BEFORE the new pocketed balls are committed.
	clearedBefore := g.GroupCleared(shooter)

	g.commitBalls(rep)

	cuePocketed := containsInt(rep.PocketedBalls, protocol.CueBallID)
	cueOOB := containsInt(rep.OutOfBoundsBalls, protocol.CueBallID)
	blackPocketed := containsInt(rep.PocketedBalls, protocol.EightBallID)
	blackOOB := containsInt(rep.OutOfBoundsBalls, protocol.EightBallID)

	res := &protocol.StrikeResult{
		StrikePlayerID:   rep.ShooterID,
		FirstContactBall: rep.FirstContactBall,
		GameStatus:       protocol.GameStatusPlaying,
		BallType:         shooter.Group.Wire(),
	}

	foul := g.detectFoul(shooter, rep, clearedBefore, cuePocketed, cueOOB)

	// --- terminal 8-ball outcomes (GAME_RULES.md §胜负判定) -----------------
	if blackOOB {
		return g.loseWith(res, shooter, opponent,
			protocol.FoulBlackOutOfBounds, protocol.ReasonEightBallOutOfTable), nil
	}
	if blackPocketed {
		if cuePocketed || cueOOB {
			return g.loseWith(res, shooter, opponent,
				protocol.FoulBlackWithCue, protocol.ReasonIllegalEightBall), nil
		}

		// Rule #4: 开球黑8豁免 - 开球进黑8不算输，重新开
		if g.IsBreakShot {
			g.resetCueBall()
			g.CurrentTurn = opponent.ID
			g.BallInHand = true
			g.KitchenOnly = g.opts.KitchenOnlyBallInHand
			g.Phase = protocol.PhaseBallInHand
			res.FoulMessage = "开球进黑8，本杆作废"
			res.NextPhase = g.Phase
			res.NextPlayerID = g.CurrentTurn
			res.BallInHand = g.BallInHand
			res.KitchenOnly = g.KitchenOnly
			return res, nil
		}

		legal := shooter.Group != GroupUnassigned &&
			clearedBefore &&
			rep.FirstContactBall == protocol.EightBallID &&
			foul == protocol.FoulNone &&
			lastInt(rep.PocketedBalls) == protocol.EightBallID

		// Rule #5: 黑8叫袋 - 进黑8必须指定袋口
		if legal && rep.DeclaredPocket == nil {
			// 进黑8但未指定袋口，拒绝
			return g.loseWith(res, shooter, opponent,
				protocol.FoulWrongBall, protocol.ReasonIllegalEightBall), nil
		}

		if legal {
			g.finish(shooter, opponent, protocol.ReasonLegalEightBall)
			res.GameStatus = g.GameStatus
			res.WinnerID = g.WinnerID
			res.NextPhase = protocol.PhaseGameOver
			res.Reason = protocol.ReasonLegalEightBall
			res.BallType = shooter.Group.Wire()
			return res, nil
		}
		return g.loseWith(res, shooter, opponent,
			protocol.FoulBlackPocketedEarly, protocol.ReasonIllegalEightBall), nil
	}

	// --- group assignment on an open table (GAME_RULES.md §进球有效性检查 #2)
	if shooter.Group == GroupUnassigned &&
		(foul == protocol.FoulNone || g.opts.AssignGroupOnFoul) {
		if gr := firstAssignableGroup(rep.PocketedBalls); gr != GroupUnassigned {
			shooter.Group = gr
			opponent.Group = gr.Other()
			res.GroupAssigned = true
		}
	}
	res.BallType = shooter.Group.Wire()

	// --- continue or pass the turn -----------------------------------------
	pottedAny, pottedOwn := false, false
	for _, id := range rep.PocketedBalls {
		if id == protocol.CueBallID || id == protocol.EightBallID {
			continue
		}
		pottedAny = true
		if shooter.Group != GroupUnassigned && GroupOf(id) == shooter.Group {
			pottedOwn = true
		}
	}

	continues := false
	if foul == protocol.FoulNone {
		if g.opts.ContinueOnAnyPot {
			continues = pottedAny
		} else {
			continues = pottedOwn
		}
	}

	if foul != protocol.FoulNone {
		res.FoulType = &foul
		res.FoulMessage = FoulMessage(foul)
	}
	res.IsContinuing = continues

	if continues {
		g.CurrentTurn = shooter.ID
	} else {
		g.CurrentTurn = opponent.ID
	}
	res.NextPlayerID = g.CurrentTurn

	// --- free ball / ball-in-hand (GAME_RULES.md §犯规结果) -----------------
	needBallInHand := foul != protocol.FoulNone &&
		(cuePocketed || cueOOB || g.opts.BallInHandOnAnyFoul)

	if needBallInHand {
		g.resetCueBall()
		g.BallInHand = true
		// Rule #6: 全场自由球 - 犯规后可在全场任意位置摆球（不限 kitchen）
		// 或根据 kitchenOnlyBallInHand 选项决定是否限制在 kitchen
		g.KitchenOnly = g.opts.KitchenOnlyBallInHand
		g.Phase = protocol.PhaseBallInHand
	} else {
		g.BallInHand = false
		g.KitchenOnly = false
		g.Phase = protocol.PhaseAiming
	}
	res.BallInHand = g.BallInHand
	res.KitchenOnly = g.KitchenOnly
	res.NextPhase = g.Phase

	g.IsBreakShot = false
	return res, nil
}

// ApplyTurnTimeout turns an expired turn into a foul and passes the turn on.
func (g *Game) ApplyTurnTimeout(playerID, foulCode string) (*protocol.StrikeResult, error) {
	shooter := g.PlayerByID(playerID)
	if shooter == nil {
		return nil, ErrUnknownPlayer
	}
	opponent := g.Opponent(playerID)
	if opponent == nil {
		return nil, ErrUnknownPlayer
	}

	g.stopAllBalls()
	code := foulCode
	g.CurrentTurn = opponent.ID
	g.IsBreakShot = false

	if g.opts.BallInHandOnAnyFoul {
		g.resetCueBall()
		g.BallInHand = true
		g.KitchenOnly = g.opts.KitchenOnlyBallInHand
		g.Phase = protocol.PhaseBallInHand
	} else {
		g.BallInHand = false
		g.KitchenOnly = false
		g.Phase = protocol.PhaseAiming
	}

	return &protocol.StrikeResult{
		StrikePlayerID: playerID,
		BallType:       shooter.Group.Wire(),
		FoulType:       &code,
		FoulMessage:    FoulMessage(code),
		IsContinuing:   false,
		GameStatus:     protocol.GameStatusPlaying,
		NextPlayerID:   g.CurrentTurn,
		NextPhase:      g.Phase,
		BallInHand:     g.BallInHand,
		KitchenOnly:    g.KitchenOnly,
	}, nil
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

// detectFoul evaluates the foul conditions of GAME_RULES.md §犯规规则 in
// descending order of severity and returns the winning foul code (or FoulNone).
func (g *Game) detectFoul(shooter *Player, rep ShotReport, clearedBefore, cuePocketed, cueOOB bool) string {
	switch {
	case cuePocketed:
		return protocol.FoulCueBallPocketed
	case cueOOB:
		return protocol.FoulCueBallOutOfBounds
	case !rep.CueBallMoved:
		return protocol.FoulNoShot
	case rep.FirstContactBall == protocol.CueBallID:
		// 0 means "nothing was touched".
		return protocol.FoulNoContact
	}

	// Rule #3: 碰库犯规 - 必须先碰自球，再碰库或进球
	if len(rep.PocketedBalls) == 0 && !rep.CushionAfterContact {
		return protocol.FoulNoBankContact
	}

	switch GroupOf(rep.FirstContactBall) {
	case GroupBlack:
		// Touching the 8 first is only legal once your own group is cleared.
		if !clearedBefore {
			return protocol.FoulWrongBall
		}
	case GroupSolid, GroupStripe:
		// On an open table any object ball except the 8 is a legal first hit.
		if shooter.Group != GroupUnassigned && GroupOf(rep.FirstContactBall) != shooter.Group {
			return protocol.FoulWrongBall
		}
	default:
		return protocol.FoulNoContact
	}
	return protocol.FoulNone
}

// loseWith terminates the game against the shooter and fills in the verdict.
func (g *Game) loseWith(res *protocol.StrikeResult, shooter, opponent *Player, foul, reason string) *protocol.StrikeResult {
	g.finish(opponent, shooter, reason)
	code := foul
	res.FoulType = &code
	res.FoulMessage = FoulMessage(code)
	res.IsContinuing = false
	res.GameStatus = g.GameStatus
	res.WinnerID = g.WinnerID
	res.NextPhase = protocol.PhaseGameOver
	res.NextPlayerID = ""
	res.Reason = reason
	return res
}

// commitBalls makes the reported physical state authoritative. Pocketed and
// out-of-bounds flags are monotonic: a ball can never come back into play
// except the cue ball, which is respotted explicitly.
func (g *Game) commitBalls(rep ShotReport) {
	for _, rb := range rep.FinalBalls {
		if !protocol.IsValidBallID(rb.BallID) {
			continue
		}
		cur := &g.Balls[rb.BallID]
		cur.Position = rb.Position
		cur.Velocity = protocol.Vector3{}
		cur.AngularVelocity = protocol.Vector3{}
		cur.IsMoving = false
		cur.InPocket = cur.InPocket || rb.InPocket
		cur.OutOfBounds = cur.OutOfBounds || rb.OutOfBounds
	}
	for _, id := range rep.PocketedBalls {
		if protocol.IsValidBallID(id) {
			g.Balls[id].InPocket = true
			g.Balls[id].IsMoving = false
		}
	}
	for _, id := range rep.OutOfBoundsBalls {
		if protocol.IsValidBallID(id) {
			g.Balls[id].OutOfBounds = true
			g.Balls[id].IsMoving = false
		}
	}
}

// resetCueBall respots the cue ball on the head spot, ready to be placed.
func (g *Game) resetCueBall() {
	cue := &g.Balls[protocol.CueBallID]
	cue.Position = protocol.Vector3{X: protocol.HeadStringX, Y: 0, Z: 0}
	cue.Velocity = protocol.Vector3{}
	cue.AngularVelocity = protocol.Vector3{}
	cue.InPocket = false
	cue.OutOfBounds = false
	cue.IsMoving = false
}

func (g *Game) stopAllBalls() {
	for i := range g.Balls {
		g.Balls[i].Velocity = protocol.Vector3{}
		g.Balls[i].AngularVelocity = protocol.Vector3{}
		g.Balls[i].IsMoving = false
	}
}

// FoulMessage returns a display string for a foul code (Chinese, for the HUD).
func FoulMessage(code string) string {
	switch code {
	case protocol.FoulNoContact:
		return "白球未碰到任何球"
	case protocol.FoulWrongBall:
		return "先碰到了非己方球"
	case protocol.FoulCueBallPocketed:
		return "白球进袋"
	case protocol.FoulCueBallOutOfBounds:
		return "白球飞出台面"
	case protocol.FoulBlackPocketedEarly:
		return "非法击落8号球"
	case protocol.FoulBlackWithCue:
		return "8号球与白球同时进袋"
	case protocol.FoulBlackOutOfBounds:
		return "8号球飞出台面"
	case protocol.FoulNoShot:
		return "白球未移动"
	case protocol.FoulTurnTimeout:
		return "出杆超时"
	case protocol.FoulShotTimeout:
		return "结算超时，本杆作废"
	case protocol.FoulIllegalReport:
		return "击球结果校验失败"
	case protocol.FoulNoBankContact:
		return "未碰库边也未进球，犯规"
	default:
		return ""
	}
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func lastInt(xs []int) int {
	if len(xs) == 0 {
		return -1
	}
	return xs[len(xs)-1]
}

// firstAssignableGroup returns the group of the first pocketed object ball that
// is neither the cue ball nor the 8.
func firstAssignableGroup(pocketed []int) Group {
	for _, id := range pocketed {
		switch GroupOf(id) {
		case GroupSolid:
			return GroupSolid
		case GroupStripe:
			return GroupStripe
		}
	}
	return GroupUnassigned
}
