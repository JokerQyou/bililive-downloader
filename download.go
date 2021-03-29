package main

import (
	"bililive-downloader/ffmpeg"
	"bililive-downloader/helper"
	"bililive-downloader/models"
	"bililive-downloader/progressbar"
	"fmt"
	"github.com/cavaliercoder/grab"
	"github.com/gosuri/uiprogress"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const UaKey = "User-Agent"
const UserAgent = "Mozilla/5.0 (Windows NT 6.1; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/55.0.2883.87 Safari/537.36"

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
	task.SetCurrentStep("解包中")
	task.SetFileName(filepath.Base(decappedTsFilePath))
	bar.SetUnitType(progressbar.UnitTypeDuration)
	runner, _ := ffmpeg.NewRunner("-i", rawFilePath, "-c", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "mpegts", decappedTsFilePath)
	runner.ProbeMediaDuration(rawFilePath)
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
			task.SetCurrentStep("已完成")
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
func downloadRecordParts(recordInfo *models.RecordParts, downloadList []int, where string, concurrency uint) (filePaths map[int]string, err error) {
	taskQueue := make(chan *models.PartTask)

	filePaths = make(map[int]string)
	var filePathUpdater sync.Mutex

	var wg sync.WaitGroup

	if int(concurrency) > len(downloadList) {
		concurrency = uint(len(downloadList))
		logger.Info().Uint("下载并发数", concurrency).Msg("自动调整下载并发数")
	}

	for i := 0; i < int(concurrency); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for task := range taskQueue {
				downloadTask := task
				time.Sleep(time.Millisecond * 20 * time.Duration(downloadTask.PartNumber))

				func() {
					downloadedFilePath, err := downloadSinglePart(downloadTask)
					if err != nil {
						logger.Error().Err(err).Int("编号", downloadTask.PartNumber).Msg("下载出错")
						downloadTask.SetCurrentStep("已出错")
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
		if !helper.ContainsInt(downloadList, i+1) {
			continue
		}

		task := &models.PartTask{
			PartNumber:        i + 1,
			Part:              &recordPart,
			DownloadDirectory: where,
		}
		task.SetCurrentStep("等待中")
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

	bar := progressbar.AddProgressBar(-1)
	bar.SetPrefixDecorator(func(b *uiprogress.Bar) string {
		return "合并"
	})
	bar.SetUnitType(progressbar.UnitTypeDuration)

	// Concat TS containers (with H.264 media) together into a single MP4 container.
	concatList := make([]string, len(inputFiles))
	for i, filePath := range inputFiles {
		concatList[i-1] = filePath
	}

	runner, _ := ffmpeg.NewRunner(
		"-i", fmt.Sprintf("concat:%s", strings.Join(concatList, "|")),
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		output,
	)
	runner.ProbeMediaDuration(concatList...)
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

type DownloadParam struct {
	RecordID     string                 // Record ID
	Info         *models.LiveRecordInfo // Record info
	Parts        *models.RecordParts    // Video parts
	Liver        *models.LiverInfo      // Livestreamer info
	DownloadList []int                  // selected part numbers
	Concurrency  uint
}

func cliDownload(p DownloadParam) error {
	// Mkdir
	cwd, err := os.Getwd()
	if err != nil {
		logger.Fatal().Err(err).Msg("检测当前目录出错")
	}

	recordDownloadDir := filepath.Join(
		cwd,
		fmt.Sprintf("%d-%s", p.Liver.UserID, p.Liver.UserName),
		fmt.Sprintf("%s-%s-%s", strings.ReplaceAll(p.Info.Start.String(), ":", "-"), p.Info.Title, p.RecordID),
	)
	if err := os.MkdirAll(recordDownloadDir, 0755); err != nil {
		logger.Fatal().Err(err).Str("下载目录", recordDownloadDir).Msg("建立下载目录出错")
	}
	logger.Info().Str("下载目录", recordDownloadDir).Send()

	{
		infoFile := filepath.Join(recordDownloadDir, "直播信息.txt")
		info := strings.Builder{}
		info.WriteString(fmt.Sprintf("直播间ID：%d\n", p.Info.RoomID))
		info.WriteString(fmt.Sprintf("主播UID：%d，用户名：%s\n", p.Liver.UserID, p.Liver.UserName))
		info.WriteString(fmt.Sprintf("直播标题：%s\n", p.Info.Title))
		info.WriteString(fmt.Sprintf("开始于：%s\n", p.Info.Start))
		info.WriteString(fmt.Sprintf("结束于：%s\n", p.Info.End))
		info.WriteString(fmt.Sprintf("共%d部分\n", len(p.Parts.List)))
		info.WriteString(fmt.Sprintf("选择下载的分段：%s\n", p.DownloadList))
		if err := ioutil.WriteFile(infoFile, []byte(info.String()), 0755); err != nil {
			logger.Error().Err(err).Str("直播信息文件", infoFile).Msg("写入直播回放信息出错")
		}
	}

	progressbar.Start()

	fullRecordFile := filepath.Join(
		recordDownloadDir,
		fmt.Sprintf(
			"%s-%s-%s-%s-complete.mp4",
			strings.ReplaceAll(p.Info.Start.String(), ":", "-"),
			p.RecordID,
			p.Info.Title,
			p.Parts.Quality(),
		),
	)

	// Skip if the full recording is already downloaded.
	if _, err := os.Stat(fullRecordFile); !os.IsNotExist(err) {
		logger.Debug().Str("文件", filepath.Base(fullRecordFile)).Msg("完整直播回放文件已存在，检查媒体时长")
		inspector, _ := ffmpeg.NewRunner("--help")
		fullRecordDuration, err := inspector.ProbSingleMediaDuration(fullRecordFile)
		if err != nil {
			logger.Error().Err(err).Str("文件", filepath.Base(fullRecordFile)).Msg("检查媒体文件出错")
			return err
		}

		if math.Abs(float64(p.Parts.Length.Duration-fullRecordDuration)) < float64(time.Second*10) {
			logger.Info().Str("文件", filepath.Base(fullRecordFile)).Msg("完整直播回放文件已存在，跳过下载")
			return nil
		}
	}

	decappedFiles, err := downloadRecordParts(p.Parts, p.DownloadList, recordDownloadDir, p.Concurrency)
	if err != nil {
		logger.Fatal().Err(err).Msg("下载直播回放出错")
	}

	// All parts downloaded, concat into a single file.
	if len(p.DownloadList) == len(p.Parts.List) && len(decappedFiles) == len(p.Parts.List) {
		logger.Info().Ints("下载的分段", p.DownloadList).Msg("合并为单个视频")
		if err := concatRecordParts(decappedFiles, fullRecordFile); err != nil {
			logger.Fatal().Err(err).Ints("下载的分段", p.DownloadList).Str("合并后的文件", fullRecordFile).Msg("合并视频分段出错")
		}
		progressbar.Stop()

		for _, filePath := range decappedFiles {
			err = os.Remove(filePath)
			logger.Debug().Err(err).Str("文件", filePath).Msg("删除中间文件")
		}

		logger.Info().Str("合并后的文件", fullRecordFile).Msg("完整回放下载完毕")
		return nil
	} else {
		progressbar.Stop()
	}

	for _, i := range p.DownloadList {
		if filePath, ok := decappedFiles[i]; ok {
			logger.Info().Str("文件", filePath).Msgf("第%d部分下载完成", i)
		} else {
			logger.Warn().Msgf("第%d部分下载不成功", i)
		}
	}

	return nil
}
