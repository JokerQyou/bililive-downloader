package helper

import "os"

func IsTTY() bool {
	info, _ := os.Stdout.Stat()
	return info.Mode()&os.ModeCharDevice != 0
}

// ContainsInt performs simple `contain` operation on int slice.
func ContainsInt(ints []int, i int) bool {
	for _, v := range ints {
		if i == v {
			return true
		}
	}
	return false
}
