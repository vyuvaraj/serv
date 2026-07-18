//go:build !wasm

package runtime

import (
	"fmt"
	"time"
)

// toTime converts a generic interface{} to time.Time, parsing common layouts.
func toTime(val interface{}) (time.Time, error) {
	if t, ok := val.(time.Time); ok {
		return t, nil
	}
	if s, ok := val.(string); ok {
		// Try RFC3339
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, nil
		}
		// Try date
		if t, err := time.Parse("2006-01-02", s); err == nil {
			return t, nil
		}
		// Try datetime
		if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
			return t, nil
		}
		// Try HTTP layout
		if t, err := time.Parse(time.RFC1123, s); err == nil {
			return t, nil
		}
		if t, err := time.Parse(time.RFC1123Z, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to convert %v to time", val)
}

// TimeParse parses a string into a time.Time value.
func TimeParse(str, layout interface{}) interface{} {
	sStr := toString(str)
	lStr := toString(layout)
	t, err := time.Parse(lStr, sStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t
}

// TimeFormat formats a time.Time value as a string.
func TimeFormat(tVal, layout interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	lStr := toString(layout)
	return t.Format(lStr)
}

// TimeInZone loads the timezone location and returns t in that timezone.
func TimeInZone(tVal, tz interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	locStr := toString(tz)
	loc, err := time.LoadLocation(locStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t.In(loc)
}

// TimeUTC converts time.Time to UTC.
func TimeUTC(tVal interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t.UTC()
}

// TimeLocal converts time.Time to local location.
func TimeLocal(tVal interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t.Local()
}

// TimeAdd parses duration and adds it to t.
func TimeAdd(tVal, durVal interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	dStr := toString(durVal)
	d, err := time.ParseDuration(dStr)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t.Add(d)
}

// TimeSub subtracts t2 from t1, returning duration in seconds (float64).
func TimeSub(t1Val, t2Val interface{}) interface{} {
	t1, err := toTime(t1Val)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	t2, err := toTime(t2Val)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return t1.Sub(t2).Seconds()
}

// TimeBefore returns true if t1 is before t2.
func TimeBefore(t1Val, t2Val interface{}) interface{} {
	t1, err := toTime(t1Val)
	if err != nil {
		return false
	}
	t2, err := toTime(t2Val)
	if err != nil {
		return false
	}
	return t1.Before(t2)
}

// TimeAfter returns true if t1 is after t2.
func TimeAfter(t1Val, t2Val interface{}) interface{} {
	t1, err := toTime(t1Val)
	if err != nil {
		return false
	}
	t2, err := toTime(t2Val)
	if err != nil {
		return false
	}
	return t1.After(t2)
}

// TimeFromUnix converts unix timestamp (seconds) to formatted string or time.Time.
func TimeFromUnix(seconds interface{}) interface{} {
	sec := toInt64(seconds)
	return time.Unix(sec, 0)
}

// TimeComponents returns a map representing components of the time.Time value.
func TimeComponents(tVal interface{}) interface{} {
	t, err := toTime(tVal)
	if err != nil {
		return [2]interface{}{nil, err.Error()}
	}
	return map[string]interface{}{
		"year":    float64(t.Year()),
		"month":   float64(t.Month()),
		"day":     float64(t.Day()),
		"hour":    float64(t.Hour()),
		"minute":  float64(t.Minute()),
		"second":  float64(t.Second()),
		"weekday": t.Weekday().String(),
		"tz":      t.Location().String(),
	}
}
