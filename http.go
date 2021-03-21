package main

import (
	"bililive-downloader/models"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"
)

// getApi performs GET request and returns `.data` field of the API response.
func getApi(url string) (*json.RawMessage, error) {
	timeout, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	var buf bytes.Buffer

	riReq, err := http.NewRequestWithContext(timeout, http.MethodGet, url, &buf)
	if err != nil {
		return nil, err
	}

	riReq.Header = http.Header{
		UaKey: []string{UserAgent},
	}
	resp, err := http.DefaultClient.Do(riReq)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var apiResp models.ApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	if apiResp.Code != 0 {
		return nil, fmt.Errorf("响应码=%d，响应消息=%v\n", apiResp.Code, apiResp.Message)
	}

	return &apiResp.Data, nil
}

// fetchRecordInfo fetches info about given livestream recording (title & start / end time) from bilibili API.
func fetchRecordInfo(recordId string) (*models.LiveRecordInfo, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/record/getInfoByLiveRecord?rid=%s", recordId))
	if err != nil {
		return nil, err
	}

	var info models.LiveRecord
	err = json.Unmarshal(*data, &info)
	return &info.Info, err
}

// fetchRecordParts fetches record parts list from bilibili API.
func fetchRecordParts(recordId string) (*models.RecordParts, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/record/getLiveRecordUrl?rid=%s&platform=html5", recordId))
	if err != nil {
		return nil, err
	}

	var info models.RecordParts
	err = json.Unmarshal(*data, &info)
	return &info, err
}

// fetchLiverInfo fetches info of the liver (owner of given room).
func fetchLiverInfo(roomId int64) (*models.LiverInfo, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/live_user/v1/UserInfo/get_anchor_in_room?roomid=%d", roomId))
	if err != nil {
		return nil, err
	}

	var wrapper models.RoomAnchorInfo
	err = json.Unmarshal(*data, &wrapper)
	return &wrapper.Info, err
}
