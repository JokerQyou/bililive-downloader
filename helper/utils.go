package helper

import "os"

func IsTTY() bool {
	info, _ := os.Stdout.Stat()
	return info.Mode()&os.ModeCharDevice != 0
}
