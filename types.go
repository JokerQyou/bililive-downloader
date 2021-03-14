package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/c2h5oh/datasize"
	"os"
	"strconv"
	"strings"
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

// IntSelection is a simple slice of integers that supports `Contains` test.
type IntSelection []int

// Contains tests whether given index was selected.
func (s *IntSelection) Contains(i int) bool {
	for _, j := range *s {
		if j == i {
			return true
		}
	}
	return false
}

// String satisfies `Stringer` interface from std libs, used when formatting as a string.
func (s IntSelection) String() string {
	builder := strings.Builder{}
	builder.WriteString("[ ")
	for i, v := range s {
		builder.WriteString(strconv.FormatInt(int64(v), 10))
		if i < len(s)-1 {
			builder.WriteString(", ")
		}
	}
	builder.WriteString(" ]")
	return builder.String()
}

// SelectFromList asks the user to select a list of indexes from the source slice, and return the selected as a `IntSelection`.
// If the user types `0` all the elements are considered selected.
// The source slice is [1, last].
// `msg` is printed before user interaction.
// TODO How should we test this?
func SelectFromList(last int, msg string) (IntSelection, error) {
	var selected IntSelection
	dedup := make(map[int]bool)

	// Ask user what to do
	var selectAll bool
	fmt.Print(msg)
	input, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		return nil, err
	}

	inputIndexes := strings.Split(strings.TrimSpace(string(input)), ",")

	for _, i := range inputIndexes {
		n, err := strconv.ParseInt(i, 10, 32)
		if err != nil {
			return nil, errors.New("无效的选择")
		}

		if n == 0 {
			selectAll = true
			break
		}

		// De-duplicate
		if _, ok := dedup[int(n)]; !ok {
			selected = append(selected, int(n))
			dedup[int(n)] = true
		}
	}

	if selectAll {
		selected = make(IntSelection, 0)
		for i := 1; i <= last; i++ {
			selected = append(selected, i)
		}
	}

	return selected, nil
}
