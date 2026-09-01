package rules

import "github.com/yourgame/8ball-backend/pkg/protocol"

// Rack layout constants. The triangle apex points at the foot of the table
// (+X), the 8 ball sits in the middle of the third row, and rows are spaced by
// one ball diameter (GAME_RULES.md §初始排列).
const (
	rackApexX = 0.71                       // apex ball centre
	rowGap    = protocol.BallRadius * 1.74 // ~sqrt(3)*r spacing between rows
	colGap    = protocol.BallRadius * 2.0  // ball diameter within a row
)

// rackOrder is the ball id placed at each triangle slot, row by row.
// Row 0: apex (1 ball) ... Row 4: back row (5 balls).
// Slot 4 (row 2, centre) is the 8 ball; corners of the back row are one solid
// and one stripe, matching standard 8-ball racking conventions.
var rackOrder = [15]int{
	1,    // row 0
	9, 2, // row 1
	10, 8, 11, // row 2  (8 ball dead centre)
	3, 12, 4, 13, // row 3
	5, 14, 6, 15, 7, // row 4
}

// NewRack returns the authoritative opening ball layout: the cue ball on the
// head spot (GAME_RULES.md: x = -0.785, z = 0) and 15 racked object balls.
func NewRack() [protocol.BallCount]protocol.BallState {
	var balls [protocol.BallCount]protocol.BallState

	for id := 0; id < protocol.BallCount; id++ {
		// RotationW=1：初始摆位为单位四元数（数字朝上基准，与客户端 ResetBall 对齐）
		balls[id] = protocol.BallState{BallID: id, RotationW: 1}
	}

	// Cue ball on the head spot, inside the kitchen.
	balls[protocol.CueBallID].Position = protocol.Vector3{X: protocol.HeadStringX, Y: 0, Z: 0}

	idx := 0
	for row := 0; row < 5; row++ {
		x := rackApexX + float64(row)*rowGap
		// Centre each row on z = 0.
		startZ := -float64(row) * colGap / 2
		for col := 0; col <= row; col++ {
			ballID := rackOrder[idx]
			balls[ballID].Position = protocol.Vector3{
				X: x,
				Y: 0,
				Z: startZ + float64(col)*colGap,
			}
			idx++
		}
	}
	return balls
}
