package rules

import (
	"testing"

	"github.com/yourgame/8ball-backend/pkg/protocol"
)

// TestRule3BankContact 测试规则 #3：碰库犯规
func TestRule3BankContact(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	p2, _ := g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	p2.Group = GroupStripe
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false

	// 测试：未碰库且未进球 -> NO_BANK_CONTACT 犯规
	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    1,       // 全色球
		PocketedBalls:       []int{}, // 无进球
		CueBallMoved:        true,
		CushionAfterContact: false, // 未碰库
	}

	foul := g.detectFoul(p1, rep, false, false, false)
	if foul != protocol.FoulNoBankContact {
		t.Errorf("expected FoulNoBankContact, got %s", foul)
	}

	// 测试：碰库->合法
	rep.CushionAfterContact = true
	foul = g.detectFoul(p1, rep, false, false, false)
	if foul != protocol.FoulNone {
		t.Errorf("expected FoulNone with bank contact, got %s", foul)
	}

	// 测试：进球->合法（即使未碰库）
	rep.CushionAfterContact = false
	rep.PocketedBalls = []int{1}
	foul = g.detectFoul(p1, rep, false, false, false)
	if foul != protocol.FoulNone {
		t.Errorf("expected FoulNone with pocketed ball, got %s", foul)
	}
}

// TestRule4OpeningBreakBlackEight 测试规则 #4：开球黑8豁免
func TestRule4OpeningBreakBlackEight(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	p2, _ := g.AddPlayer("p2", "Player2", "")
	g.CurrentTurn = p1.ID
	g.IsBreakShot = true

	// 初始化球的状态
	g.Balls = NewRack()

	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    2,
		PocketedBalls:       []int{8}, // 开球进黑8
		CueBallMoved:        true,
		CushionAfterContact: true,
		FinalBalls:          g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	// 开球进黑8不应该立即结束游戏，应该重新开
	if res.GameStatus != protocol.GameStatusPlaying {
		t.Errorf("game should still be playing, but got status %s", res.GameStatus)
	}

	// 应该转移到对手，且进入 BallInHand 阶段
	if res.NextPlayerID != p2.ID {
		t.Errorf("expected next player to be p2, got %s", res.NextPlayerID)
	}

	if res.NextPhase != protocol.PhaseBallInHand {
		t.Errorf("expected BallInHand phase, got %s", res.NextPhase)
	}
}

// TestRule5BlackEightDeclaredPocket 测试规则 #5：黑8叫袋
func TestRule5BlackEightDeclaredPocket(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false

	// 模拟所有己方球都进了，现在要进黑8
	// 标记所有全色球已进
	for i := 1; i <= 7; i++ {
		g.Balls[i].InPocket = true
	}

	g.Balls = NewRack()
	for i := 1; i <= 7; i++ {
		g.Balls[i].InPocket = true
	}

	// 测试：进黑8但未指定袋口 -> 拒绝
	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    8,
		PocketedBalls:       []int{8},
		CueBallMoved:        true,
		CushionAfterContact: true,
		DeclaredPocket:      nil, // 未指定袋口
		FinalBalls:          g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	// 应该拒绝，游戏结束，对手胜利
	if res.GameStatus == protocol.GameStatusPlaying {
		t.Errorf("expected game to end, but status is still playing")
	}

	if res.WinnerID != "" && res.WinnerID == p1.ID {
		t.Errorf("p1 should not win without declaring pocket, but got winner %s", res.WinnerID)
	}

	// 测试：指定袋口后胜利
	g = NewGame(DefaultOptions())
	p1, _ = g.AddPlayer("p1", "Player1", "")
	g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false
	g.Balls = NewRack()
	for i := 1; i <= 7; i++ {
		g.Balls[i].InPocket = true
	}

	declaredPocket := 0 // 指定袋口号
	rep.DeclaredPocket = &declaredPocket
	rep.FinalBalls = g.Balls[:]

	res, err = g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	// 应该胜利
	if res.GameStatus != protocol.GameStatusP1Wins && res.GameStatus != protocol.GameStatusP2Wins {
		t.Errorf("game should be finished, but status is %s", res.GameStatus)
	}
}

