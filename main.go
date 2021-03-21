package main

import (
	"bililive-downloader/models"
	"bufio"
	"fmt"
	"github.com/cavaliercoder/grab"
	"github.com/gosuri/uiprogress"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const UaKey = "User-Agent"
const UserAgent = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/55.0.2883.87 Safari/537.36"

var ffmpegBin string
var ffprobeBin string

// downloadSinglePart downloads given part (as encoded in `task`) into given directory.
// Downloaded file will also be de-capped to MPEGTS media, the intermediate FLV file will be deleted.
func downloadSinglePart(task *models.PartTask) (filePath string, err error) {
	recordPart := task.Part

	rawFilePath := filepath.Join(task.DownloadDirectory, recordPart.FileName())
	decappedTsFilePath := fmt.Sprintf("%s.ts", strings.Split(rawFilePath, ".")[0])

	bar := task.AddProgressBar(-1)

	// Already processed (probably selectively downloaded this part before), use existing MPEGTS media as result, no need to re-download.
	if info, err := os.Stat(decappedTsFilePath); err == nil && info.Mode().IsRegular() {
		task.SetCurrentStep("已存在")
		task.SetFileName(filepath.Base(decappedTsFilePath))
		bar.SetTotal(info.Size())
		bar.SetCurrent(info.Size())
		return decappedTsFilePath, nil
	}

	var client *grab.Client
	var dlReq *grab.Request
	var resp *grab.Response
	var ticker *time.Ticker

	// Already downloaded, directly proceed to de-cap, skip downloading.
	if info, err := os.Stat(rawFilePath); err == nil && info.Size() == int64(recordPart.Size.Bytes()) {
		task.SetCurrentStep("已下载")
		bar.SetTotal(info.Size())
		bar.SetCurrent(info.Size())
		goto WaitTillDecapped
	}

	bar.SetTotal(int64(task.Part.Size.Bytes()))
	task.SetCurrentStep("下载中")
	client = grab.NewClient()
	client.UserAgent = UserAgent
	dlReq, err = grab.NewRequest(rawFilePath, recordPart.Url)
	if err != nil {
		return
	}

	resp = client.Do(dlReq)
	ticker = time.NewTicker(time.Millisecond * 120)
	defer ticker.Stop()

WaitTillDownloaded:
	for {
		select {
		case <-ticker.C:
			bar.SetCurrent(resp.BytesComplete())
		case <-resp.Done:
			bar.SetCurrent(resp.BytesComplete())
			break WaitTillDownloaded
		}
	}

WaitTillDecapped:
	// De-cap from FLV to MPEG TS media
	// TODO Are we confident enough that all bilibili livestream records will be H.264 streams encapsulated in FLV containers?
	task.SetCurrentStep("解包")
	task.SetFileName(filepath.Base(decappedTsFilePath))
	bar.SetUnitType(models.UnitTypeDuration)
	runner := NewFFmpegRunner("-i", rawFilePath, "-c", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "mpegts", decappedTsFilePath)
	runner.ProbeMediaDuration([]string{rawFilePath})
	runner.SetTimeout(time.Minute * 15)
	var decapProgTotalSet bool
	err = runner.Run(func(current, total int64) {
		if !decapProgTotalSet {
			bar.SetTotal(total)
			decapProgTotalSet = true
		}
		bar.SetCurrent(current)
	})

	// 解包后对TS媒体进行检查，如果长度相差过大则认为解包失败，保留FLV文件以供后续人工检视
	durationMatch := func(tsDuration time.Duration) bool {
		return math.Abs(float64(task.Part.Length.Duration-tsDuration)) < float64(time.Second*3)
	}
	if err == nil {
		task.SetCurrentStep("检查中")
		if tsDuration, err := runner.ProbSingleMediaDuration(decappedTsFilePath); err == nil && durationMatch(tsDuration) {
			os.Remove(rawFilePath)
			task.SetCurrentStep("完成")
		} else {
			task.SetCurrentStep(fmt.Sprintf("出错: 解包后媒体长度相差%s", task.Part.Length.Duration-tsDuration))
		}
	} else {
		task.SetCurrentStep(fmt.Sprintf("出错: %v", err))
	}

	return decappedTsFilePath, err
}

// downloadRecordParts download selected parts (`downloadList`) of given livestream record into `where`.
// It also manages the progress bar and concurrency of downloading (`concurrency`).
func downloadRecordParts(recordInfo *models.RecordParts, downloadList models.IntSelection, where string, concurrency int) (filePaths map[int]string, err error) {
	taskQueue := make(chan *models.PartTask)

	filePaths = make(map[int]string)
	var filePathUpdater sync.Mutex

	var wg sync.WaitGroup

	if concurrency > len(downloadList) {
		concurrency = len(downloadList)
		fmt.Printf("已自动调整下载并发数为 %d\n", concurrency)
	} else {
		fmt.Printf("下载并发数 %d\n", concurrency)
	}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for task := range taskQueue {
				downloadTask := task
				time.Sleep(time.Millisecond * 20 * time.Duration(downloadTask.PartNumber))

				func() {
					downloadedFilePath, err := downloadSinglePart(downloadTask)
					if err != nil {
						downloadTask.SetCurrentStep(fmt.Sprintf("出错: %v", err))
					} else {
						filePathUpdater.Lock()
						defer filePathUpdater.Unlock()
						filePaths[downloadTask.PartNumber] = downloadedFilePath
					}
				}()
			}
		}()
	}

	// Generate and insert tasks.
	for i, part := range recordInfo.List {
		recordPart := part
		index := i
		if !downloadList.Contains(index + 1) {
			continue
		}

		task := &models.PartTask{
			PartNumber:        index + 1,
			Part:              &recordPart,
			DownloadDirectory: where,
		}
		task.SetCurrentStep("等待下载")
		task.SetFileName(recordPart.FileName())
		taskQueue <- task
	}

	close(taskQueue)
	wg.Wait()

	return
}

