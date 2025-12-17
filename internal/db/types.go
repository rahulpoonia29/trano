package db

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

type ErrorCounter struct {
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"`
}

type RunErrors struct {
	StaticResponse *ErrorCounter `json:"static_response,omitempty"`
	APIError       *ErrorCounter `json:"api_error,omitempty"`
	UnknownError   *ErrorCounter `json:"unknown,omitempty"`
}

func (r *RunErrors) Scan(value any) error {
	if value == nil {
		*r = RunErrors{}
		r.StaticResponse = &ErrorCounter{}
		return nil
	}

	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("cannot scan %T into RunErrors", value)
	}

	if len(b) == 0 {
		*r = RunErrors{}
		r.StaticResponse = &ErrorCounter{}
		return nil
	}

	err := json.Unmarshal(b, r)
	if err != nil {
		return err
	}
	if r.StaticResponse == nil {
		r.StaticResponse = &ErrorCounter{}
	}
	return nil
}

func (r RunErrors) Value() (driver.Value, error) {
	if (r == RunErrors{}) {
		return "{}", nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
