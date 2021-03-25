package models

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// IntSelection is a simple slice of integers that supports `Contains` test.

type IntSelection struct {
	selection []int
	selectAll bool
}

// Contains tests whether given index was selected.
func (s *IntSelection) Contains(i int) bool {
	if s.selectAll {
		return true
	}

	for _, j := range s.selection {
		if j == i {
			return true
		}
	}
	return false
}

func (s *IntSelection) Count() int {
	if s.selectAll {
		return math.MaxInt8
	}
	return len(s.selection)
}

func (s *IntSelection) IsFull() bool {
	return s.selectAll
}

func (s *IntSelection) AsIntSlice() []int {
	return s.selection[:]
}

// String satisfies `Stringer` interface from std libs, used when formatting as a string.
func (s IntSelection) String() string {
	builder := strings.Builder{}
	if s.selectAll {
		builder.WriteString("all")
	} else {
		for i, v := range s.selection {
			builder.WriteString(strconv.FormatInt(int64(v), 10))
			if i < len(s.selection)-1 {
				builder.WriteString(",")
			}
		}
	}
	return builder.String()
}

// ParseStringFromString creates a new IntSelection instance out of given string.
func ParseStringFromString(s string) (*IntSelection, error) {
	s = strings.TrimSpace(s)
	if strings.ToLower(s) == "all" {
		return &IntSelection{selectAll: true}, nil
	}

	inputIndexes := strings.Split(strings.TrimSpace(s), ",")

	var selected IntSelection
	dedup := make(map[int]bool)

	for _, i := range inputIndexes {
		n, err := strconv.ParseInt(i, 10, 32)
		if err != nil {
			return nil, errors.New("无效的选择")
		}

		// Selections are human-readable, so it starts from 1, not 0. Ignore 0.
		if n == 0 {
			continue
		}

		// De-duplicate
		if _, ok := dedup[int(n)]; !ok {
			selected.selection = append(selected.selection, int(n))
			dedup[int(n)] = true
		}
	}

	return &selected, nil
}

// SelectFromList asks the user to select a list of indexes from the source slice, and return the selected as a `IntSelection`.
// If the user types `0` all the elements are considered selected.
// The source slice is [1, last].
// `msg` is printed before user interaction.
// TODO How should we test this?
func ReadFrom(msg string) (*IntSelection, error) {
	// Ask user what to do
	fmt.Print(msg)
	input, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		return nil, err
	}

	return ParseStringFromString(string(input))
}
