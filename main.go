package main

import (
	"bililive-downloader/ffmpeg"
	"bililive-downloader/helper"
	"bililive-downloader/models"
	"bililive-downloader/progressbar"
	"bufio"
	"errors"
	"fmt"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const timeFormat = "2006-01-02 15:04:05"
const defaultConcurrency = 2

var logger zerolog.Logger

type DownloadParam struct {
	RecordID     string                 // Record ID
	Info         *models.LiveRecordInfo // Record info
	Parts        *models.RecordParts    // Video parts
	Liver        *models.LiverInfo      // Livestreamer info
	DownloadList *models.IntSelection   // selected part numbers
	Concurrency  uint
}

func handleDownloadAction(c *cli.Context) error {
	var param DownloadParam
	if c.Bool("interactive") {
		// Ask user for parameters
		var err error
		if param, err = askForArguments(); err != nil {
			return err
		}
	} else {
		// Extract parameters from cli context
		if recordID, err := extractRecordID(c.String("record")); err != nil {
			return cli.Exit(err.Error(), 1)
		} else {
			param.RecordID = recordID
			logger.Info().Str("ID", param.RecordID).Msg("直播回放ID确认")
		}

		// FIXME
		if selected := strings.ToLower(c.String("select")); selected == "" {
			logger.Error().Msg("没有指定要下载的分段")
			return cli.Exit("没有指定要下载的分段", 1)
		} else {
			selection, err := models.ParseStringFromString(selected)
			if err != nil {
				logger.Error().Err(err).Str("输入的选择", selected).Msg("无法解析输入的选择")
				return cli.Exit("无法解析输入的选择", 1)
			}
			if selection.Count() == 0 {
				logger.Error().Str("输入的选择", selected).Msg("没有选择要下载的分段")
				return cli.Exit("没有选择要下载的分段", 1)
			}
			param.DownloadList = selection
			logger.Info().Stringer("选择的分段", param.DownloadList).Send()
		}

		if concurrency := c.Uint("concurrency"); concurrency == 0 {
			param.Concurrency = defaultConcurrency
			logger.Info().Uint("并发数", param.Concurrency).Msg("使用默认下载并发数")
		} else {
			param.Concurrency = concurrency
			logger.Info().Uint("并发数", param.Concurrency).Msg("指定了下载并发数")
		}

		if recordInfo, err := fetchRecordInfo(param.RecordID); err != nil {
			logger.Error().Err(err).Msg("加载回放信息出错")
			return cli.Exit("加载回放信息出错", 1)
		} else {
			param.Info = recordInfo
		}

		if liverInfo, err := fetchLiverInfo(param.Info.RoomID); err != nil {
			logger.Error().Err(err).Msg("加载直播间信息出错")
			return cli.Exit("加载直播间信息出错", 1)
		} else {
			param.Liver = liverInfo
		}

		if parts, err := fetchRecordParts(param.RecordID); err != nil {
			logger.Error().Err(err).Msg("加载回放分段信息出错")
			return cli.Exit("加载回放分段信息出错", 1)
		} else {
			param.Parts = parts
		}

		logger.Debug().Interface("param", param).Send()
	}

	// Setup progress bar manager only if we're connected to a TTY
	if isTTY() {
		progressbar.Init(os.Stdout)
		logger.Debug().Msg("将显示进度条")
	} else {
		// progress disabled
		progressbar.Init(ioutil.Discard)
		logger.Debug().Msg("不在终端中运行，将不显示进度条")
	}

	return download(param)
}

func extractRecordID(link string) (string, error) {
	recordId := strings.TrimSpace(strings.Split(string(link), "?")[0])
	rIds := strings.Split(recordId, "/")
	recordId = rIds[len(rIds)-1]
	if recordId == "" {
		return "", errors.New("没有指定直播回放ID")
	}

	return recordId, nil
}

func askForArguments() (DownloadParam, error) {
	var param DownloadParam
	var err error

	fmt.Print("请输入您要下载的B站直播回放链接地址: ")
	var line []byte
	if line, _, err = bufio.NewReader(os.Stdin).ReadLine(); err != nil {
		logger.Error().Err(err).Msg("无法读取用户输入")
		return param, err
	}
	recordId, err := extractRecordID(string(line))
	if err != nil {
		return param, err
	}
	fmt.Printf("直播回放ID是 %s\n", recordId)

	videoInfo, err := fetchRecordInfo(recordId)
	if err != nil {
		logger.Error().Err(err).Msg("查询直播回放信息出错")
		return param, err
	}

	liverInfo, err := fetchLiverInfo(videoInfo.RoomID)
	if err != nil {
		logger.Error().Err(err).Msg("查询主播信息出错")
		return param, err
	}

	recordInfo, err := fetchRecordParts(recordId)
	if err != nil {
		logger.Error().Err(err).Msg("查询直播回放分片出错")
		return param, err
	}

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
		partEnd := helper.JSONTime{Time: partStart.Add(v.Length.Duration)}
		fmt.Printf("%d\t%s\t长度%s\t大小%s\t%s ~ %s\n", i+1, v.FileName(), v.Length, v.Size, partStart, partEnd)
		partStart = partEnd
	}

	// Ask user what to do
	fmt.Print("要下载哪些分段？请输入分段的序号，用英文逗号分隔（输入all来下载所有分段并合并成单个视频）: ")
	input, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		logger.Error().Err(err).Msg("读取用户选择出错")
		return param, err
	}
	downloadList, err := models.ParseStringFromString(string(input))
	fmt.Printf("将下载这些分段: %s\n", downloadList)

	// Ask user about concurrency
	var concurrency uint = 2
	fmt.Print("下载并发数（可同时进行多少个分段的下载。默认为2，如果您的网络较好，可适当增加）: ")
	line, _, err = bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		concurrency = 2
		logger.Error().Err(err).Uint("下载并发数", concurrency).Msg("读取用户输入出错，回退到默认下载并发数")
	} else {
		con32, parseErr := strconv.ParseUint(strings.TrimSpace(string(line)), 10, 32)
		if parseErr != nil {
			concurrency = 2
			logger.Info().Err(parseErr).Uint("下载并发数", concurrency).Msg("解析错误，回退到默认下载并发数")
		} else {
			concurrency = uint(con32)
			logger.Info().Uint("下载并发数", concurrency).Msg("用户指定了下载并发数")
		}
	}

	param = DownloadParam{
		RecordID:     recordId,
		Info:         videoInfo,
		Parts:        recordInfo,
		Liver:        liverInfo,
		DownloadList: downloadList,
		Concurrency:  concurrency,
	}
	return param, nil
}

