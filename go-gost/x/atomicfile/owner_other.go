//go:build !unix

package atomicfile

import "os"

func fileOwner(os.FileInfo) (int, int) { return -1, -1 }
