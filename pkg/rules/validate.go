package rules

import (
	"math"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// minSeparationFactor is how close two resting balls may be reported before the
// server treats the frame as fabricated. Physics keeps balls at >= 2r apart, so
// 0.75 * 2r leaves room for float drift while still catching teleports.
const minSeparationFactor = 0.75

// cueMoveEpsilon is the minimum displacement (metres) the cue ball must show
// when the client claims CueBallMoved. 1mm is far below any real stroke at the
// minimum cue speed (1.5 m/s) but comfortably above float noise.
const cueMoveEpsilon = 0.001

// ValidateShoot checks a SHOOT intent against the authoritative state. It is
// the first anti-cheat gate: turn order, phase and parameter ranges.
func (g *Game) ValidateShoot(playerID string, angle, power float64, spin *protocol.Vector3) error {
	if err := g.requireActiveTurn(playerID); err != nil {
		return err
	}
	if g.Phase != protocol.PhaseAiming {
		if g.Phase == protocol.PhaseBallInHand {
			return protocol.Errf(protocol.ErrNotBallInHand,
				"必须先摆放白球（当前阶段 %s）", g.Phase)
		}
		return protocol.Errf(protocol.ErrInvalidPhase,
			"当前阶段 %s 不接受出杆", g.Phase)
	}
	if math.IsNaN(angle) || math.IsInf(angle, 0) {
		return protocol.Errf(protocol.ErrInvalidShot, "cueAngle 非法")
	}
	if math.IsNaN(power) || math.IsInf(power, 0) || power <= 0 || power > 1 {
		return protocol.Errf(protocol.ErrInvalidShot, "power 必须在 (0,1] 区间，收到 %v", power)
	}
	if spin != nil {
		for _, c := range []float64{spin.X, spin.Y, spin.Z} {
			if math.IsNaN(c) || math.IsInf(c, 0) || math.Abs(c) > 1 {
				return protocol.Errf(protocol.ErrInvalidShot, "spin 分量必须在 [-1,1] 区间")
			}
		}
	}
	return nil
}

// ValidatePlacement checks a CUE_BALL_PLACEMENT request.
func (g *Game) ValidatePlacement(playerID string, pos protocol.Vector3) error {
	if err := g.requireActiveTurn(playerID); err != nil {
		return err
	}
	// 【2026-09-01 修复】用户拖球期间 BallInHand=true，应该接受摆球位置
	// 无论当前 Phase 是什么（可能已经变成 Aiming 了），只要 BallInHand=true 就说明用户还在拖
	if !g.BallInHand {
		return protocol.Errf(protocol.ErrNotBallInHand,
			"当前不允许摆放白球（BallInHand=false）")
	}
	if math.IsNaN(pos.X) || math.IsNaN(pos.Z) ||
		math.IsInf(pos.X, 0) || math.IsInf(pos.Z, 0) {
		return protocol.Errf(protocol.ErrInvalidPlacement, "position 非法")
	}
	if g.KitchenOnly {
		if !protocol.InsideKitchen(pos) {
			return protocol.Errf(protocol.ErrInvalidPlacement,
				"白球必须摆在开球线后区域 (x ∈ [%.3f, %.3f], |z| ≤ %.3f)",
				-protocol.LimitX, protocol.HeadStringX, protocol.LimitZ)
		}
	} else if !protocol.InsideTable(pos) {
		return protocol.Errf(protocol.ErrInvalidPlacement, "白球必须摆在台面内")
	}

	minDist := 2 * protocol.BallRadius
	for id := 1; id <= protocol.MaxBallID; id++ {
		b := g.Balls[id]
		if offTable(b) {
			continue
		}
		if protocol.Distance2D(pos, b.Position) < minDist {
			return protocol.Errf(protocol.ErrInvalidPlacement,
				"白球与 %d 号球重叠，请换个位置", id)
		}
	}
	return nil
}

// ValidateShotResult is the main anti-cheat gate for the client-simulated
// outcome. It rejects reports that are physically impossible or inconsistent
// with the authoritative pre-shot state.
func (g *Game) ValidateShotResult(playerID string, rep ShotReport) error {
	if g.Finished() {
		return protocol.Errf(protocol.ErrGameFinished, "对局已结束")
	}
	if g.Phase != protocol.PhaseMoving {
		return protocol.Errf(protocol.ErrInvalidPhase,
			"当前阶段 %s 不接受击球结果", g.Phase)
	}
	if playerID != g.CurrentTurn {
		return protocol.Errf(protocol.ErrNotYourTurn,
			"只有出杆方可以上报击球结果")
	}
	if rep.ShotNumber != g.ShotNumber {
		return protocol.Errf(protocol.ErrDuplicateShot,
			"shotNumber 不匹配：服务端 %d，收到 %d", g.ShotNumber, rep.ShotNumber)
	}
	if !protocol.IsValidBallID(rep.FirstContactBall) {
		return protocol.Errf(protocol.ErrInvalidShotResult,
			"firstContactBall %d 非法", rep.FirstContactBall)
	}
	if len(rep.FinalBalls) != protocol.BallCount {
		return protocol.Errf(protocol.ErrInvalidShotResult,
			"ballStates 必须包含 %d 个球，收到 %d", protocol.BallCount, len(rep.FinalBalls))
	}

	// --- ball ids must be a complete, duplicate-free 0..15 set --------------
	var seen [protocol.BallCount]bool
	states := make([]protocol.BallState, protocol.BallCount)
	for _, rb := range rep.FinalBalls {
		if !protocol.IsValidBallID(rb.BallID) {
			return protocol.Errf(protocol.ErrInvalidShotResult, "ballId %d 非法", rb.BallID)
		}
		if seen[rb.BallID] {
			return protocol.Errf(protocol.ErrInvalidShotResult, "ballId %d 重复上报", rb.BallID)
		}
		seen[rb.BallID] = true
		states[rb.BallID] = rb
	}
	for id, ok := range seen {
		if !ok {
			return protocol.Errf(protocol.ErrInvalidShotResult, "缺少 %d 号球的状态", id)
		}
	}

	// --- pocketed / out-of-bounds lists must be new and duplicate-free ------
	if err := g.validateEventList(rep.PocketedBalls, "pocketedBalls", func(b protocol.BallState) bool {
		return b.InPocket
	}); err != nil {
		return err
	}
	if err := g.validateEventList(rep.OutOfBoundsBalls, "outOfBoundsBalls", func(b protocol.BallState) bool {
		return b.OutOfBounds
	}); err != nil {
		return err
	}

	// --- per-ball plausibility --------------------------------------------
	for id := 0; id < protocol.BallCount; id++ {
		rb := states[id]
		prev := g.Balls[id]

		// Monotonicity: a ball that is already down can never come back.
		if prev.InPocket && !rb.InPocket {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球已进袋，不能回到台面", id)
		}
		if prev.OutOfBounds && !rb.OutOfBounds {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球已出台，不能回到台面", id)
		}
		// Newly-down balls must appear in the corresponding event list.
		if rb.InPocket && !prev.InPocket && !containsInt(rep.PocketedBalls, id) {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球标记为进袋但未出现在 pocketedBalls 中", id)
		}
		if rb.OutOfBounds && !prev.OutOfBounds && !containsInt(rep.OutOfBoundsBalls, id) {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球标记为出台但未出现在 outOfBoundsBalls 中", id)
		}
		if offTable(rb) {
			continue
		}
		// Balls still in play must have come to rest inside the table.
		if rb.IsMoving || protocol.Speed(rb.Velocity) > protocol.RestSpeedEpsilon {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球尚未静止，不能作为结算状态", id)
		}
		if !protocol.InsideTable(rb.Position) {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%d 号球最终位置越界 (x=%.3f, z=%.3f)", id, rb.Position.X, rb.Position.Z)
		}
	}

	// --- no two resting balls may overlap ---------------------------------
	minDist := 2 * protocol.BallRadius * minSeparationFactor
	for a := 0; a < protocol.BallCount; a++ {
		if offTable(states[a]) {
			continue
		}
		for b := a + 1; b < protocol.BallCount; b++ {
			if offTable(states[b]) {
				continue
			}
			if protocol.Distance2D(states[a].Position, states[b].Position) < minDist {
				return protocol.Errf(protocol.ErrInvalidShotResult,
					"%d 号球与 %d 号球位置重叠", a, b)
			}
		}
	}

	// --- first contact must be a ball that was actually on the table -------
	if rep.FirstContactBall != protocol.CueBallID {
		if offTable(g.Balls[rep.FirstContactBall]) {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"firstContactBall %d 在本杆开始前已不在台面上", rep.FirstContactBall)
		}
	} else if rep.CueBallMoved && len(rep.PocketedBalls) > 0 {
		// Nothing was hit yet balls dropped: impossible.
		return protocol.Errf(protocol.ErrInvalidShotResult,
			"未发生碰撞却有球进袋")
	}

	// --- "cue ball moved" must be backed by an actual displacement ---------
	// Minimum cue speed is PowerMinSpeed (1.5 m/s), so a claimed move with a
	// sub-millimetre delta is a fabricated report trying to dodge NO_SHOT.
	if rep.CueBallMoved &&
		!offTable(states[protocol.CueBallID]) &&
		protocol.Distance2D(states[protocol.CueBallID].Position, g.Balls[protocol.CueBallID].Position) < cueMoveEpsilon {
		return protocol.Errf(protocol.ErrInvalidShotResult,
			"cueBallMoved=true 但白球位置未变化")
	}
	return nil
}

