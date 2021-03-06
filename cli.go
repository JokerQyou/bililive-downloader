package main

import (
	"bililive-downloader/helper"
	"bililive-downloader/progressbar"
	"bililive-downloader/version"
	"bufio"
	"errors"
	"fmt"
	"github.com/c2h5oh/datasize"
	"github.com/rs/zerolog"
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
		logger.Info().Str("直播回放ID", param.RecordID).Send()
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

		selection := make([]int, 0)
		if selected == "all" {
			for i, _ := range param.Parts.List {
				selection = append(selection, i+1)
			}
		} else {
			for _, v := range strings.Split(selected, ",") {
				n := strings.TrimSpace(v)
				number, err := strconv.ParseInt(n, 10, 32)
				if err != nil {
					continue
				}

				selection = append(selection, int(number))
			}
		}

		if len(selection) == 0 {
			logger.Error().Str("输入的选择", selected).Msg("没有选择要下载的分段")
			return cli.Exit("没有选择要下载的分段", returnCodeError)
		}
		param.DownloadList = selection
		logger.Info().Ints("选择的分段", param.DownloadList).Send()
	}
	{
		param.NoMerge = c.Bool("no-merge")
		logger.Info().Bool("将合并为完整视频", !param.NoMerge).Send()
	}

	if !c.IsSet("limit") && interactive {
		var userInput string
		if userInput, err = ask("要对下载进行限速吗？在此输入限速值，单位是Mib/s。例如1表示限速1MiB/s，输入0表示不限速: "); err != nil {
			return cli.Exit(err, returnCodeError)
		}
		c.Set("limit", userInput)
	}
	{
		limit := c.Float64("limit")
		speedLimit := int64(limit * float64(datasize.MB))
		if speedLimit <= 0 {
			logger.Info().Msg("下载不限速")
		} else {
			param.RateLimit = datasize.ByteSize(speedLimit)
			logger.Info().Str("限速值", param.RateLimit.HumanReadable()).Uint64("每秒字节数", param.RateLimit.Bytes()).Msg("下载限速")
		}
	}

	// Setup progress bar manager only if we're connected to a TTY
	var progressWriter io.Writer = os.Stdout
	if !helper.IsTTY() {
		progressWriter = ioutil.Discard
		logger.Debug().Msg("不在终端中运行，将不显示进度条")
	}
	progressbar.Init(progressWriter)

	return cliDownload(param)
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

// wrapAction wraps given action. It takes care of `--debug` option, to setup proper logging level.
func wrapAction(actionFunc cli.ActionFunc) cli.ActionFunc {
	return func(c *cli.Context) error {
		if c.Bool("debug") {
			logger = logger.Level(zerolog.DebugLevel)
			logger.Debug().Msg("开启DEBUG级别日志，进度条可能被打乱")
		}
		return actionFunc(c)
	}
}

// newCliApp creates a cli.App instance to handle commandline execution.
func newCliApp() *cli.App {
	cli.VersionPrinter = func(c *cli.Context) {
		_, _ = fmt.Fprintf(c.App.Writer, "%v 版本 %v, 编译时间 %v\n", c.App.Name, c.App.Version, c.App.Compiled)
	}

	return &cli.App{
		Version:  version.VersionString,
		Compiled: version.CompiledTime,
		Name:     "bililive-downloader",
		Usage:    "Download livestream recordings from Bilibili",
		Flags:    []cli.Flag{&cli.BoolFlag{Name: "debug", Usage: "开启DEBUG级别日志", Value: false}},
		Commands: []*cli.Command{
			{
				Name:    "version",
				Aliases: []string{"v"},
				Usage:   "显示版本信息",
				Action: func(c *cli.Context) error {
					cli.ShowVersion(c)
					return nil
				},
			},
			{
				Name:    "download",
				Aliases: []string{"d"},
				Usage:   "下载直播回放",
				Action:  wrapAction(handleDownloadAction),
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "interactive", Aliases: []string{"i"}, Usage: "交互式询问各个未传递的参数。", Value: false},
					&cli.UintFlag{Name: "concurrency", Usage: "设定`并发数`（可以同时下载几个分段）。如果您的网络较好，可适当调高。"},
					&cli.StringFlag{Name: "select", Usage: "指定要下载的`分段编号`，以逗号分隔。"},
					&cli.BoolFlag{Name: "no-merge", Usage: "不合并各个视频分段。如果不指定此选项，并下载所有分段，则会合并为单个视频文件。", Value: false},
					&cli.StringFlag{Name: "record", Usage: "直播回放的`链接或ID`。"},
					&cli.Float64Flag{Name: "limit", Usage: "`下载限速值`，单位为MiB/s。例如1表示限速1MiB/s，0表示不限速。"},
				},
			},
		},
	}
}
