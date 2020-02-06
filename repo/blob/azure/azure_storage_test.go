package azure_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/kopia/kopia/internal/blobtesting"

	"github.com/kopia/kopia/repo/blob"
	"github.com/kopia/kopia/repo/blob/azure"
)

const (
	testContainerEnv   = "KOPIA_AZURE_TEST_CONTAINER"
	testAccountNameEnv = "KOPIA_AZURE_TEST_ACCOUNT_NAME"
	testAccountKeyEnv  = "KOPIA_AZURE_TEST_ACCOUNT_KEY"
)

func getEnvOrSkip(t *testing.T, name string) string {
	value, ok := os.LookupEnv(name)
	if !ok {
		t.Skip(fmt.Sprintf("%s not provided", name))
	}

	return value
}

func TestAzureStorage(t *testing.T) {
	container := getEnvOrSkip(t, testContainerEnv)
	accountName := getEnvOrSkip(t, testAccountNameEnv)
	accountKey := getEnvOrSkip(t, testAccountKeyEnv)

	data := make([]byte, 8)
	rand.Read(data) //nolint:errcheck

	ctx := context.Background()
	st, err := azure.New(ctx, &azure.Options{
		Container:   container,
		AccountName: accountName,
		AccountKey:  accountKey,
		Prefix:      fmt.Sprintf("test-%v-%x-", time.Now().Unix(), data),
	})

	if err != nil {
		t.Fatalf("unable to connect to Azure: %v", err)
	}

	if err := st.ListBlobs(ctx, "", func(bm blob.Metadata) error {
		return st.DeleteBlob(ctx, bm.BlobID)
	}); err != nil {
		t.Fatalf("unable to clear Azure blob container: %v", err)
	}

	blobtesting.VerifyStorage(ctx, t, st)
	blobtesting.AssertConnectionInfoRoundTrips(ctx, t, st)

	// delete everything again
	if err := st.ListBlobs(ctx, "", func(bm blob.Metadata) error {
		return st.DeleteBlob(ctx, bm.BlobID)
	}); err != nil {
		t.Fatalf("unable to clear Azure blob container: %v", err)
	}

	if err := st.Close(ctx); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestAzureStorageInvalid(t *testing.T) {
	container := getEnvOrSkip(t, testContainerEnv)
	accountName := getEnvOrSkip(t, testAccountNameEnv)
	accountKey := getEnvOrSkip(t, testAccountKeyEnv)

	ctx := context.Background()
	st, err := azure.New(ctx, &azure.Options{
		Container:   container + "-invalid",
		AccountName: accountName,
		AccountKey:  accountKey,
	})

	if err != nil {
		t.Fatalf("unable to connect to Azure container: %v", err)
	}

	defer st.Close(ctx)

	if err := st.PutBlob(ctx, "xxx", []byte{1, 2, 3}); err == nil {
		t.Errorf("unexpected success when adding to non-existent container")
	}
}
