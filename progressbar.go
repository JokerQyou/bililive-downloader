package main

import (
	"fmt"
	"github.com/c2h5oh/datasize"
	"github.com/gosuri/uiprogress"
	"strconv"
	"time"
)

const TotalPlaceholder = 999

type ProgressUnitType int

const (
	UnitTypeNumber   ProgressUnitType = iota // Render as original number
	UnitTypeByteSize                         // Render as byte size
	UnitTypeDuration                         // Render as time duration (nanoseconds)
)

// ProgressBar simplifies long-time operation on file(s).
// It wraps a uiprogress.Bar instance and allow dynamically changing prefix decoration and value unit as you wish.
// A user can call `.SetPrependDecorator(*uiprogress.Bar) string` to render some text before the internal decorators.
// The final bar will be like this: `{customPrependDecoration}\t{percentage} [bar] {current} / {total}`.
// {percentage}, {current} and {total} are rendered by internal decorators, they don't support customization.
type ProgressBar struct {
	*uiprogress.Bar
	totalSet  bool
	unitType  ProgressUnitType
	prepender uiprogress.DecoratorFunc
}

// SetTotal sets the max value to given `total`, allowing dynamically changing the progress bar percentage.
func (b *ProgressBar) SetTotal(total int64) {
	b.Total = int(total)
	if !b.totalSet {
		b.totalSet = true
	}
}

// SetCurrent sets the current value to given `current`. Do not use `.Set(int)` method.
func (b *ProgressBar) SetCurrent(current int64) {
	// Must set actual `total` before setting `current`.
	if !b.totalSet {
		return
	}

	_ = b.Set(int(current))
}

// SetUnitType sets the unit used to decorate progress bar value.
func (b *ProgressBar) SetUnitType(typ ProgressUnitType) error {
	switch typ {
	case UnitTypeNumber, UnitTypeByteSize, UnitTypeDuration:
		b.unitType = typ
		return nil
	default:
		return fmt.Errorf("invalid unit type %v", typ)
	}
}

// SetPrefixDecorator sets a custom decorator that prints some text before the progress bar.
func (b *ProgressBar) SetPrefixDecorator(fn uiprogress.DecoratorFunc) {
	b.prepender = fn
}

// prependDecorator is an internal decorator.
func (b *ProgressBar) prependDecorator(bar *uiprogress.Bar) string {
	var customPrefix string
	if b.prepender != nil {
		customPrefix = b.prepender(bar)
	}

	return fmt.Sprintf("%s\t%.2f%%\t", customPrefix, bar.CompletedPercent())
}

// appendDecorator is an internal decorator.
func (b *ProgressBar) appendDecorator(bar *uiprogress.Bar) string {
	values := make([]string, 2)

	switch b.unitType {
	case UnitTypeNumber:
		// TODO Improve
		values[0] = strconv.FormatInt(int64(b.Current()), 10)
		values[1] = strconv.FormatInt(int64(b.Total), 10)
		break
	case UnitTypeByteSize:
		values[0] = datasize.ByteSize(int64(b.Current())).HumanReadable()
		values[1] = datasize.ByteSize(int64(b.Total)).HumanReadable()
		break
	case UnitTypeDuration:
		// 1024h60m59.09s
		values[0] = Duration{time.Duration(int64(b.Current()))}.String()
		values[1] = Duration{time.Duration(int64(b.Total))}.String()
		break
	default:
		return ""
	}

	// TODO Should we fill to fixed width?
	return fmt.Sprintf("\t%s / %s", values[0], values[1])
}

// AddProgressBar adds a new progress bar.
// Pass `total=-1` to decide total amount later. `.SetCurrent` will not take effect until `.SetTotal` is called at least once.
func AddProgressBar(total int64) *ProgressBar {
	barTotal := total
	if barTotal == -1 {
		barTotal = TotalPlaceholder
	}

	bar := uiprogress.AddBar(int(barTotal))
	pbar := &ProgressBar{
		bar,
		total != -1,
		UnitTypeNumber,
		nil,
	}

	bar.PrependFunc(pbar.prependDecorator)
	bar.AppendFunc(pbar.appendDecorator)
	return pbar
}
