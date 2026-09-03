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

// TestSpecialCase1BreakShotEightBallLoses pins down GAME_RULES.md §特殊情况 1
// 「开球后直接进黑8」:
//
//	if (开局 && 黑8进袋) { 输("开局进8号"); 对手赢() }
//
// 游戏立即结束，开球方失败、对手获胜。
//
// 这是 PROJECT RULE，与 WPA 官方口径相反：WPA 会判「本杆作废、重置白球、
// 换对手、进入 BallInHand 阶段」。本仓库曾用 TestRule4OpeningBreakBlackEight
// 断言过那一版 WPA 行为，现按产品决策（2026-08-31）以 GAME_RULES.md 为准，
// 该测试被本测试取代。
func TestSpecialCase1BreakShotEightBallLoses(t *testing.T) {
	cases := []struct {
		name         string
		pocketed     []int
		outOfBounds  []int
		wantFoul    string
		wantMessage string
	}{
		{
			name:        "开球进黑8，白球安全 -> 判负",
			pocketed:    []int{protocol.EightBallID},
			wantFoul:    protocol.FoulBlackPocketedEarly,
			wantMessage: "开球进黑8，判负",
		},
		{
			name:        "开球进黑8，白球同时进袋 -> 判负",
			pocketed:    []int{protocol.CueBallID, protocol.EightBallID},
			wantFoul:    protocol.FoulBlackWithCue,
			wantMessage: FoulMessage(protocol.FoulBlackWithCue),
		},
		{
			name:        "开球进黑8，白球同时出台 -> 判负",
			pocketed:    []int{protocol.EightBallID},
			outOfBounds: []int{protocol.CueBallID},
			wantFoul:    protocol.FoulBlackWithCue,
			wantMessage: FoulMessage(protocol.FoulBlackWithCue),
		},
		{
			// 文档内层条件 if (黑8是本轮第一个进球) 的边界：黑8不是本杆第一
			// 个进球时文档没有明说判什么。开球杆上双方分组未定，落到通用分支
			// 也会因为 legal == false 而判负，结论一致，这里把它固定下来。
			name:        "开球黑8不是本杆第一个进球 -> 仍判负",
			pocketed:    []int{1, protocol.EightBallID},
			wantFoul:    protocol.FoulBlackPocketedEarly,
			wantMessage: "开球进黑8，判负",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGame(DefaultOptions())
			p1, _ := g.AddPlayer("p1", "Player1", "")
			p2, _ := g.AddPlayer("p2", "Player2", "")
			g.CurrentTurn = p1.ID
			g.IsBreakShot = true
			g.Balls = NewRack()

			rep := ShotReport{
				ShooterID:           p1.ID,
				ShotNumber:          1,
				FirstContactBall:    2,
				PocketedBalls:       tc.pocketed,
				OutOfBoundsBalls:    tc.outOfBounds,
				CueBallMoved:        true,
				CushionAfterContact: true,
				FinalBalls:          g.Balls[:],
			}

			res, err := g.ApplyShotResult(rep)
			if err != nil {
				t.Fatalf("ApplyShotResult failed: %v", err)
			}

			// 游戏必须立即结束，开球方 p1 失败、对手 p2 获胜。
			if res.GameStatus == protocol.GameStatusPlaying {
				t.Fatalf("game must end on a break-shot 8-ball, got status %s", res.GameStatus)
			}
			if res.GameStatus != protocol.GameStatusP2Wins {
				t.Errorf("expected p2 (the opponent) to win, got status %s", res.GameStatus)
			}
			if res.WinnerID != p2.ID {
				t.Errorf("expected winner p2, got %q", res.WinnerID)
			}
			if g.LoserID != p1.ID {
				t.Errorf("expected loser p1, got %q", g.LoserID)
			}
			if res.Reason != protocol.ReasonIllegalEightBall {
				t.Errorf("expected reason %s, got %s", protocol.ReasonIllegalEightBall, res.Reason)
			}
			if res.NextPhase != protocol.PhaseGameOver {
				t.Errorf("expected GameOver phase, got %s", res.NextPhase)
			}

			// 判负不是「本杆作废」，绝不能进入 BallInHand / 换人。
			if res.NextPlayerID != "" {
				t.Errorf("a lost game must not schedule a next player, got %q", res.NextPlayerID)
			}
			if res.BallInHand {
				t.Errorf("a lost game must not grant ball-in-hand")
			}
			if !g.Finished() {
				t.Errorf("expected the game to be finished")
			}
			// 终局快照不能残留 IsBreakShot=true，否则客户端结算画面/观战态
			// 拿这个字段做判断时会走错分支。复位发生在 finish() 里，对所有
			// 终局路径（判负 / 合法获胜 / 认输 / 弃权）统一生效。
			if g.IsBreakShot {
				t.Errorf("a finished game must not report IsBreakShot = true")
			}
			if g.Snapshot().IsBreakShot {
				t.Errorf("the final snapshot must not report IsBreakShot = true")
			}

			if res.FoulType == nil {
				t.Fatalf("expected a foul code, got nil")
			}
			if *res.FoulType != tc.wantFoul {
				t.Errorf("expected foul %s, got %s", tc.wantFoul, *res.FoulType)
			}
			if res.FoulMessage != tc.wantMessage {
				t.Errorf("expected foul message %q, got %q", tc.wantMessage, res.FoulMessage)
			}
		})
	}
}

