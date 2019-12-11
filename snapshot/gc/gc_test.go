package gc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/kopia/kopia/repo/content"
)

func Test_deleteUnused(t *testing.T) {
	tests := []struct {
		name         string
		snapCount    uint
		contentCount int
		deleteCount  int
		batchSize    int
	}{
		{
			name:         "with no snap ids",
			snapCount:    0,
			contentCount: 4,
			deleteCount:  3,
			batchSize:    5,
		},

		{
			name:         "with single snap id",
			snapCount:    1,
			contentCount: 4,
			deleteCount:  3,
			batchSize:    5,
		},

		{
			name:         "0 contents to delete",
			snapCount:    3,
			contentCount: 4,
			deleteCount:  0,
			batchSize:    5,
		},

		{
			name:         "delete some of the content",
			snapCount:    3,
			contentCount: 8,
			deleteCount:  6,
			batchSize:    10,
		},

		{
			name:         "delete all the content",
			snapCount:    3,
			contentCount: 9,
			deleteCount:  9,
			batchSize:    10,
		},

		{
			name:         "delete count same as batch size",
			snapCount:    3,
			contentCount: 12,
			deleteCount:  10,
			batchSize:    10,
		},

		{
			name:         "delete count larger than batch size",
			snapCount:    3,
			contentCount: 21,
			deleteCount:  19,
			batchSize:    10,
		},

		{
			name:         "delete multiple batches",
			snapCount:    3,
			contentCount: 155,
			deleteCount:  147,
			batchSize:    50,
		},
	}

	ctx := context.Background()

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			check := assert.New(t)
			r := createAndOpenRepo(t)
			defer r.Close(t)

			cids := writeContents(ctx, t, r.repo.Content, tt.contentCount)
			snaps := nManifestIDs(t, tt.snapCount)
			toDeleteCh := make(chan content.ID)

			go func() {
				defer close(toDeleteCh)
				for _, id := range cids[:tt.deleteCount] {
					toDeleteCh <- id
				}
			}()

			check.NoError(r.repo.Flush(ctx))
			// Ensure that deleted contents have a newer time stamp
			time.Sleep(time.Second)

			err := deleteUnused(ctx, r.repo, snaps, toDeleteCh, tt.batchSize)
			check.NoError(err, "unexpected error")

			// verify that all the contents sent through the channel were
			// deleted and nothing else
			verifyContentDeletedState(ctx, t, r.repo.Content, cids[:tt.deleteCount], true)
			verifyContentDeletedState(ctx, t, r.repo.Content, cids[tt.deleteCount:], false)

			// check: are there GC manifests?
			gcMans, err := r.repo.Manifests.Find(ctx, markManifestLabels())
			check.NoError(err)
			check.Len(gcMans, (tt.deleteCount+tt.batchSize-1)/tt.batchSize, "expected a single GC mark manifest")

			var foundGCContentCount int

			opts := content.IterateOptions{Prefix: ContentPrefix}
			err = r.repo.Content.IterateContents(opts, func(i content.Info) error {
				foundGCContentCount++
				return nil
			})

			check.NoError(err)

			check.Equal(len(gcMans), foundGCContentCount, "GC details content count does not match number of GC mark manifests")
		})
	}
}
