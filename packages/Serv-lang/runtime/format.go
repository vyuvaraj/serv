//go:build !wasm

package runtime

import (
	"fmt"
	"math"
)

// FormatBytes formats a byte size into human-readable string (e.g. 1048576 -> "1 MB").
func FormatBytes(val interface{}) interface{} {
	b := toFloat64(val)
	if b <= 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	digitGroups := int(math.Log10(b) / math.Log10(1024))
	if digitGroups >= len(units) {
		digitGroups = len(units) - 1
	}
	value := b / math.Pow(1024, float64(digitGroups))
	if value == math.Trunc(value) {
		return fmt.Sprintf("%.0f %s", value, units[digitGroups])
	}
	return fmt.Sprintf("%.2f %s", value, units[digitGroups])
}

// FormatNumber formats a large number into human-readable string (e.g. 1500000 -> "1.5M").
func FormatNumber(val interface{}) interface{} {
	n := toFloat64(val)
	absN := math.Abs(n)
	if absN < 1000 {
		if n == math.Trunc(n) {
			return fmt.Sprintf("%.0f", n)
		}
		return fmt.Sprintf("%.2f", n)
	}
	units := []string{"", "K", "M", "B", "T"}
	digitGroups := int(math.Log10(absN) / 3)
	if digitGroups >= len(units) {
		digitGroups = len(units) - 1
	}
	value := n / math.Pow(1000, float64(digitGroups))
	if value == math.Trunc(value) {
		return fmt.Sprintf("%.0f%s", value, units[digitGroups])
	}
	return fmt.Sprintf("%.1f%s", value, units[digitGroups])
}

// FormatPercent formats float percentage to string (e.g. 0.856 -> "85.6%").
func FormatPercent(val interface{}) interface{} {
	p := toFloat64(val) * 100.0
	if p == math.Trunc(p) {
		return fmt.Sprintf("%.0f%%", p)
	}
	return fmt.Sprintf("%.1f%%", p)
}

// FormatPlural formats singular/plural based on count.
func FormatPlural(count, singular, plural interface{}) interface{} {
	c := toFloat64(count)
	s := toString(singular)
	p := toString(plural)
	if c == 1 {
		return fmt.Sprintf("%.0f %s", c, s)
	}
	return fmt.Sprintf("%.0f %s", c, p)
}
