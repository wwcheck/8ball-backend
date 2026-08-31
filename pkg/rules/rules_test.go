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

// TestRule6FullTableFreeBall 测试规则 #6：全场自由球
func TestRule6FullTableFreeBall(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false

	g.Balls = NewRack()

	// 模拟犯规
	rep := ShotReport{
		ShooterID:           p1.ID,
		ShotNumber:          1,
		FirstContactBall:    9, // 碰到花色球（犯规）
		PocketedBalls:       []int{},
		CueBallMoved:        true,
		CushionAfterContact: false,
		FinalBalls:          g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Errorf("ApplyShotResult failed: %v", err)
		return
	}

	// 应该进入 BallInHand 阶段
	if res.NextPhase != protocol.PhaseBallInHand {
		t.Errorf("expected BallInHand phase, got %s", res.NextPhase)
	}

	// 应该激活 BallInHand
	if !res.BallInHand {
		t.Errorf("expected BallInHand to be true")
	}

	// KitchenOnly 取决于选项（此处默认为 false 表示全场自由球）
	// 根据选项配置验证
	if g.opts.KitchenOnlyBallInHand {
		if !res.KitchenOnly {
			t.Errorf("expected KitchenOnly when option is set")
		}
	} else {
		if res.KitchenOnly {
			t.Errorf("expected full-table free ball when option is not set")
		}
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
