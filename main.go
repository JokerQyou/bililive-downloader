package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cavaliercoder/grab"
	"github.com/vbauerster/mpb/v6"
	"github.com/vbauerster/mpb/v6/decor"
)

const UaKey = "User-Agent"
const UserAgent = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/55.0.2883.87 Safari/537.36"

// downloadRecordParts download all parts of given livestream record into `where`.
func downloadRecordParts(recordInfo *RecordParts, where string) error {
	var wg sync.WaitGroup
	progressBars := mpb.New(mpb.WithWaitGroup(&wg), mpb.WithRefreshRate(time.Millisecond*500))

	for i, part := range recordInfo.List {
		recordPart := part
		bar := progressBars.AddBar(
			int64(recordPart.Size.Bytes()),
			mpb.PrependDecorators(
				decor.Name(fmt.Sprintf("下载第%d部分", i+1), decor.WCSyncSpace),
				decor.Name(recordPart.FileName(), decor.WCSyncSpace),
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.Current(decor.UnitKiB, "已下载 % .2f", decor.WCSyncSpace),
				decor.Total(decor.UnitKiB, "总大小 % .2f", decor.WCSyncSpace),
				decor.OnComplete(decor.EwmaSpeed(decor.UnitKiB, "% .2f", 1, decor.WCSyncSpace), "完成"),
			),
		)

		wg.Add(1)
		go func(p *RecordPart) {
			defer wg.Done()
			start := time.Now()
			client := grab.NewClient()
			client.UserAgent = UserAgent
			dlReq, err := grab.NewRequest(filepath.Join(where, p.FileName()), p.Url)
			if err != nil {
				return
			}

			resp := client.Do(dlReq)
			ticker := time.NewTicker(time.Millisecond * 500)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					bar.SetCurrent(resp.BytesComplete())
					bar.DecoratorEwmaUpdate(time.Since(start))
					start = time.Now()
				case <-resp.Done:
					bar.SetCurrent(resp.BytesComplete())
					bar.DecoratorEwmaUpdate(time.Since(start))
					return
				}
			}

		}(&recordPart)
	}

	progressBars.Wait()
	return nil
}

// concatRecordParts concatenates multiple record parts (in individual FLV files) into a single MP4 file.
// Video parts are expected to be stored in `where`, and concatenated media files will be stored as `output`.
// Requires `ffmpeg` binary to present in PATH.
func concatRecordParts(recordInfo *RecordParts, where, output string) error {
	if info, err := os.Stat(output); err == nil && info.Mode().IsRegular() {
		return fmt.Errorf("文件 %s 已经存在", output)
	}

	// TODO Are we confident enough that all bilibili livestream records will be H.264 streams encapsulated in FLV containers?
	// Locate ffmpeg tool
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return err
	}

	progress := mpb.New(mpb.WithRefreshRate(time.Millisecond * 500))
	deCapBar := progress.AddBar(
		int64(len(recordInfo.List)),
		mpb.PrependDecorators(
			decor.Name("FLV解包", decor.WCSyncSpace),
			decor.Percentage(decor.WCSyncSpace),
		),
		mpb.AppendDecorators(
			decor.CurrentNoUnit("已完成 %d", decor.WCSyncSpace),
			decor.TotalNoUnit("共 %d 个文件", decor.WCSyncSpace),
		),
	)

	// De-capsulate FLV container. (FLV => TS)
	var wg sync.WaitGroup
	concatList := make([]string, len(recordInfo.List))
	for i, part := range recordInfo.List {
		recordPart := part

		partFilePath := filepath.Join(where, recordPart.FileName())
		baseName := strings.Split(filepath.Base(recordPart.FileName()), ".")[0]
		tempFilePath := filepath.Join(os.TempDir(), fmt.Sprintf("%s.ts", baseName))
		concatList[i] = tempFilePath

		wg.Add(1)
		go func() {
			defer wg.Done()
			defer deCapBar.Increment()

			timeout, cancel := context.WithTimeout(context.Background(), time.Minute*15)
			defer cancel()
			deCap := exec.CommandContext(timeout, ffmpegBin, "-i", partFilePath, "-c", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "mpegts", tempFilePath)
			deCap.Run()
		}()
	}
	// Remove TS media files
	defer func() {
		for _, tempFile := range concatList {
			os.Remove(tempFile)
		}
	}()

	// Wait for all de-cap to finish because concat depends on their output files.
	wg.Wait()

	// Concat TS containers (with H.264 media) together into a single MP4 container.
	concatBar := progress.AddBar(1, mpb.PrependDecorators(decor.Name("合并视频分段", decor.WCSyncSpace)))
	defer concatBar.Increment()

	timeout, cancel := context.WithTimeout(context.Background(), time.Minute*10)
	defer cancel()

	concat := exec.CommandContext(timeout, ffmpegBin, "-i", fmt.Sprintf("concat:%s", strings.Join(concatList, "|")), "-c", "copy", "-bsf:a", "aac_adtstoasc", "-movflags", "faststart", output)
	return concat.Run()
}

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

	var apiResp ApiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	if apiResp.Code != 0 {
		return nil, fmt.Errorf("响应码=%d，响应消息=%v\n", apiResp.Code, apiResp.Message)
	}

	return &apiResp.Data, nil
}

