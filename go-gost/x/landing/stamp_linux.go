//go:build linux

package landing

import (
	"os"
	"syscall"
)

// fileStamp identifies one version of a file. ctime cannot be set from user
// space, so a same-size rewrite with a restored mtime is still noticed.
type fileStamp struct {
	size, mtime, ctime int64
	dev, ino           uint64
}

func stampOf(info os.FileInfo) fileStamp {
	s := fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.ctime = st.Ctim.Sec*1e9 + st.Ctim.Nsec
		s.dev, s.ino = uint64(st.Dev), st.Ino
	}
	return s
}
