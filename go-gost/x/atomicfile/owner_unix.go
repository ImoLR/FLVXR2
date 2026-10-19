//go:build unix

package atomicfile

import (
	"os"
	"syscall"
)

func fileOwner(info os.FileInfo) (int, int) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid), int(st.Gid)
	}
	return -1, -1
}
