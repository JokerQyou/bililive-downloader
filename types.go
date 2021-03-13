package main

import (
	"encoding/json"
	"github.com/c2h5oh/datasize"
	"time"
)

// Duration is an alias for `time.Duration` that supports direct JSON-unmarshalling.
type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	// value is in `ms` (typical Javascript), but Duration type conversion wants `ns`.
	d.Duration = time.Duration(v * 1_000_000)
	return nil
}

// Size is a file size type which always evaluates to human-readable form.
// It also supports direct JSON-unmarshalling.
type Size struct {
	datasize.ByteSize
}

func (s *Size) UnmarshalJSON(b []byte) error {
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	s.ByteSize = datasize.ByteSize(v)
	return nil
}

func (s Size) String() string {
	return s.HumanReadable()
}

// JSONTime is an alias for `time.Time` that supports unmarshalling from integer timestamp.
type JSONTime struct {
	time.Time
}

func (t *JSONTime) UnmarshalJSON(b []byte) error {
	var v int64
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	t.Time = time.Unix(v, 0)
	return nil
}

// Notice that the `Stringer` interface should be implemented on the value type itself rather than on its pointer,
// since to print a string representation of a type instance means to consume a new copy of the given value.
func (t JSONTime) String() string {
	return t.Local().Format("2006-01-02 15:04:05")
}
