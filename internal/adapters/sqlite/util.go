package sqlite

import (
	"os"
	"time"
)

func osStatFile(path string) (os.FileInfo, error) { return os.Stat(path) }

func nowFileTimestamp() string { return time.Now().UTC().Format("20060102T150405.000000000Z") }

func osMkdirAll(path string) error { return os.MkdirAll(path, 0o700) }