func download(p DownloadParam) error {
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
	logger.Info().Str("下载目录", recordDownloadDir).Msg("下载目录确认完成")

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

	decappedFiles, err := downloadRecordParts(p.Parts, p.DownloadList, recordDownloadDir, p.Concurrency)
	if err != nil {
		logger.Fatal().Err(err).Msg("下载直播回放出错")
	}

	// All parts downloaded, concat into a single file.
	if p.DownloadList.IsFull() && len(decappedFiles) == len(p.Parts.List) {
		logger.Info().Interface("下载的分段", p.DownloadList).Msg("合并为单个视频")
		output := filepath.Join(
			recordDownloadDir,
			fmt.Sprintf(
				"%s-%s-%s-%s-complete.mp4",
				strings.ReplaceAll(p.Info.Start.String(), ":", "-"),
				p.RecordID,
				p.Info.Title,
				p.Parts.Quality(),
			),
		)
		if err := concatRecordParts(decappedFiles, output); err != nil {
			logger.Fatal().Err(err).Interface("下载的分段", p.DownloadList).Str("合并后的文件", output).Msg("合并视频分段出错")
		}
		progressbar.Stop()

		for _, i := range p.DownloadList.AsIntSlice() {
			if filePath, ok := decappedFiles[i]; ok && filePath != "" {
				logger.Info().Str("文件", filePath).Msg("删除中间文件")
				os.Remove(filePath)
			}
		}

		logger.Info().Str("合并后的文件", output).Msg("完整回放下载完毕")
		return nil
	} else {
		progressbar.Stop()
	}

	for _, i := range p.DownloadList.AsIntSlice() {
		if filePath, ok := decappedFiles[i]; ok {
			logger.Info().Str("文件", filePath).Msgf("第%d部分下载完成", i)
		} else {
			logger.Warn().Msgf("第%d部分下载不成功", i)
		}
	}

	return nil
}

func isTTY() bool {
	info, _ := os.Stdout.Stat()
	return info.Mode()&os.ModeCharDevice != 0
}

func handleVersionCommand(c *cli.Context) error {
	return errors.New("DEBUG!")
}

func main() {
	logger = log.Output(zerolog.ConsoleWriter{
		NoColor:    !isTTY(),
		Out:        os.Stdout,
		TimeFormat: timeFormat,
	})

	// Setup ffmpeg tools
	ffmpegBin, err := exec.LookPath("ffmpeg")
	if err != nil {
		logger.Fatal().Err(err).Msg("没有找到ffmpeg工具")
	}
	ffprobeBin, err := exec.LookPath("ffprobe")
	if err != nil {
		logger.Fatal().Err(err).Msg("没有找到ffprobe工具")
	}
	ffmpeg.Init(ffmpegBin, ffprobeBin)

	defaultSelection, _ := models.ParseStringFromString("all")

	app := &cli.App{
		Name:  "bililive-downloader",
		Usage: "Download livestream recordings from Bilibili",
		Flags: []cli.Flag{},
		Commands: []*cli.Command{
			{
				Name:    "version",
				Aliases: []string{"v"},
				Usage:   "显示版本信息",
				Action:  handleVersionCommand,
			},
			{
				Name:   "download",
				Usage:  "下载直播回放",
				Action: handleDownloadAction,
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "interactive", Aliases: []string{"i"}, Usage: "交互式询问各个参数。指定此选项时，其他参数都被忽略。", Value: false},
					&cli.UintFlag{Name: "concurrency", Usage: "设定下载的`并发数`。如果您的网络较好，可适当调高。", Value: defaultConcurrency},
					&cli.StringFlag{Name: "select", Usage: "指定要下载的`分段编号`，以逗号分隔。all表示指定所有分段。如果指定所有分段，则下载完成后会合并为单个文件。", Value: defaultSelection.String()},
					&cli.StringFlag{Name: "record", Usage: "直播回放的`链接或ID`。"},
				},
			},
		},
	}
	app.Run(os.Args)
}
