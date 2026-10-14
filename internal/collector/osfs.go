package collector

import (
	"io/fs"
	"os"
)

func osDirFS(dir string) fs.FS { return os.DirFS(dir) }
