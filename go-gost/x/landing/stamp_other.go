//go:build !linux

package landing

import "os"

// fileStamp identifies one version of a file (landing runs on Linux only;
// this keeps the package buildable elsewhere).
type fileStamp struct{ size, mtime int64 }

func stampOf(info os.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
}