// TestNonBreakEightBallArbitrationUnchanged 是回归守卫：确认把开球进黑8改成
// 判负之后，「非开球时进黑8」的四条原有判定没有被破坏。
func TestNonBreakEightBallArbitrationUnchanged(t *testing.T) {
	// clearedGroup 为真时，先把 1-7 号（p1 的全色组）全部标记为已进袋，
	// 让 p1 处于「打完己方球、可以合法击打黑8」的状态。
	newGame := func(clearedGroup bool) (*Game, *Player, *Player) {
		g := NewGame(DefaultOptions())
		p1, _ := g.AddPlayer("p1", "Player1", "")
		p2, _ := g.AddPlayer("p2", "Player2", "")
		p1.Group = GroupSolid
		p2.Group = GroupStripe
		g.CurrentTurn = p1.ID
		g.IsBreakShot = false
		g.Balls = NewRack()
		if clearedGroup {
			for i := 1; i <= 7; i++ {
				g.Balls[i].InPocket = true
			}
		}
		return g, p1, p2
	}

	cases := []struct {
		name        string
		cleared     bool
		firstHit    int
		pocketed    []int
		declared    *int
		wantFoul    string
		wantWinner  int // 1 = p1 赢, 2 = p2 赢
		wantReason  string
		wantPlaying bool
	}{
		{
			name:        "己方球未清完就进黑8 -> 判负 (BLACK_POCKETED_EARLY)",
			cleared:     false,
			firstHit:    protocol.EightBallID,
			pocketed:    []int{protocol.EightBallID},
			wantFoul:    protocol.FoulBlackPocketedEarly,
			wantWinner:  2,
			wantReason:  protocol.ReasonIllegalEightBall,
			wantPlaying: false,
		},
		{
			name:        "清完己方球、先碰黑8、叫袋 -> p1 合法获胜",
			cleared:     true,
			firstHit:    protocol.EightBallID,
			pocketed:    []int{protocol.EightBallID},
			declared:    intPtr(0),
			wantWinner:  1,
			wantReason:  protocol.ReasonLegalEightBall,
			wantPlaying: false,
		},
		{
			name:        "清完己方球、进黑8但未叫袋 -> 判负",
			cleared:     true,
			firstHit:    protocol.EightBallID,
			pocketed:    []int{protocol.EightBallID},
			declared:    nil,
			wantFoul:    protocol.FoulWrongBall,
			wantWinner:  2,
			wantReason:  protocol.ReasonIllegalEightBall,
			wantPlaying: false,
		},
		{
			name:        "清完己方球、先碰黑8、但白球同时进袋 -> 判负 (BLACK_WITH_CUE)",
			cleared:     true,
			firstHit:    protocol.EightBallID,
			pocketed:    []int{protocol.CueBallID, protocol.EightBallID},
			declared:    intPtr(0),
			wantFoul:    protocol.FoulBlackWithCue,
			wantWinner:  2,
			wantReason:  protocol.ReasonIllegalEightBall,
			wantPlaying: false,
		},
		{
			// 黑8没进，只是先碰到了黑8（己方球已清完）-> 不犯规、不结束，
			// 未进球所以换人。这条最容易在改 blackPocketed 分支时被误伤。
			name:        "己方球清完后先碰黑8但没进 -> 普通犯规判定，游戏继续",
			cleared:     true,
			firstHit:    protocol.EightBallID,
			pocketed:    []int{},
			wantPlaying: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, p1, p2 := newGame(tc.cleared)

			rep := ShotReport{
				ShooterID:           p1.ID,
				ShotNumber:          2,
				FirstContactBall:    tc.firstHit,
				PocketedBalls:       tc.pocketed,
				CueBallMoved:        true,
				CushionAfterContact: true,
				DeclaredPocket:      tc.declared,
				FinalBalls:          g.Balls[:],
			}

			res, err := g.ApplyShotResult(rep)
			if err != nil {
				t.Fatalf("ApplyShotResult failed: %v", err)
			}

			wantWinner := p1.ID
			if tc.wantWinner == 2 {
				wantWinner = p2.ID
			}

			if tc.wantPlaying {
				if res.GameStatus != protocol.GameStatusPlaying {
					t.Fatalf("expected the game to continue, got status %s", res.GameStatus)
				}
				if res.NextPlayerID == "" {
					t.Errorf("expected a next player to be scheduled")
				}
				return
			}

			if res.GameStatus == protocol.GameStatusPlaying {
				t.Fatalf("expected the game to end, got status %s", res.GameStatus)
			}
			if res.WinnerID != wantWinner {
				t.Errorf("expected winner %s, got %q", wantWinner, res.WinnerID)
			}
			if res.Reason != tc.wantReason {
				t.Errorf("expected reason %s, got %s", tc.wantReason, res.Reason)
			}
			if tc.wantFoul == "" {
				if res.FoulType != nil && *res.FoulType != protocol.FoulNone {
					t.Errorf("expected no foul, got %s", *res.FoulType)
				}
				return
			}
			if res.FoulType == nil || *res.FoulType != tc.wantFoul {
				t.Errorf("expected foul %s, got %v", tc.wantFoul, res.FoulType)
			}
		})
	}
}

