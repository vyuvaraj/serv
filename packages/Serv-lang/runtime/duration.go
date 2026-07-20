//go:build !wasm

package runtime

import (
	"time"
)

// DurationParse parses a human-readable duration (like "2h30m") and returns seconds as float64.
func DurationParse(durStr interface{}) interface{} {
	d, err := time.ParseDuration(toString(durStr))
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return d.Seconds()
}

// DurationFormat formats a float64 duration in seconds into human-readable string (like "2h30m15s").
func DurationFormat(seconds interface{}) interface{} {
	sec := toFloat64(seconds)
	d := time.Duration(sec * float64(time.Second))
	return d.String()
}

// DurationSince calculates seconds since a past timestamp string or time.Time.
func DurationSince(ts interface{}) interface{} {
	t, err := toTime(ts)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return time.Now().UTC().Sub(t).Seconds()
}
