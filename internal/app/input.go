package app

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// The original API accepts operation IDs as either JSON numbers or strings.
// Keep their decimal representation intact rather than passing through float64.
type identifier string

func (id *identifier) UnmarshalJSON(b []byte) error {
	value := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &value); err != nil {
			return err
		}
	}
	if !safeID(value) {
		return errors.New("operation ID must contain decimal digits")
	}
	*id = identifier(value)
	return nil
}

type amountValue float64

func (amount *amountValue) UnmarshalJSON(b []byte) error {
	value := string(b)
	if len(b) > 0 && b[0] == '"' {
		if err := json.Unmarshal(b, &value); err != nil {
			return err
		}
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || !validAmount(n) {
		return errors.New("positive amount required")
	}
	*amount = amountValue(n)
	return nil
}