func intPtr(v int) *int { return &v }

// TestCompareShotResults pins down the P3 two-end consistency check:
// pocketed/out-of-bounds lists compare as sets (hard rule facts), ball positions
// compare with a fixed tolerance, and mismatches surface the affected balls.
func TestCompareShotResults(t *testing.T) {
	base := func() ShotReport {
		balls := make([]protocol.BallState, protocol.BallCount)
		for i := 0; i < protocol.BallCount; i++ {
			balls[i] = protocol.BallState{
				BallID:   i,
				Position: protocol.Vector3{X: float64(i) * 0.1, Z: float64(i) * 0.1},
			}
		}
		return ShotReport{
			ShotNumber:          1,
			FirstContactBall:    1,
			PocketedBalls:       []int{1, 3},
			CueBallMoved:        true,
			CushionAfterContact: true,
			FinalBalls:          balls,
		}
	}

	t.Run("一致无差异", func(t *testing.T) {
		if diffs := CompareShotResults(base(), base()); len(diffs) != 0 {
			t.Fatalf("expected no diffs, got %v", diffs)
		}
	})

	t.Run("进袋列表集合不一致被检出", func(t *testing.T) {
		observer := base()
		observer.PocketedBalls = []int{1}
		if diffs := CompareShotResults(base(), observer); len(diffs) == 0 {
			t.Fatalf("expected pocketed mismatch to be detected")
		}
	})

	t.Run("球位超出容差被检出", func(t *testing.T) {
		observer := base()
		observer.FinalBalls[5].Position.X += 0.01 // 10mm > 1mm tolerance
		if diffs := CompareShotResults(base(), observer); len(diffs) == 0 {
			t.Fatalf("expected position mismatch to be detected")
		}
	})

	t.Run("球位在容差内不算差异", func(t *testing.T) {
		observer := base()
		observer.FinalBalls[5].Position.X += 0.0005 // 0.5mm < 1mm tolerance
		if diffs := CompareShotResults(base(), observer); len(diffs) != 0 {
			t.Fatalf("expected no diffs within tolerance, got %v", diffs)
		}
	})
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

// TestRule5BlackEightWrongPocket 测试黑8进错袋的判负逻辑（叫对袋但进错袋）
func TestRule5BlackEightWrongPocket(t *testing.T) {
	g := NewGame(DefaultOptions())
	p1, _ := g.AddPlayer("p1", "Player1", "")
	p2, _ := g.AddPlayer("p2", "Player2", "")
	p1.Group = GroupSolid
	g.CurrentTurn = p1.ID
	g.IsBreakShot = false
	g.Balls = NewRack()
	for i := 1; i <= 7; i++ {
		g.Balls[i].InPocket = true
	}

	declaredPocket := 0 // p1 叫的是 0 号袋
	actualPocket := 3   // 但黑8实际进了 3 号袋

	rep := ShotReport{
		ShooterID:               p1.ID,
		ShotNumber:              2,
		FirstContactBall:        8,
		PocketedBalls:           []int{8},
		CueBallMoved:            true,
		CushionAfterContact:     true,
		DeclaredPocket:          &declaredPocket,
		ActualBlack8PocketIndex: &actualPocket,
		FinalBalls:              g.Balls[:],
	}

	res, err := g.ApplyShotResult(rep)
	if err != nil {
		t.Fatalf("ApplyShotResult failed: %v", err)
	}

	// 应该判负：叫对袋但进错袋
	if res.GameStatus == protocol.GameStatusPlaying {
		t.Fatalf("expected game to end, got status %s", res.GameStatus)
	}
	if res.WinnerID != p2.ID {
		t.Errorf("expected p2 (opponent) to win, got winner %s", res.WinnerID)
	}
	if res.FoulType == nil || *res.FoulType != protocol.FoulBlackWrongPocket {
		t.Errorf("expected foul %s, got %v", protocol.FoulBlackWrongPocket, res.FoulType)
	}
	if res.Reason != protocol.ReasonWrongPocket {
		t.Errorf("expected reason %s, got %s", protocol.ReasonWrongPocket, res.Reason)
	}
	if res.NextPhase != protocol.PhaseGameOver {
		t.Errorf("expected GameOver phase, got %s", res.NextPhase)
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
