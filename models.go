package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/c2h5oh/datasize"
	"github.com/gofrs/uuid"
)

// RecordPart represents a part of the whole livestream recording.
// It's typically H.264 media stream encapsulated in a single FLV container.
type RecordPart struct {
	Url         string   `json:"url"`
	Size        Size     `json:"size"`
	Length      Duration `json:"length"`
	BackupUrl   *string  `json:"backup_url,omitempty"`
	PreviewInfo *string  `json:"preview_info,omitempty"`
	filename    string   `json:"-"`
}

// FileName returns the unique filename of this part.
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

// RecordInfo represents minimal media info about a complete recording.
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

// ApiResponse wraps general HTTP API response from bilibili.
type ApiResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Ttl     int        `json:"ttl"`
	Data    RecordInfo `json:"data"`
}

type LiveRecordInfo struct {
	Title          string `json:"title"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
}

type VideoData struct {
	LiveRecord LiveRecordInfo `json:"live_record_info"`
}

type VideoApiResponse struct {
	Code    int       `json:"code"`
	Message string    `json:"message"`
	Ttl     int       `json:"ttl"`
	Data    VideoData `json:"data"`
}

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
