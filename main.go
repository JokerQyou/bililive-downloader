package main

import (
	"bililive-downloader/ffmpeg"
	"bililive-downloader/helper"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"os"
	"os/exec"
)

const timeFormat = "2006-01-02 15:04:05"

var logger zerolog.Logger

func main() {
	logger = log.Output(zerolog.ConsoleWriter{
		NoColor:    !helper.IsTTY(),
		Out:        os.Stdout,
		TimeFormat: timeFormat,
	}).Level(zerolog.InfoLevel)

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

	newCliApp().Run(os.Args)
}
