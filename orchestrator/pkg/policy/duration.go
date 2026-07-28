package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Duration is a time.Duration that is represented as a human-readable string
// in JSON and YAML, for example "30s" or "5m".
type Duration time.Duration

func NewDuration(value time.Duration) Duration {
	return Duration(value)
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d.String()), nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	if len(bytes.TrimSpace(text)) == 0 {
		*d = 0
		return nil
	}
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("parse duration %q: %w", text, err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("duration must be a string: %w", err)
	}
	return d.UnmarshalText([]byte(text))
}
