package main

import (
	"bililive-downloader/helper"
	"bililive-downloader/models"
	"bililive-downloader/progressbar"
	"bufio"
	"errors"
	"fmt"
	"github.com/urfave/cli/v2"
	"io"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultConcurrency = 2
const (
	returnCodeOk int = iota
	returnCodeError
)

var timezone *time.Location
var initGuard sync.Once

func init() {
	initGuard.Do(func() {
		timezone, _ = time.LoadLocation("Asia/Shanghai")
	})
}

// extractRecordID extract livestream record ID from given string.
func extractRecordID(link string) (string, error) {
	recordId := strings.TrimSpace(strings.Split(link, "?")[0])
	rIds := strings.Split(recordId, "/")
	recordId = strings.TrimSpace(rIds[len(rIds)-1])
	if recordId == "" {
		return "", errors.New("没有指定直播回放ID")
	}

	return recordId, nil
}

// handleDownloadAction handles `download` subcommand. The only error it might return is cli.Exit.
func handleDownloadAction(c *cli.Context) error {
	var err error
	interactive := c.Bool("interactive")

	var param DownloadParam

	if strings.TrimSpace(c.String("record")) == "" && interactive {
		var recordLink string
		if recordLink, err = ask("请输入您要下载的B站直播回放链接地址: "); err != nil {
			return cli.Exit(err, returnCodeError)
		}

		c.Set("record", recordLink)
	}

	// Extract parameters from cli context
	if recordID, err := extractRecordID(c.String("record")); err != nil {
		return cli.Exit(err.Error(), returnCodeError)
	} else {
		param.RecordID = recordID
		logger.Info().Str("ID", param.RecordID).Msg("直播回放ID确认")
	}

	// Ask user about concurrency
	var concurrency uint
	if concurrency = c.Uint("concurrency"); concurrency == 0 && interactive {
		var concurrencyStr string
		concurrencyStr, err = ask("下载并发数（可同时进行多少个分段的下载。默认为2，如果您的网络较好，可适当增加）: ")
		if err != nil {
			return cli.Exit(err, returnCodeError)
		} else {
			if con32, parseErr := strconv.ParseUint(concurrencyStr, 10, 32); parseErr == nil {
				concurrency = uint(con32)
				logger.Info().Uint("下载并发数", concurrency).Send()
			}
		}
	}

	if concurrency == 0 {
		concurrency = defaultConcurrency
		logger.Info().Uint("下载并发数", concurrency).Msg("使用默认下载并发数")
	}
	param.Concurrency = concurrency

	if recordInfo, err := fetchRecordInfo(param.RecordID); err != nil {
		logger.Error().Err(err).Msg("加载回放信息出错")
		return cli.Exit("加载回放信息出错", returnCodeError)
	} else {
		param.Info = recordInfo
	}

	if liverInfo, err := fetchLiverInfo(param.Info.RoomID); err != nil {
		logger.Error().Err(err).Msg("加载直播间信息出错")
		return cli.Exit("加载直播间信息出错", returnCodeError)
	} else {
		param.Liver = liverInfo
	}

	if parts, err := fetchRecordParts(param.RecordID); err != nil {
		logger.Error().Err(err).Msg("加载回放分段信息出错")
		return cli.Exit("加载回放分段信息出错", returnCodeError)
	} else {
		param.Parts = parts
	}

	// Interactive mode, ask again, for part selection.
	if interactive {
		var selectionMessenger strings.Builder
		selectionMessenger.WriteString(fmt.Sprintf(
			"%s(UID:%d)《%s》直播时间%s ~ %s，时长%v，画质：%s，总大小%s（共%d部分）\n",
			param.Liver.UserName,
			param.Liver.UserID,
			param.Info.Title,
			param.Info.Start, param.Info.End, param.Parts.Length,
			param.Parts.Quality(),
			param.Parts.Size, len(param.Parts.List),
		))
		partStart := param.Info.Start
		for i, v := range param.Parts.List {
			// Parse part start from filename
			// ***REMOVED***
			fields := strings.SplitN(strings.SplitN(v.FileName(), ".", 2)[0], "-", 2)
			fileStartTimeStr := fields[len(fields)-1]
			start, err := time.ParseInLocation("2006-01-02-15-04-05", fileStartTimeStr, timezone)
			if err == nil {
				partStart = helper.JSONTime{Time: start}
			}
			partEnd := helper.JSONTime{Time: partStart.Add(v.Length.Duration)}
			selectionMessenger.WriteString(fmt.Sprintf("%d\t%s\t长度%s\t大小%s\t%s ~ %s\n", i+1, v.FileName(), v.Length, v.Size, partStart, partEnd))
			partStart = partEnd
		}
		selectionMessenger.WriteString("要下载哪些分段？请输入分段的序号，用英文逗号分隔（输入all来下载所有分段并合并成单个视频）: ")

		var userSelection string
		if userSelection, err = ask(selectionMessenger.String()); err != nil {
			return cli.Exit(err, returnCodeError)
		}
		c.Set("select", userSelection)
	}

	{
		selected := strings.ToLower(c.String("select"))
		if selected == "" {
			selected = "all"
		}

		selection, err := models.ParseStringFromString(selected)
		if err != nil {
			logger.Error().Err(err).Str("输入的选择", selected).Msg("无法解析输入的选择")
			return cli.Exit("无法解析输入的选择", returnCodeError)
		}
		if selection.Count() == 0 {
			logger.Error().Str("输入的选择", selected).Msg("没有选择要下载的分段")
			return cli.Exit("没有选择要下载的分段", returnCodeError)
		}
		param.DownloadList = selection
		logger.Info().Stringer("选择的分段", param.DownloadList).Send()
	}

	logger.Debug().Interface("param", param).Send()

	// Setup progress bar manager only if we're connected to a TTY
	var progressWriter io.Writer = os.Stdout
	if !helper.IsTTY() {
		progressWriter = ioutil.Discard
		logger.Debug().Msg("不在终端中运行，将不显示进度条")
	}
	progressbar.Init(progressWriter)

	return download(param)
}

// ask asks a question (prints given `msg`), and read user's answer via `os.Stdin`.
func ask(msg string) (string, error) {
	fmt.Print(msg)

	line, _, err := bufio.NewReader(os.Stdin).ReadLine()
	if err != nil {
		logger.Error().Err(err).Send()
		return "", errors.New("读取用户输入出错")
	}

	return strings.TrimSpace(string(line)), nil
}

// handleVersionCommand handles `version` subcommand. It prints version of this program.
func handleVersionCommand(c *cli.Context) error {
	return cli.Exit("DEBUG!", returnCodeOk)
}

// newCliApp creates a cli.App instance to handle commandline execution.
func newCliApp() *cli.App {
	return &cli.App{
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
					&cli.BoolFlag{Name: "interactive", Aliases: []string{"i"}, Usage: "交互式询问各个未传递的参数。", Value: false},
					&cli.UintFlag{Name: "concurrency", Usage: "设定下载的`并发数`。如果您的网络较好，可适当调高。"},
					&cli.StringFlag{Name: "select", Usage: "指定要下载的`分段编号`，以逗号分隔。all表示指定所有分段。如果指定所有分段，则下载完成后会合并为单个文件。"},
					&cli.StringFlag{Name: "record", Usage: "直播回放的`链接或ID`。"},
				},
			},
		},
	}
}