// TestFoulBallInHandOnlyWhenCueBallPocketed pins down GAME_RULES.md §犯规结果:
//
//	所有犯规的共同结果：
//	  1. 换人 (SwitchPlayer)
//	  2. 白球进袋时：对手进入摆放模式，在开球线后区域（kitchen）摆放白球
//	  3. 重置本轮数据
//
// So: every foul passes the turn, but ball-in-hand is granted ONLY when the cue
// ball is pocketed or leaves the table — and even then placement is confined to
// the kitchen, never the whole table (§1 白球进袋: "非全场任意位置").
//
// This replaces an earlier TestRule6FullTableFreeBall which asserted a
// full-table free ball on *any* foul. GAME_RULES.md has no "rule #6" (fouls are
// numbered 1-5) and explicitly rules out full-table placement, so the test was
// inventing a rule rather than testing one. Per the product decision the
// document wins and the test was corrected.
func TestFoulBallInHandOnlyWhenCueBallPocketed(t *testing.T) {
	// --- Case 1: wrong ball first, cue ball stays on the table -------------
	// Expect the foul to be recorded and the turn to pass, but NO ball-in-hand.
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	p2, _ := g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false
	g.Balls = NewRack()

	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    9, // a stripe while p1 is on solids -> FoulWrongBall
		PocketedBalls:       []int{},
		CueBallMoved:        true,
		CushionAfterContact: true, // legal cushion contact; the wrong ball is the only foul
		FinalBalls:          g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Fatalf("ApplyShotResult failed: %v", err)
	}
	if res.FoulType == nil || *res.FoulType == protocol.FoulNone {
		t.Fatalf("expected a foul for contacting a stripe first, got %v", res.FoulType)
	}
	if res.BallInHand {
		t.Errorf("foul without a pocketed cue ball must not grant ball-in-hand")
	}
	if res.NextPhase == protocol.PhaseBallInHand {
		t.Errorf("foul without a pocketed cue ball must not enter BallInHand, got %s", res.NextPhase)
	}
	if res.NextPlayerID != p2.ID {
		t.Errorf("expected next player to be p2, got %s", res.NextPlayerID)
	}

	// --- Case 2: cue ball pocketed -----------------------------------------
	// Expect ball-in-hand for the opponent, restricted to the kitchen.
	g = NewGame(DefaultOptions())
	p1, _ = g.AddPlayer("p1", "Player1", "")
	p2, _ = g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false
	g.Balls = NewRack()

	// A real client reports the settled table with the cue ball already down.
	final := g.Balls
	final[protocol.CueBallID].InPocket = true

	rep = ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    1, // legal first contact (a solid)
		PocketedBalls:       []int{protocol.CueBallID},
		CueBallMoved:        true,
		CushionAfterContact: true,
		FinalBalls:          final[:],
	}

	res, err = g.ApplyShotResult(rep)
	if err != nil {
		t.Fatalf("ApplyShotResult (cue ball pocketed) failed: %v", err)
	}
	if !res.BallInHand {
		t.Errorf("pocketing the cue ball must grant ball-in-hand")
	}
	if res.NextPhase != protocol.PhaseBallInHand {
		t.Errorf("expected BallInHand phase, got %s", res.NextPhase)
	}
	if res.NextPlayerID != p2.ID {
		t.Errorf("expected ball-in-hand to go to p2, got %s", res.NextPlayerID)
	}
	// §1 白球进袋: placement is behind the head string, "非全场任意位置".
	if g.opts.KitchenOnlyBallInHand && !res.KitchenOnly {
		t.Errorf("placement must be confined to the kitchen when the option is set")
	}
}

// TestRule7OpenTableBallGroup 测试规则 #7：开放球台分组
func TestRule7OpenTableBallGroup(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	p2, _ := g.AddPlayer("p2", "Player2", "")
	g.CurrentTurn = p1.ID
	g.IsBreakShot = true

	g.Balls = NewRack()

	// 测试：开球进全色球 -> p1 是全色
	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    1,
		PocketedBalls:       []int{1}, // 进全色球
		CueBallMoved:        true,
		CushionAfterContact: true,
		FinalBalls:          g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	if !res.GroupAssigned {
		t.Errorf("expected group to be assigned")
	}

	if p1.Group != GroupSolid {
		t.Errorf("expected p1 to be solid, got %v", p1.Group)
	}

	if p2.Group != GroupStripe {
		t.Errorf("expected p2 to be stripe, got %v", p2.Group)
	}

	// 测试：开球进花色球 -> p1 是花色
	g = NewGame(DefaultOptions())
	p1, _ = g.AddPlayer("p1", "Player1", "")
	p2, _ = g.AddPlayer("p2", "Player2", "")
	g.CurrentTurn = p1.ID
	g.IsBreakShot = true

	g.Balls = NewRack()

	rep.PocketedBalls = []int{9} // 进花色球
	rep.FinalBalls = g.Balls[:]

	res, err = g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	if p1.Group != GroupStripe {
		t.Errorf("expected p1 to be stripe, got %v", p1.Group)
	}

	if p2.Group != GroupSolid {
		t.Errorf("expected p2 to be solid, got %v", p2.Group)
	}

	// 测试：开球未进球 -> 开放球台，对方选择
	g = NewGame(DefaultOptions())
	p1, _ = g.AddPlayer("p1", "Player1", "")
	g.AddPlayer("p2", "Player2", "")
	g.CurrentTurn = p1.ID
	g.IsBreakShot = true

	g.Balls = NewRack()

	rep.PocketedBalls = []int{} // 未进球
	rep.FinalBalls = g.Balls[:]

	res, err = g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	if p1.Group != GroupUnassigned {
		t.Errorf("expected p1 group to remain unassigned")
	}
}
