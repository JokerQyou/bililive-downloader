package main

import (
	"encoding/json"
	"fmt"
	"github.com/gofrs/uuid"
	"net/url"
	"strings"
)

// RecordPart represents a part of the whole livestream recording.
// It's typically H.264 media stream encapsulated in a single FLV container.
type RecordPart struct {
	Url         string   `json:"url"`
	Size        Size     `json:"size"`
	Length      Duration `json:"length"`
	BackupUrl   *string  `json:"backup_url,omitempty"`
	PreviewInfo *string  `json:"preview_info,omitempty"`
	filename    string
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

// RecordParts represents minimal media info about parts of a complete recording.
type RecordParts struct {
	List                 []RecordPart `json:"list"`
	Size                 Size         `json:"size"`
	Length               Duration     `json:"length"`
	CurrentQualityNumber uint64       `json:"current_qn"`
	Qualities            []Quality    `json:"qn_desc"`
}

func (ri *RecordParts) Quality() string {
	for _, q := range ri.Qualities {
		if q.Number == ri.CurrentQualityNumber {
			return q.Name
		}
	}
	return "未知"
}

// ApiResponse wraps general HTTP API response from bilibili.
type ApiResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Ttl     int             `json:"ttl"`
	Data    json.RawMessage `json:"data"` // No generics, no union types, :(
}

type LiveRecordInfo struct {
	Title          string `json:"title"`
	StartTimestamp int64  `json:"start_timestamp"`
	EndTimestamp   int64  `json:"end_timestamp"`
}

type LiveRecord struct {
	Info LiveRecordInfo `json:"live_record_info"`
}
