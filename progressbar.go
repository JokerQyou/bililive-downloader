package main

import (
	"github.com/gosuri/uiprogress"
)

const TotalPlaceholder = 999

type ProgressBar struct {
	*uiprogress.Bar
	totalSet bool
}

func (b *ProgressBar) SetTotal(total int64) {
	b.Total = int(total)
	if !b.totalSet {
		b.totalSet = true
	}
}

func (b *ProgressBar) SetCurrent(current int64) {
	// Must set actual `total` before setting `current`.
	if !b.totalSet {
		return
	}

	_ = b.Set(int(current))
}

// AddProgressBar adds a new progress bar.
// Pass `total=-1` to decide total amount later. `.SetCurrent` will not take effect until `.SetTotal` is called at least once.
func AddProgressBar(total int64) *ProgressBar {
	barTotal := total
	if barTotal == -1 {
		barTotal = TotalPlaceholder
	}

	bar := uiprogress.AddBar(int(barTotal))
	return &ProgressBar{bar, total != -1}
}
