package models

import (
	"bililive-downloader/progressbar"
	"fmt"
	"github.com/gosuri/uiprogress"
)

func (t *PartTask) SetCurrentStep(name string) {
	t.currentStep = name
}
func (t *PartTask) SetFileName(name string) {
	t.filename = name
}

func (t *PartTask) DecorStepName() string {
	return t.currentStep
}

func (t *PartTask) DecorFileName() string {
	if t.filename == "" {
		t.filename = t.Part.FileName()
	}
	return t.filename
}

func (t *PartTask) DecorPartName() string {
	return fmt.Sprintf("第%d部分", t.PartNumber)
}

// PartTask represents a task for downloading a single part of livestream recording.
type PartTask struct {
	PartNumber        int         // partNumber is index+1
	Part              *RecordPart // Part is record part info
	DownloadDirectory string
	currentStep       string
	filename          string
}

func (t *PartTask) AddProgressBar(total int64) *progressbar.ProgressBar {
	bar := progressbar.AddProgressBar(total)
	bar.SetPrefixDecorator(func(b *uiprogress.Bar) string {
		return fmt.Sprintf("%s\t%s\t%s\t", t.DecorPartName(), t.DecorStepName(), t.DecorFileName())
	})
	bar.SetUnitType(progressbar.UnitTypeByteSize)
	return bar
}
