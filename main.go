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

var ffmpegBin string

// step represents current step in a long-time file operation.
type step struct {
	currentStep string
	filename    string
	part        int
}

func (s *step) SetCurrentStep(name string) {
	s.currentStep = name
}
func (s *step) SetFileName(name string) {
	s.filename = name
}

func (s *step) DecorStepName(_ decor.Statistics) string {
	return s.currentStep
}

func (s *step) DecorFileName(_ decor.Statistics) string {
	return s.filename
}

func (s *step) DecorPartName(_ decor.Statistics) string {
	return fmt.Sprintf("第%d部分", s.part)
}

// downloadRecordParts download all parts of given livestream record into `where`.
// Downloaded files will also be de-capped to MPEGTS media, the intermediate FLV files will be deleted.
func downloadRecordParts(recordInfo *RecordParts, downloadList IntSelection, where string) (filePaths map[int]string, err error) {
	filePaths = make(map[int]string)
	var filePathUpdater sync.Mutex

	var wg sync.WaitGroup
	progressBars := mpb.New(mpb.WithWaitGroup(&wg), mpb.WithRefreshRate(time.Millisecond*500))

	for i, part := range recordInfo.List {
		recordPart := part
		index := i
		if !downloadList.Contains(index + 1) {
			continue
		}

		currentStep := &step{currentStep: "下载", part: index + 1}
		currentStep.SetFileName(recordPart.FileName())
		bar := progressBars.AddBar(
			int64(recordPart.Size.Bytes()),
			mpb.PrependDecorators(
				decor.Any(currentStep.DecorStepName, decor.WCSyncSpace),
				decor.Any(currentStep.DecorPartName, decor.WCSyncSpace),
				decor.Any(currentStep.DecorFileName, decor.WCSyncSpace),
				decor.Percentage(decor.WCSyncSpace),
			),
			mpb.AppendDecorators(
				decor.Current(decor.UnitKiB, "% .2f", decor.WCSyncSpace),
				decor.Name("/", decor.WCSyncSpace),
				decor.Total(decor.UnitKiB, "% .2f", decor.WCSyncSpace),
				decor.OnComplete(decor.EwmaSpeed(decor.UnitKiB, "% .2f", 1, decor.WCSyncSpace), "完成"),
			),
		)

		wg.Add(1)
		go func(p *RecordPart, index int) {
			defer wg.Done()
			start := time.Now()
			rawFilePath := filepath.Join(where, p.FileName())
			decappedTsFilePath := fmt.Sprintf("%s.ts", strings.Split(rawFilePath, ".")[0])

			// Already processed (probably selectively downloaded this part before), use existing MPEGTS media as result, no need to re-download.
			if info, err := os.Stat(decappedTsFilePath); err == nil && info.Mode().IsRegular() {
				filePathUpdater.Lock()
				defer filePathUpdater.Unlock()
				filePaths[index+1] = decappedTsFilePath
				bar.SetCurrent(int64(recordPart.Size.Bytes()))
				bar.DecoratorEwmaUpdate(time.Since(start))
				currentStep.SetCurrentStep("已存在")
				currentStep.SetFileName(filepath.Base(decappedTsFilePath))
				return
			}

			client := grab.NewClient()
			client.UserAgent = UserAgent
			dlReq, err := grab.NewRequest(rawFilePath, p.Url)
			if err != nil {
				return
			}

			resp := client.Do(dlReq)
			ticker := time.NewTicker(time.Millisecond * 500)
			defer ticker.Stop()

		WaitTillDownloaded:
			for {
				select {
				case <-ticker.C:
					bar.SetCurrent(resp.BytesComplete())
					bar.DecoratorEwmaUpdate(time.Since(start))
					start = time.Now()
				case <-resp.Done:
					bar.SetCurrent(resp.BytesComplete())
					bar.DecoratorEwmaUpdate(time.Since(start))
					start = time.Now()
					break WaitTillDownloaded
				}
			}

			// De-cap from FLV to MPEG TS media
			// TODO Are we confident enough that all bilibili livestream records will be H.264 streams encapsulated in FLV containers?
			currentStep.SetCurrentStep("解包")
			bar.SetRefill(0)

			timeout, cancel := context.WithTimeout(context.Background(), time.Minute*15)
			defer cancel()
			deCap := exec.CommandContext(timeout, ffmpegBin, "-i", rawFilePath, "-c", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "mpegts", decappedTsFilePath)
			if err := deCap.Run(); err == nil {
				filePathUpdater.Lock()
				defer filePathUpdater.Unlock()
				filePaths[index+1] = decappedTsFilePath
				currentStep.SetFileName(filepath.Base(decappedTsFilePath))
				currentStep.SetCurrentStep("完成")
				bar.SetCurrent(int64(recordPart.Size.Bytes()))
				bar.DecoratorEwmaUpdate(time.Since(start))
				// Remove FLV file
				os.Remove(rawFilePath)
			}

		}(&recordPart, index)
	}

	progressBars.Wait()
	return
}

