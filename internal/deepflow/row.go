package deepflow

import (
	"fmt"
	"strconv"
)

// String returns the value for key as a string. Missing keys and nulls
// yield "".
func (r Row) String(key string) string {
	v, ok := r[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// Int64 coerces the value at key to int64. JSON numbers arrive as float64
// from encoding/json; large ClickHouse counters fit in float64 below 2^53
// which is well above any realistic l7_flow_log volume per cycle.
func (r Row) Int64(key string) int64 {
	v, ok := r[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case string:
		i, _ := strconv.ParseInt(t, 10, 64)
		return i
	}
	return 0
}

// Int returns Int64 truncated to int.
func (r Row) Int(key string) int {
	return int(r.Int64(key))
}

// Float64 coerces the value at key to float64.
func (r Row) Float64(key string) float64 {
	v, ok := r[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case int64:
		return float64(t)
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}
