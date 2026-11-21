//go:build !windows

package memory

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
