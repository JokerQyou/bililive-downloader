package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/c2h5oh/datasize"
	"github.com/gofrs/uuid"
	"net/url"
	"strings"
	"time"
)

type RecordPart struct {
	Url         string   `json:"url"`
	Size        Size     `json:"size"`
	Length      Duration `json:"length"`
	BackupUrl   *string  `json:"backup_url,omitempty"`
	PreviewInfo *string  `json:"preview_info,omitempty"`
	filename    string   `json:"-"`
}

func (rp *RecordPart) FileName() string {
	if rp.filename == "" {
		u, err := url.Parse(rp.Url)
		if err == nil {
			pp := strings.Split(u.Path, "/")
			rp.filename = strings.ReplaceAll(pp[len(pp)-1], ":", "-")
		} else {
			id, err := uuid.NewV4()
			if err != nil {
				panic(err)
			}
			rp.filename = fmt.Sprintf("%s.flv", id.String())
		}
	}
	return rp.filename
}

type Quality struct {
	Number uint64 `json:"qn"`
	Name   string `json:"desc"`
}

type RecordInfo struct {
	List                 []RecordPart `json:"list"`
	Size                 Size         `json:"size"`
	Length               Duration     `json:"length"`
	CurrentQualityNumber uint64       `json:"current_qn"`
	Qualities            []Quality    `json:"qn_desc"`
}

func (ri *RecordInfo) Quality() string {
	for _, q := range ri.Qualities {
		if q.Number == ri.CurrentQualityNumber {
			return q.Name
		}
	}
	return "未知"
}

type ApiResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Ttl     int        `json:"ttl"`
	Data    RecordInfo `json:"data"`
}

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
