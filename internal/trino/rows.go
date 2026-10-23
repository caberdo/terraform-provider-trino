package trino

import (
	"encoding/json"
	"strconv"
	"strings"
)

// Row is one result row keyed by lower-case column name. Values are the Go
// values the Trino driver produced: string, int64, bool, float64, time.Time,
// nil, and so on.
type Row map[string]any

// String returns a string column. It accepts string and []byte values, which
// are the two shapes the driver produces for VARCHAR.
func (r Row) String(col string) (string, bool) {
	value, ok := r[strings.ToLower(col)]
	if !ok {
		return "", false
	}
	return asString(value)
}

// Bool returns a boolean column. Trino's information_schema renders booleans
// as YES/NO, while query results use JSON booleans, so both spellings are
// accepted in addition to "true"/"false".
func (r Row) Bool(col string) (bool, bool) {
	value, ok := r[strings.ToLower(col)]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch {
		case strings.EqualFold(typed, "true"), strings.EqualFold(typed, "yes"):
			return true, true
		case strings.EqualFold(typed, "false"), strings.EqualFold(typed, "no"):
			return false, true
		}
	}
	return false, false
}

// Int64 returns an integer column. It accepts the integer widths the driver and
// encoding/json produce, plus a numeric string.
func (r Row) Int64(col string) (int64, bool) {
	value, ok := r[strings.ToLower(col)]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		return parsed, err == nil
	}
	return 0, false
}

// SoleString returns the only value of a single-column row, which is how
// SHOW statements return one name per row. It reports false when the row does
// not have exactly one column or the value is not a string.
func (r Row) SoleString() (string, bool) {
	if len(r) != 1 {
		return "", false
	}
	for _, value := range r {
		return asString(value)
	}
	return "", false
}

func asString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case []byte:
		return string(typed), true
	}
	return "", false
}