// ValidateStateFrame guards the relayed 20Hz frames.
func (g *Game) ValidateStateFrame(playerID string, shotNumber int, states []protocol.BallState) error {
	if g.Phase != protocol.PhaseMoving {
		return protocol.Errf(protocol.ErrInvalidPhase, "非运动阶段不接受状态帧")
	}
	if playerID != g.CurrentTurn {
		return protocol.Errf(protocol.ErrNotYourTurn, "只有出杆方可以广播状态帧")
	}
	if shotNumber != g.ShotNumber {
		return protocol.Errf(protocol.ErrDuplicateShot,
			"状态帧 shotNumber 过期（服务端 %d）", g.ShotNumber)
	}
	if len(states) == 0 || len(states) > protocol.BallCount {
		return protocol.Errf(protocol.ErrInvalidShotResult, "ballStates 数量非法")
	}
	for _, s := range states {
		if !protocol.IsValidBallID(s.BallID) {
			return protocol.Errf(protocol.ErrInvalidShotResult, "ballId %d 非法", s.BallID)
		}
	}
	return nil
}

// validateEventList checks a pocketed/out-of-bounds id list for validity,
// duplicates and "already happened" entries.
func (g *Game) validateEventList(ids []int, field string, already func(protocol.BallState) bool) error {
	if len(ids) > protocol.BallCount {
		return protocol.Errf(protocol.ErrInvalidShotResult, "%s 数量非法", field)
	}
	var seen [protocol.BallCount]bool
	for _, id := range ids {
		if !protocol.IsValidBallID(id) {
			return protocol.Errf(protocol.ErrInvalidShotResult, "%s 含非法球号 %d", field, id)
		}
		if seen[id] {
			return protocol.Errf(protocol.ErrInvalidShotResult, "%s 含重复球号 %d", field, id)
		}
		seen[id] = true
		if already(g.Balls[id]) {
			return protocol.Errf(protocol.ErrInvalidShotResult,
				"%s 含本杆之前就已离台的 %d 号球", field, id)
		}
	}
	return nil
}

// requireActiveTurn is the shared turn/phase precondition.
func (g *Game) requireActiveTurn(playerID string) error {
	if g.Finished() {
		return protocol.Errf(protocol.ErrGameFinished, "对局已结束")
	}
	if g.Phase == protocol.PhaseWaiting {
		return protocol.Errf(protocol.ErrGameNotStarted, "对局尚未开始")
	}
	if g.PlayerByID(playerID) == nil {
		return protocol.Errf(protocol.ErrNotInRoom, "玩家不在本对局中")
	}
	if playerID != g.CurrentTurn {
		return protocol.Errf(protocol.ErrNotYourTurn, "未到你的回合")
	}
	return nil
}