// concatRecordParts concatenates multiple record parts into a single MP4 file.
func concatRecordParts(inputFiles map[int]string, output string) error {
	if info, err := os.Stat(output); err == nil && info.Mode().IsRegular() {
		return fmt.Errorf("文件 %s 已经存在", output)
	}

	bar := models.AddProgressBar(-1)
	bar.SetPrefixDecorator(func(b *uiprogress.Bar) string {
		return fmt.Sprintf("合并%d个视频\t%s\t", len(inputFiles), filepath.Base(output))
	})
	bar.SetUnitType(models.UnitTypeDuration)

	// Concat TS containers (with H.264 media) together into a single MP4 container.
	concatList := make([]string, len(inputFiles))
	for i, filePath := range inputFiles {
		concatList[i-1] = filePath
	}

	runner := NewFFmpegRunner(
		"-i", fmt.Sprintf("concat:%s", strings.Join(concatList, "|")),
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		output,
	)
	runner.ProbeMediaDuration(concatList)
	runner.SetTimeout(time.Minute * 20)
	var progTotalSet bool
	return runner.Run(func(current, total int64) {
		if !progTotalSet {
			bar.SetTotal(total)
			progTotalSet = true
		}
		bar.SetCurrent(current)
	})
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
	// Locate ffprobe tool (should be installed with ffmpeg suite)
	ffprobeBin, err = exec.LookPath("ffprobe")
	criticalErr(err, "没有找到 ffprobe 工具")

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
		partEnd := models.JSONTime{partStart.Add(v.Length.Duration)}
		fmt.Printf("%d\t%s\t长度%s\t大小%s\t%s ~ %s\n", i+1, v.FileName(), v.Length, v.Size, partStart, partEnd)
		partStart = partEnd
	}

	// Mkdir
	cwd, err := os.Getwd()
	criticalErr(err, "检测当前目录")

	// Ask user what to do
	downloadList, err := models.SelectFromList(len(recordInfo.List), "要下载哪些分段？请输入分段的序号，用英文逗号分隔（输入0来下载所有分段并合并成单个视频）: ")
	criticalErr(err, "读取用户选择")
	fmt.Printf("将下载这些分段: %s\n", downloadList)

	// Ask user about concurrency
	concurrency := 2
	fmt.Print("下载并发数（可同时进行多少个分段的下载。默认为2，如果您的网络较好，可适当增加）: ")
	line, _, err = bufio.NewReader(os.Stdin).ReadLine()
	con32, parseErr := strconv.ParseInt(strings.TrimSpace(string(line)), 10, 32)
	if err != nil || parseErr != nil {
		fmt.Printf("解析错误，使用默认下载并发数: %d\n", concurrency)
	} else {
		concurrency = int(con32)
		fmt.Printf("指定了下载并发数: %d\n", concurrency)
	}

	recordDownloadDir := filepath.Join(
		cwd,
		fmt.Sprintf("%d-%s", liverInfo.UserID, liverInfo.UserName),
		fmt.Sprintf("%s-%s-%s", strings.ReplaceAll(videoInfo.Start.String(), ":", "-"), videoInfo.Title, recordId),
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

	uiprogress.Start()

	decappedFiles, err := downloadRecordParts(recordInfo, downloadList, recordDownloadDir, concurrency)
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
		uiprogress.Stop()

		for _, i := range downloadList {
			if filePath, ok := decappedFiles[i]; ok && filePath != "" {
				fmt.Printf("删除文件%s\n", filePath)
				os.Remove(filePath)
			}
		}

		fmt.Printf("完整回放下载完毕: %s\n", output)
		return
	} else {
		uiprogress.Stop()
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
