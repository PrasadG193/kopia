package content

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"golang.org/x/exp/mmap"

	"github.com/kopia/kopia/repo/blob"
)

const (
	simpleIndexSuffix                      = ".sndx"
	unusedCommittedContentIndexCleanupTime = 1 * time.Hour // delete unused committed index blobs after 1 hour
)

type diskCommittedContentIndexCache struct {
	dirname string
}

func (c *diskCommittedContentIndexCache) indexBlobPath(indexBlobID blob.ID) string {
	return filepath.Join(c.dirname, string(indexBlobID)+simpleIndexSuffix)
}

func (c *diskCommittedContentIndexCache) openIndex(indexBlobID blob.ID) (packIndex, error) {
	fullpath := c.indexBlobPath(indexBlobID)

	f, err := mmap.Open(fullpath)
	if err != nil {
		return nil, err
	}

	return openPackIndex(f)
}

func (c *diskCommittedContentIndexCache) hasIndexBlobID(indexBlobID blob.ID) (bool, error) {
	_, err := os.Stat(c.indexBlobPath(indexBlobID))
	if err == nil {
		return true, nil
	}

	if os.IsNotExist(err) {
		return false, nil
	}

	return false, err
}

func (c *diskCommittedContentIndexCache) addContentToCache(indexBlobID blob.ID, data []byte) error {
	exists, err := c.hasIndexBlobID(indexBlobID)
	if err != nil {
		return err
	}

	if exists {
		return nil
	}

	tmpFile, err := writeTempFileAtomic(c.dirname, data)
	if err != nil {
		return err
	}

	// rename() is atomic, so one process will succeed, but the other will fail
	if err := os.Rename(tmpFile, c.indexBlobPath(indexBlobID)); err != nil {
		// verify that the content exists
		exists, err := c.hasIndexBlobID(indexBlobID)
		if err != nil {
			return err
		}

		if !exists {
			return errors.Errorf("unsuccessful index write of content %q", indexBlobID)
		}
	}

	return nil
}

func writeTempFileAtomic(dirname string, data []byte) (string, error) {
	// write to a temp file to avoid race where two processes are writing at the same time.
	tf, err := ioutil.TempFile(dirname, "tmp")
	if err != nil {
		if os.IsNotExist(err) {
			os.MkdirAll(dirname, 0700) //nolint:errcheck
			tf, err = ioutil.TempFile(dirname, "tmp")
		}
	}

	if err != nil {
		return "", errors.Wrap(err, "can't create tmp file")
	}

	if _, err := tf.Write(data); err != nil {
		return "", errors.Wrap(err, "can't write to temp file")
	}

	if err := tf.Close(); err != nil {
		return "", errors.Errorf("can't close tmp file")
	}

	return tf.Name(), nil
}

func (c *diskCommittedContentIndexCache) expireUnused(used []blob.ID) error {
	entries, err := ioutil.ReadDir(c.dirname)
	if err != nil {
		return errors.Wrap(err, "can't list cache")
	}

	remaining := map[blob.ID]os.FileInfo{}

	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), simpleIndexSuffix) {
			n := strings.TrimSuffix(ent.Name(), simpleIndexSuffix)
			remaining[blob.ID(n)] = ent
		}
	}

	for _, u := range used {
		delete(remaining, u)
	}

	for _, rem := range remaining {
		if time.Since(rem.ModTime()) > unusedCommittedContentIndexCleanupTime {
			log.Debugf("removing unused %v %v", rem.Name(), rem.ModTime())

			if err := os.Remove(filepath.Join(c.dirname, rem.Name())); err != nil {
				log.Warningf("unable to remove unused index file: %v", err)
			}
		} else {
			log.Debugf("keeping unused %v because it's too new %v", rem.Name(), rem.ModTime())
		}
	}

	return nil
}
