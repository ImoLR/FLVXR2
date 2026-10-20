//go:build linux

package landing

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Minimum headroom for a first Core install: the 33 MiB binary plus later
// geo data, and enough memory to start the Core next to live forwarding.
const (
	installMinDiskBytes = 150 << 20
	installMinMemBytes  = 128 << 20
)

// checkInstallResources refuses a first Core install when the landing
// directory's filesystem or the machine's available memory is too low.
func checkInstallResources(dir string) error {
	path := dir
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err == nil {
		free := uint64(fs.Bavail) * uint64(fs.Bsize)
		if free < installMinDiskBytes {
			return fmt.Errorf("落地程序安装所需磁盘空间不足：剩余 %d MiB，至少需要 %d MiB", free>>20, installMinDiskBytes>>20)
		}
	}
	if avail, ok := memAvailable(); ok && avail < installMinMemBytes {
		return fmt.Errorf("落地程序安装所需内存不足：可用 %d MiB，至少需要 %d MiB", avail>>20, installMinMemBytes>>20)
	}
	return nil
}

func memAvailable() (uint64, bool) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, false
			}
			return kb << 10, true
		}
	}
	return 0, false
}