// fetchRecordInfo fetches info about given livestream recording (title & start / end time) from bilibili API.
func fetchRecordInfo(recordId string) (*LiveRecordInfo, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/record/getInfoByLiveRecord?rid=%s", recordId))
	if err != nil {
		return nil, err
	}

	var info LiveRecord
	err = json.Unmarshal(*data, &info)
	return &info.Info, err
}

// fetchRecordParts fetches record parts list from bilibili API.
func fetchRecordParts(recordId string) (*RecordParts, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/xlive/web-room/v1/record/getLiveRecordUrl?rid=%s&platform=html5", recordId))
	if err != nil {
		return nil, err
	}

	var info RecordParts
	err = json.Unmarshal(*data, &info)
	return &info, err
}

func main() {
	criticalErr := func(e error, logLine string) {
		if e != nil {
			fmt.Printf("%s 出错: %v\n", logLine, e)
			os.Exit(1)
		}
	}

	fmt.Print("请输入您要下载的B站直播回放链接地址: ")
	line, _, err := bufio.NewReader(os.Stdin).ReadLine()
	criticalErr(err, "读取用户输入")

	recordId := strings.TrimSpace(strings.Split(string(line), "?")[0])
	rIds := strings.Split(recordId, "/")
	recordId = rIds[len(rIds)-1]
	fmt.Printf("直播回放ID是 %s\n", recordId)

	recordInfo, err := fetchRecordParts(recordId)
	criticalErr(err, "加载视频分段信息")

	videoInfo, err := fetchRecordInfo(recordId)
	criticalErr(err, "加载视频信息")
	fmt.Printf(
		"《%s》直播时间%s ~ %s，时长%v，画质：%s，总大小%s（共%d部分）\n",
		videoInfo.Title,
		videoInfo.Start, videoInfo.End, recordInfo.Length,
		recordInfo.Quality(),
		recordInfo.Size, len(recordInfo.List),
	)

	// Mkdir
	cwd, err := os.Getwd()
	criticalErr(err, "检测当前目录")
	recordDownloadDir := filepath.Join(cwd, recordId)
	criticalErr(os.MkdirAll(recordDownloadDir, 0755), "建立下载目录")
	criticalErr(downloadRecordParts(recordInfo, recordDownloadDir), "下载直播回放分段")

	output := filepath.Join(
		recordDownloadDir,
		fmt.Sprintf("%s-%s-%s.mp4", videoInfo.Start, recordId, videoInfo.Title),
	)
	if len(recordInfo.List) > 0 {
		criticalErr(concatRecordParts(recordInfo, recordDownloadDir, output), "合并视频分段")
	}
	fmt.Printf("回放下载完毕: %s\n", output)
}
