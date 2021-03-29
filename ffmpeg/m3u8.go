package ffmpeg

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateM3U8Playlist generates a M3U8 playlist file for given input files.
// All input files must be of TS media type.
// This function requires ffmpeg toolset (same as rest of this package), as it uses the `ffprobe` tool.
// Existing playlist file will be deleted.
func GenerateM3U8Playlist(inputFiles []string, outputFile string) error {
	_, err := os.Stat(outputFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := os.Remove(outputFile); err != nil {
			return err
		}
	}

	if len(inputFiles) == 0 {
		return fmt.Errorf("no input file given")
	}

	runner, err := NewRunner()
	if err != nil {
		return err
	}

	baseDir := filepath.Dir(outputFile)
	// Build playlist entries
	var refMode *os.FileMode
	var playlistHeader strings.Builder
	playlistHeader.WriteString("#EXTM3U\n")
	playlistHeader.WriteString("#EXT-X-VERSION:7\n")

	var playlistItems strings.Builder
	var maxDuration time.Duration

	for _, filePath := range inputFiles {
		// Ensure input file existence
		info, err := os.Stat(filePath)
		if err != nil {
			return err
		}

		// Use first TS media file as permission reference.
		if refMode == nil {
			mode := info.Mode()
			refMode = &mode
		}

		// Probe media duration
		length, err := runner.ProbSingleMediaDuration(filePath)
		if err != nil {
			return err
		}

		playlistItems.WriteString(fmt.Sprintf("#EXTINF:%f,\n", length.Seconds()))
		relFilePath, err := filepath.Rel(baseDir, filePath)
		if err != nil {
			return err
		}
		playlistItems.WriteString(fmt.Sprintf("%s\n\n", relFilePath))

		if length > maxDuration {
			maxDuration = length
		}
	}
	playlistItems.WriteString("#EXT-X-ENDLIST\n")

	if refMode == nil {
		return fmt.Errorf("failed to detect file mode")
	}

	playlistHeader.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int64(maxDuration.Round(time.Second).Seconds()+1)))
	playlistHeader.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n\n")
	return ioutil.WriteFile(outputFile, []byte(playlistHeader.String()+playlistItems.String()), *refMode)
}
