package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// flexNum decodes a JSON value that may be a number, a numeric string, or a
// placeholder such as "-". Valid reports whether a number was found.
type flexNum struct {
	Value float64
	Valid bool
}

func (f *flexNum) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		*f = flexNum{}
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("decode number: %w", err)
		}
		*f = parseFlex(s)
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		*f = flexNum{}
		return nil //nolint:nilerr // A non-numeric value is reported as invalid, not as a decode failure.
	}
	*f = flexNum{Value: v, Valid: true}
	return nil
}

func parseFlex(s string) flexNum {
	s = strings.TrimSuffix(strings.TrimSpace(s), "x")
	if s == "" || s == "-" {
		return flexNum{}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return flexNum{}
	}
	return flexNum{Value: v, Valid: true}
}
