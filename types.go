package main

import (
	"encoding/json"
	"errors"
	"github.com/c2h5oh/datasize"
	"time"
)

// Duration is an alias for `time.Duration` that supports direct JSON-unmarshalling.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		// value is in `ms` (typical Javascript), but Duration type conversion wants `ns`.
		d.Duration = time.Duration(value * 1_000_000)
		return nil
	default:
		return errors.New("invalid duration")
	}
}

// Size is a file size type which always evaluates to human-readable form.
// It also supports direct JSON-unmarshalling.
type Size struct {
	datasize.ByteSize
}

func (s *Size) UnmarshalJSON(b []byte) error {
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch value := v.(type) {
	case float64:
		s.ByteSize = datasize.ByteSize(value)
		return nil
	default:
		return errors.New("invalid duration")
	}
}

func (s Size) String() string {
	return s.HumanReadable()
}
