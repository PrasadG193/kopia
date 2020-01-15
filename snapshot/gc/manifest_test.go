package gc

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	assert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kopia/kopia/repo"
	"github.com/kopia/kopia/repo/blob/filesystem"
	"github.com/kopia/kopia/repo/content"
	"github.com/kopia/kopia/repo/manifest"
)

const configFileName = "kopia.config"

func TestMarkContentsDeleted(t *testing.T) {
	const contentCount = 10

	ctx := context.Background()
	check := require.New(t)
	th := createAndOpenRepo(t)

	defer th.Close(t)

	// setup: create contents
	cids := writeContents(ctx, t, th.repo.Content, contentCount)

	check.NoError(th.repo.Flush(ctx))

	// Ensure that deleted contents have a newer time stamp
	time.Sleep(time.Second)

	// delete half the contents
	snaps := nManifestIDs(t, 3)

	toDelete := cids[0:5]
	err := markContentsDeleted(ctx, th.repo, snaps, toDelete)
	check.NoError(err)

	// check: is there a GC manifest?
	gcMans, err := th.repo.Manifests.Find(ctx, markManifestLabels())
	check.NoError(err)
	check.Len(gcMans, 1, "expected a single GC mark manifest")

	var man MarkManifest
	err = th.repo.Manifests.Get(ctx, gcMans[0].ID, &man)
	check.NoError(err)

	// check: is there a content with GC mark details?
	var gcContents []content.ID

	opts := content.IterateOptions{Prefix: ContentPrefix}
	err = th.repo.Content.IterateContents(opts, func(i content.Info) error {
		gcContents = append(gcContents, i.ID)
		return nil
	})

	check.NoError(err)

	check.Len(gcContents, 1, "there must be a single GC details content")

	check.Equal(man.DetailsID, gcContents[0], "ID of the GC details content must match the mark manifest DetailsID field")

	// deserialize mark details
	b, err := th.repo.Content.GetContent(ctx, man.DetailsID)
	check.NoError(err)
	check.NotNil(b)

	var markDetails MarkDetails

	check.NoError(json.Unmarshal(b, &markDetails))

	check.Equal(snaps, markDetails.Snapshots, "markDetails.Snapshots must be the same as 'snaps'")

	check.Equal(toDelete, markDetails.MarkedContent, "MarkedContent must have the ids of the removed contents")

	// verify content not in `toDelete` was not deleted
	verifyContentDeletedState(ctx, t, th.repo.Content, cids[5:], false)

	// verify content in 'toDelete' was marked as deleted
	verifyContentDeletedState(ctx, t, th.repo.Content, toDelete, true)
}

func TestSortContentIDs(t *testing.T) {
	cids := []content.ID{"x", "c", "b", "a"}
	content.SortIDs(cids)

	for i, id := range cids[1:] {
		prev, current := string(cids[i]), string(id)
		require.LessOrEqual(t, prev, current, "content IDs not sorted")
	}
}

type testRepo struct {
	stateDir string
	repo     *repo.Repository
}

func createAndOpenRepo(t *testing.T) testRepo {
	const masterPassword = "foo"

	t.Helper()

	ctx := context.Background()
	check := require.New(t)

	stateDir, err := ioutil.TempDir("", "manifest-test")
	check.NoError(err, "cannot create temp directory")
	t.Log("repo dir:", stateDir)

	repoDir := filepath.Join(stateDir, "repo")
	check.NoError(os.MkdirAll(repoDir, 0700), "cannot create repository directory")

	storage, err := filesystem.New(context.Background(), &filesystem.Options{
		Path: repoDir,
	})
	check.NoError(err, "cannot create storage directory")

	err = repo.Initialize(ctx, storage, &repo.NewRepositoryOptions{}, masterPassword)
	check.NoError(err, "cannot create repository")

	configFile := filepath.Join(stateDir, configFileName)
	connOpts := repo.ConnectOptions{
		CachingOptions: content.CachingOptions{
			CacheDirectory: filepath.Join(stateDir, "cache"),
		},
	}
	err = repo.Connect(ctx, configFile, storage, masterPassword, connOpts)

	check.NoError(err, "unable to connect to repository")

	rep, err := repo.Open(ctx, configFile, masterPassword, &repo.Options{})
	check.NoError(err, "unable to open repository")

	return testRepo{
		stateDir: stateDir,
		repo:     rep,
	}
}

func (r *testRepo) Close(t *testing.T) {
	t.Helper()

	ctx := context.Background()

	if r.repo != nil {
		assert.NoError(t, r.repo.Close(ctx), "unable to close repository")
	}

	if r.stateDir != "" {
		configFile := filepath.Join(r.stateDir, configFileName)
		err := repo.Disconnect(configFile)

		require.NoError(t, err, "failed to disconnect repo with config file: ", configFile)
		assert.NoError(t, os.RemoveAll(r.stateDir), "unable to cleanup test state directory")
	}
}

func nManifestIDs(t *testing.T, n uint) []manifest.ID {
	ids := make([]manifest.ID, n)

	for i := range ids {
		ids[i] = manifest.ID(makeRandomHexString(t, 32))
	}

	return ids
}

func makeRandomHexString(t *testing.T, length int) string {
	t.Helper()

	b := make([]byte, (length-1)/2+1)
	_, err := rand.Read(b) // nolint:gosec

	require.NoError(t, err)

	return hex.EncodeToString(b)
}

func verifyContentDeletedState(ctx context.Context, t *testing.T, cm *content.Manager, cids []content.ID, wantDeleted bool) {
	t.Helper()

	for _, id := range cids {
		info, err := cm.ContentInfo(ctx, id)
		assert.NoError(t, err)
		assert.Equal(t, wantDeleted, info.Deleted, "content deleted state does not match")
	}
}

func writeContents(ctx context.Context, t *testing.T, cm *content.Manager, n int) []content.ID {
	t.Helper()

	b := make([]byte, 8)
	ids := make([]content.ID, 0, n)

	for i := rand.Uint64(); n > 0; n-- {
		binary.BigEndian.PutUint64(b, i)
		i++

		id, err := cm.WriteContent(ctx, b, "")
		assert.NoError(t, err, "Failed to write content")

		ids = append(ids, id)
	}

	return ids
}
