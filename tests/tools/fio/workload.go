package fio

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// WriteFiles writes files to the directory specified by path, up to the
// provided size and number of files
func (fr *Runner) WriteFiles(path string, sizeB int64, numFiles int) error {
	fullPath := filepath.Join(fr.DataDir, path)

	err := os.MkdirAll(fullPath, 0777)
	if err != nil {
		return err
	}

	_, _, err = fr.RunConfigs(Config{
		{
			Name: fmt.Sprintf("write-%vB-%v", sizeB, numFiles),
			Options: map[string]string{
				"size":      strconv.Itoa(int(sizeB)),
				"nrfiles":   strconv.Itoa(numFiles),
				"directory": fullPath,
			},
		},
	})

	return err
}
