package socket

import (
	"io"
	"sync"

	"github.com/go-gost/x/atomicfile"
	"github.com/go-gost/x/config"
)

// configMutex 保护配置文件的并发写入
var configMutex sync.Mutex

func saveConfig() error {
	configMutex.Lock()
	defer configMutex.Unlock()

	// Atomic replace: a failed write (e.g. a full disk) keeps the previous gost.json instead
	// of leaving a truncated file the next agent start would load as an empty config.
	return atomicfile.Write("gost.json", 0600, func(w io.Writer) error {
		return config.Global().Write(w, "json")
	})
}
