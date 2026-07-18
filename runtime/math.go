//go:build !wasm

package runtime

import (
	"math"
)

// toFloat64 converts a generic interface{} number to float64.
func toFloat64(val interface{}) float64 {
	switch v := val.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	default:
		return 0.0
	}
}

func MathFloor(x interface{}) interface{} {
	return math.Floor(toFloat64(x))
}

func MathCeil(x interface{}) interface{} {
	return math.Ceil(toFloat64(x))
}

func MathRound(x interface{}) interface{} {
	return math.Round(toFloat64(x))
}

func MathAbs(x interface{}) interface{} {
	return math.Abs(toFloat64(x))
}

func MathPow(base, exp interface{}) interface{} {
	return math.Pow(toFloat64(base), toFloat64(exp))
}

func MathSqrt(x interface{}) interface{} {
	return math.Sqrt(toFloat64(x))
}

func MathMin(a, b interface{}) interface{} {
	fa := toFloat64(a)
	fb := toFloat64(b)
	if fa < fb {
		return fa
	}
	return fb
}

func MathMax(a, b interface{}) interface{} {
	fa := toFloat64(a)
	fb := toFloat64(b)
	if fa > fb {
		return fa
	}
	return fb
}
