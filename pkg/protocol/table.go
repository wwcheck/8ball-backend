package protocol

import "math"

// Table geometry (metres). X is the long axis of the table, Z the short one;
// the origin sits at the centre of the bed.
//
// Boundary* are the cushion faces (the playing surface rectangle). Limit* are
// the reachable limits for a *ball centre*, i.e. half a ball radius in from the
// cushions, and are what placement / settling checks should compare against.
const (
	BoundaryHalfX = 1.3  // cushion face, X (long axis)
	BoundaryHalfZ = 0.647 // cushion face, Z (short axis)
	LimitX        = 1.26  // max |x| for a ball centre = BoundaryHalfX - BallRadius
	LimitZ        = 0.607 // max |z| for a ball centre = BoundaryHalfZ - BallRadius

	BallRadius = 0.04 // ball radius (matches Unity's scaled-up ball)

	// HeadStringX is the head string: the cue ball must be placed behind it
	// when breaking / in the kitchen (GAME_RULES.md §初始排列: x = -0.785).
	HeadStringX = -0.785

	// Kitchen is the region between the head cushion and the head string.
	KitchenMinX = -LimitX     // -1.26
	KitchenMaxX = HeadStringX // -0.785
)

// Ball identity. IDs follow the game's numbering: 0 = cue, 1-7 = solids,
// 8 = black, 9-15 = stripes.
const (
	CueBallID   = 0
	EightBallID = 8
	MaxBallID   = 15
	BallCount   = 16 // 0..15
)

// Shot dynamics.
const (
	// PowerMinSpeed / PowerMaxSpeed bound the linear map from SHOOT.power
	// (0,1] to the initial cue ball speed in m/s.
	PowerMinSpeed = 1.5
	PowerMaxSpeed = 8.0

	// RestSpeedEpsilon is the speed below which a ball counts as stopped.
	RestSpeedEpsilon = 0.01
)

// PositionTolerance is the slack allowed when comparing reported positions
// against expected ones. It covers float drift only - NOT pocket capture.
//
// It used to be 0.12 to mask a client-side pocket-geometry bug (balls bouncing
// back out of pockets). The client now uses the corrected WPA pocket geometry,
// so it is tightened back to a genuine float-precision tolerance.
const PositionTolerance = 0.01

// Pocket geometry constants (WPA 9-foot table specification, all units: meters).
//
// These describe the corrected pocket shape: the cushions are split into
// segments, the pocket jaws are rounded instead of square, and a ball is
// captured once it crosses the mouth line. See 袋口几何重做方案.md.
const (
	CornerMouthWidth = 0.1596   // Corner pocket opening = 1.995 × ball diameter
	SideMouthWidth   = 0.1428   // Side pocket opening = 1.785 × ball diameter
	CornerCutX       = 0.112854 // Corner cushion cut point X offset
	CornerCutY       = 0.112854 // Corner cushion cut point Y offset (symmetric)
	SideCutHalfX     = 0.0714   // Side pocket cushion cut point half-width
	JawRadius        = 0.02     // Pocket jaw corner radius
	ThroatDepth      = 0.12     // Pocket throat depth
)

// Cushion segment boundaries (for pocket region calculation).
//
// Long rails are split in two by the side pockets: [-1.187146, -0.0714] and
// [0.0714, 1.187146]. Short rails are a single segment [-0.534146, 0.534146].
const (
	CushionSegmentLongInner = 0.0714   // inner end of a long-rail segment
	CushionSegmentLongOuter = 1.187146 // outer end of a long-rail segment
	CushionSegmentShortHalf = 0.534146 // half-width of a short-rail segment
)

// SpeedForPower maps SHOOT.power (0,1] onto the initial cue ball speed.
func SpeedForPower(power float64) float64 {
	if power < 0 {
		power = 0
	}
	if power > 1 {
		power = 1
	}
	return PowerMinSpeed + power*(PowerMaxSpeed-PowerMinSpeed)
}

// IsValidBallID reports whether id is a legal ball number (0..MaxBallID).
func IsValidBallID(id int) bool { return id >= 0 && id <= MaxBallID }

// Distance2D is the planar (XZ) distance between two positions.
func Distance2D(a, b Vector3) float64 {
	dx, dz := a.X-b.X, a.Z-b.Z
	return math.Sqrt(dx*dx + dz*dz)
}

// Speed is the magnitude of a velocity vector.
func Speed(v Vector3) float64 {
	return math.Sqrt(v.X*v.X + v.Y*v.Y + v.Z*v.Z)
}

// InsideTable reports whether a ball centre lies within the reachable bed.
func InsideTable(p Vector3) bool {
	return p.X >= -LimitX && p.X <= LimitX && p.Z >= -LimitZ && p.Z <= LimitZ
}

// InsideKitchen reports whether a ball centre lies in the kitchen: behind the
// head string and inside the bed.
func InsideKitchen(p Vector3) bool {
	return p.X >= KitchenMinX && p.X <= KitchenMaxX && p.Z >= -LimitZ && p.Z <= LimitZ
}

// DefaultTableInfo returns the geometry contract handed to clients in
// WELCOME.table and GameStateDTO.table.
func DefaultTableInfo() TableInfo {
	return TableInfo{
		HalfX:         BoundaryHalfX,
		HalfZ:         BoundaryHalfZ,
		LimitX:        LimitX,
		LimitZ:        LimitZ,
		BallRadius:    BallRadius,
		HeadStringX:   HeadStringX,
		KitchenMinX:   KitchenMinX,
		KitchenMaxX:   KitchenMaxX,
		PowerMinSpeed: PowerMinSpeed,
		PowerMaxSpeed: PowerMaxSpeed,
		PocketGeometry: &PocketGeometryConfig{
			CornerMouthWidth:        CornerMouthWidth,
			SideMouthWidth:          SideMouthWidth,
			CornerCutX:             CornerCutX,
			CornerCutY:             CornerCutY,
			SideCutHalfX:           SideCutHalfX,
			JawRadius:              JawRadius,
			ThroatDepth:            ThroatDepth,
			CushionSegmentLongInner: CushionSegmentLongInner,
			CushionSegmentLongOuter: CushionSegmentLongOuter,
			CushionSegmentShortHalf: CushionSegmentShortHalf,
		},
	}
}
