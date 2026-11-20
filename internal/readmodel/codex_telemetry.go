package readmodel

import "math"

// Scope the display values to the structured types the collector emits.
// SQLite's JSON reader yields float64, while a fresh projection carries int64.
func codexSafeMetadata(m row) row {
	fields := row{}
	number := func(source, key string, lower, upper float64) {
		var v float64
		switch n := m[source].(type) {
		case int64:
			v = float64(n)
		case float64:
			v = n
		default:
			return
		}
		if v >= lower && v <= upper && v == math.Trunc(v) {
			fields[key] = v
		}
	}
	number("attempt", "attempt", 0, math.MaxInt32)
	number("http.response.status_code", "httpStatus", 100, 599)
	number("duration_ms", "durationMs", 0, 1<<53-1)
	if success, ok := m["success"].(bool); ok {
		fields["toolSuccess"] = success
	}
	if len(fields) == 0 {
		return nil
	}
	return fields
}
