package config

import (
	"encoding/json"
	"time"
)

// Duration wraps time.Duration to provide custom JSON marshaling/unmarshaling
type Duration struct {
	time.Duration
}

// MarshalJSON implements the json.Marshaler interface
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON implements the json.Unmarshaler interface
func (d *Duration) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	
	duration, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	
	d.Duration = duration
	return nil
}