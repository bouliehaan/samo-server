package scanner

import (
	"os"
	"strconv"
	"syscall"
)

func fileInode(path string) string {
	stat, err := os.Stat(path)
	if err != nil {
		return ""
	}
	sys, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(sys.Ino), 10)
}
