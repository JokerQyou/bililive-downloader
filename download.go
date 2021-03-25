package main

import (
	"bililive-downloader/ffmpeg"
	"bililive-downloader/models"
	"bililive-downloader/progressbar"
	"fmt"
	"github.com/cavaliercoder/grab"
	"github.com/gosuri/uiprogress"
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
	task.SetCurrentStep("解包")
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
func downloadRecordParts(recordInfo *models.RecordParts, downloadList *models.IntSelection, where string, concurrency uint) (filePaths map[int]string, err error) {
	taskQueue := make(chan *models.PartTask)

	filePaths = make(map[int]string)
	var filePathUpdater sync.Mutex

	var wg sync.WaitGroup

	if int(concurrency) > downloadList.Count() {
		concurrency = uint(downloadList.Count())
		fmt.Printf("已自动调整下载并发数为 %d\n", concurrency)
	} else {
		fmt.Printf("下载并发数 %d\n", concurrency)
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

	bar := progressbar.AddProgressBar(-1)
	bar.SetPrefixDecorator(func(b *uiprogress.Bar) string {
		return fmt.Sprintf("合并%d个视频\t%s\t", len(inputFiles), filepath.Base(output))
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
