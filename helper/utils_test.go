package helper

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestContainsInt(t *testing.T) {
	type testRow struct {
		set              []int
		test             int
		expectedContains bool
	}

	testData := []testRow{
		{[]int{1, 2, 3}, 1, true},
		{[]int{}, 0, false},
		{nil, -1, false},
		{[]int{-1, 0, 100, 1, 1, 1}, 1, true},
	}

	for _, row := range testData {
		assert.Equal(t, row.expectedContains, ContainsInt(row.set, row.test))
	}
}
