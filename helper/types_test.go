package helper

import (
	"encoding/json"
	"github.com/c2h5oh/datasize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestDuration_String(t *testing.T) {
	testData := map[string]time.Duration{
		"100ms":  time.Millisecond * 123,
		"1.2s":   time.Millisecond * 1230,
		"12.3s":  time.Millisecond * 12300,
		"2m3s":   time.Millisecond * 123000,
		"2h3m4s": time.Millisecond * 7384001,
	}

	for expected, sourceDuration := range testData {
		assert.Equal(t, expected, Duration{sourceDuration}.String())
	}
}

func TestDuration_UnmarshalJSON(t *testing.T) {
	type testWrapper struct {
		Length Duration `json:"duration"`
	}

	testData := map[string]time.Duration{
		`{"duration": 1000}`:   time.Second * 1,
		`{"duration": 144000}`: time.Second * 144, // 2.4m
		`{"duration": 0}`:      time.Millisecond * 0,
		`{"duration": "123}`:   -1, // placeholder for error
		`{"duration": -123}`:   -1,
		`{"duration": "1m"}`:   -1,
	}

	for sourceJSON, expected := range testData {
		var tDestination testWrapper
		err := json.Unmarshal([]byte(sourceJSON), &tDestination)
		if expected < 0 {
			assert.Error(t, err)
			continue
		}

		assert.NoError(t, err)
		assert.Equal(t, expected, tDestination.Length.Duration)
	}
}

func TestSize_String(t *testing.T) {
	testData := map[int64]string{
		1:           "1 B",
		1023:        "1023 B",
		12345:       "12.1 KB",
		13347456:    "12.7 MB",
		1262453674:  "1.2 GB",
		12743862523: "11.9 GB",
	}

	for byteSize, expected := range testData {
		assert.Equal(t, expected, Size{datasize.ByteSize(byteSize)}.String())
	}
}

func TestSize_UnmarshalJSON(t *testing.T) {
	type testWrapper struct {
		Size Size `json:"size"`
	}

	testData := map[string]string{
		`{"size": 1}`:           "1 B",
		`{"size": 1023}`:        "1023 B",
		`{"size": 12345}`:       "12.1 KB",
		`{"size": 13347456}`:    "12.7 MB",
		`{"size": 1262453674}`:  "1.2 GB",
		`{"size": 12743862523}`: "11.9 GB",
		`{"size": 0}`:           "0 B",
		`{"size": -1}`:          "error", // placeholder for error
		`{"size": "invalid"}`:   "error",
		`{"size": 12.3}`:        "error",
	}

	for sourceJSON, expected := range testData {
		var tDestination testWrapper
		err := json.Unmarshal([]byte(sourceJSON), &tDestination)
		if expected == "error" {
			assert.Error(t, err)
			continue
		}

		assert.NoError(t, err)
		assert.Equal(t, expected, tDestination.Size.String())
	}
}

func TestJSONTime_String(t *testing.T) {
	timezone, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)

	testData := map[string]time.Time{
		"2006-01-02 15:04:05": time.Date(2006, 01, 02, 15, 04, 05, 0, timezone),
		"2021-03-01 02:33:45": time.Date(2021, 03, 01, 02, 33, 45, 0, timezone),
	}

	for expected, sourceTime := range testData {
		actualTimeStr := JSONTime{sourceTime}.String()
		assert.Equal(t, expected, actualTimeStr)
	}
}

func TestJSONTime_UnmarshalJSON(t *testing.T) {
	type testWrapper struct {
		Time JSONTime `json:"time"`
	}

	// TODO Improve
	testTime1 := time.Date(2021, 03, 27, 14, 26, 59, 0, time.UTC)
	testData := map[string]*time.Time{
		`{"time": 1616855219}`:            &testTime1,
		`{"time": "1 minute ago"}`:        nil, // placeholder for error
		`{"time": 1.25}`:                  nil,
		`{"time": "2006-01-02 15:04:05"}`: nil,
		`{"time": -1.235}`:                nil,
	}

	for sourceJSON, expectedTime := range testData {
		var tDestination testWrapper
		err := json.Unmarshal([]byte(sourceJSON), &tDestination)
		if expectedTime == nil {
			assert.Error(t, err)
			continue
		}

		assert.NoError(t, err)
		assert.True(t, expectedTime.Equal(tDestination.Time.Time))
	}
}