// concatRecordParts concatenates multiple record parts into a single MP4 file.
func concatRecordParts(inputFiles map[int]string, output string) error {
	if info, err := os.Stat(output); err == nil && info.Mode().IsRegular() {
		return fmt.Errorf("文件 %s 已经存在", output)
	}

	var err error
	var wg sync.WaitGroup
	progressBars := mpb.New(mpb.WithWaitGroup(&wg), mpb.WithRefreshRate(time.Millisecond*500))
	bar := progressBars.AddBar(1, mpb.PrependDecorators(decor.Name("合并为单个视频", decor.WCSyncSpace)))

	wg.Add(1)
	go func(e *error) {
		defer wg.Done()
		defer bar.SetCurrent(1)

		// Concat TS containers (with H.264 media) together into a single MP4 container.
		concatList := make([]string, len(inputFiles))
		for i, filePath := range inputFiles {
			concatList[i-1] = filePath
		}

		timeout, cancel := context.WithTimeout(context.Background(), time.Minute*10)
		defer cancel()

		concat := exec.CommandContext(
			timeout,
			ffmpegBin,
			"-i", fmt.Sprintf("concat:%s", strings.Join(concatList, "|")),
			"-c", "copy",
			"-bsf:a", "aac_adtstoasc",
			"-movflags", "faststart",
			output,
		)
		*e = concat.Run()
	}(&err)

	progressBars.Wait()
	return err
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

// fetchLiverInfo fetches info of the liver (owner of given room).
func fetchLiverInfo(roomId int64) (*LiverInfo, error) {
	data, err := getApi(fmt.Sprintf("https://api.live.bilibili.com/live_user/v1/UserInfo/get_anchor_in_room?roomid=%d", roomId))
	if err != nil {
		return nil, err
	}

	var wrapper RoomAnchorInfo
	err = json.Unmarshal(*data, &wrapper)
	return &wrapper.Info, err
}

func main() {
	criticalErr := func(e error, logLine string) {
		if e != nil {
			fmt.Printf("%s 出错: %v\n", logLine, e)
			os.Exit(1)
		}
	}
	var err error
	// Locate ffmpeg tool
	ffmpegBin, err = exec.LookPath("ffmpeg")
	criticalErr(err, "没有找到 ffmpeg 工具")

	fmt.Print("请输入您要下载的B站直播回放链接地址: ")
	line, _, err := bufio.NewReader(os.Stdin).ReadLine()
	criticalErr(err, "读取用户输入")

	recordId := strings.TrimSpace(strings.Split(string(line), "?")[0])
	rIds := strings.Split(recordId, "/")
	recordId = rIds[len(rIds)-1]
	fmt.Printf("直播回放ID是 %s\n", recordId)

	videoInfo, err := fetchRecordInfo(recordId)
	criticalErr(err, "加载直播回放信息")
	liverInfo, err := fetchLiverInfo(videoInfo.RoomID)
	criticalErr(err, "加载主播信息")
	recordInfo, err := fetchRecordParts(recordId)
	criticalErr(err, "加载视频分段信息")

	fmt.Printf(
		"%s(UID:%d)《%s》直播时间%s ~ %s，时长%v，画质：%s，总大小%s（共%d部分）\n",
		liverInfo.UserName,
		liverInfo.UserID,
		videoInfo.Title,
		videoInfo.Start, videoInfo.End, recordInfo.Length,
		recordInfo.Quality(),
		recordInfo.Size, len(recordInfo.List),
	)
	partStart := videoInfo.Start
	for i, v := range recordInfo.List {
		partEnd := JSONTime{partStart.Add(v.Length.Duration)}
		fmt.Printf("%d\t%s\t长度%s\t大小%s\t%s ~ %s\n", i+1, v.FileName(), v.Length, v.Size, partStart, partEnd)
		partStart = partEnd
	}

	// Mkdir
	cwd, err := os.Getwd()
	criticalErr(err, "检测当前目录")

	// Ask user what to do
	downloadList, err := SelectFromList(len(recordInfo.List), "要下载哪些分段？请输入分段的序号，用英文逗号分隔（输入0来下载所有分段并合并成单个视频）: ")
	criticalErr(err, "读取用户选择")
	fmt.Printf("将下载这些分段: %s\n", downloadList)

	recordDownloadDir := filepath.Join(
		cwd,
		fmt.Sprintf("%d-%s", liverInfo.UserID, liverInfo.UserName),
		fmt.Sprintf("%s-%s", strings.ReplaceAll(videoInfo.Start.String(), ":", "-"), recordId),
	)
	criticalErr(os.MkdirAll(recordDownloadDir, 0755), "建立下载目录")
	fmt.Printf("下载目录: \"%s\"\n", recordDownloadDir)

	{
		infoFile := filepath.Join(recordDownloadDir, "直播信息.txt")
		info := strings.Builder{}
		info.WriteString(fmt.Sprintf("直播间ID：%d\n", videoInfo.RoomID))
		info.WriteString(fmt.Sprintf("主播UID：%d，用户名：%s\n", liverInfo.UserID, liverInfo.UserName))
		info.WriteString(fmt.Sprintf("直播标题：%s\n", videoInfo.Title))
		info.WriteString(fmt.Sprintf("开始于：%s\n", videoInfo.Start))
		info.WriteString(fmt.Sprintf("结束于：%s\n", videoInfo.End))
		info.WriteString(fmt.Sprintf("共%d部分\n", len(recordInfo.List)))
		info.WriteString(fmt.Sprintf("选择下载的分段：%s\n", downloadList))
		criticalErr(ioutil.WriteFile(infoFile, []byte(info.String()), 0755), "写入直播回放信息")
	}

	decappedFiles, err := downloadRecordParts(recordInfo, downloadList, recordDownloadDir)
	criticalErr(err, "下载直播回放分段")

	// All parts downloaded, concat into a single file.
	if len(downloadList) == len(recordInfo.List) && len(decappedFiles) == len(recordInfo.List) {
		fmt.Println("所有回放分段都已下载，合并为单个视频")
		output := filepath.Join(
			recordDownloadDir,
			fmt.Sprintf(
				"%s-%s-%s-%s-complete.mp4",
				strings.ReplaceAll(videoInfo.Start.String(), ":", "-"),
				recordId,
				videoInfo.Title,
				recordInfo.Quality(),
			),
		)
		criticalErr(concatRecordParts(decappedFiles, output), "合并视频分段")

		for _, i := range downloadList {
			if filePath, ok := decappedFiles[i]; ok && filePath != "" {
				fmt.Printf("删除文件%s\n", filePath)
				os.Remove(filePath)
			}
		}

		fmt.Printf("完整回放下载完毕: %s\n", output)
		return
	}

	fmt.Println("下载结果:")
	for _, i := range downloadList {
		if filePath, ok := decappedFiles[i]; ok {
			fmt.Printf("第%d部分: 下载完成 %s\n", i, filePath)
		} else {
			fmt.Printf("第%d部分: 未下载成功\n", i)
		}
	}
}
