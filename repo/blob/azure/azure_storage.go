// Package azure implements Azure Blob Storage.
package azure

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/efarrer/iothrottler"
	"github.com/pkg/errors"
	gblob "gocloud.dev/blob"
	"gocloud.dev/blob/azureblob"
	"gocloud.dev/gcerrors"

	"github.com/kopia/kopia/internal/retry"
	"github.com/kopia/kopia/repo/blob"
)

const (
	azStorageType = "azureBlob"
)

type azStorage struct {
	Options

	ctx context.Context

	bucket *gblob.Bucket

	downloadThrottler *iothrottler.IOThrottlerPool
	uploadThrottler   *iothrottler.IOThrottlerPool
}

func (az *azStorage) GetBlob(ctx context.Context, b blob.ID, offset, length int64) ([]byte, error) {
	if offset < 0 {
		return nil, errors.Errorf("invalid offset")
	}
	attempt := func() (interface{}, error) {
		reader, err := az.bucket.NewRangeReader(ctx, az.getObjectNameString(b), offset, length, nil)
		if err != nil {
			return nil, err
		}

		defer reader.Close() //nolint:errcheck

		throttled, err := az.downloadThrottler.AddReader(reader)
		if err != nil {
			return nil, err
		}

		return ioutil.ReadAll(throttled)
	}

	v, err := exponentialBackoff(fmt.Sprintf("GetBlob(%q,%v,%v)", b, offset, length), attempt)
	if err != nil {
		return nil, translateError(err)
	}

	fetched := v.([]byte)
	if len(fetched) != int(length) && length >= 0 {
		return nil, errors.Errorf("invalid offset/length")
	}
	return fetched, nil
}

func exponentialBackoff(desc string, att retry.AttemptFunc) (interface{}, error) {
	return retry.WithExponentialBackoff(desc, att, isRetriableError)
}

func isRetriableError(err error) bool {
	if me, ok := err.(azblob.ResponseError); ok {
		if me.Response() == nil {
			return true
		}
		// retry on server errors, not on client errors
		return me.Response().StatusCode >= 500
	}

	switch gcerrors.Code(err) {
	case gcerrors.OK:
		return false
	case gcerrors.NotFound:
		return false
	case gcerrors.Unknown:
		return false
	case gcerrors.InvalidArgument:
		return false
	case gcerrors.Unimplemented:
		return false
	case gcerrors.PermissionDenied:
		return false
	default:
		return false
	}
}

func translateError(err error) error {
	switch gcerrors.Code(err) {
	case gcerrors.OK:
		return nil
	case gcerrors.NotFound:
		return blob.ErrBlobNotFound
	default:
		return err
	}
}

func (az *azStorage) PutBlob(ctx context.Context, b blob.ID, data []byte) error {
	ctx, cancel := context.WithCancel(ctx)
	throttled, err := az.uploadThrottler.AddReader(ioutil.NopCloser(bytes.NewReader(data)))
	if err != nil {
		return err
	}

	// Create azure Bucket writer
	writer, err := az.bucket.NewWriter(ctx, az.getObjectNameString(b), &gblob.WriterOptions{ContentType: "application/x-kopia"})
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, throttled)
	if err != nil {
		// cancel context before closing the writer causes it to abandon the upload.
		cancel()

		_ = writer.Close() // failing already, ignore the error

		return translateError(err)
	}
	defer cancel()

	// calling close before cancel() causes it to commit the upload.
	return translateError(writer.Close())
}

// DeleteBlob deletes azure blob from container with given ID
func (az *azStorage) DeleteBlob(ctx context.Context, b blob.ID) error {
	attempt := func() (interface{}, error) {
		return nil, az.bucket.Delete(ctx, az.getObjectNameString(b))
	}
	_, err := exponentialBackoff(fmt.Sprintf("DeleteBlob(%q)", b), attempt)
	err = translateError(err)

	// Don't return error if blob is already deleted
	if err == blob.ErrBlobNotFound {
		return nil
	}

	return err
}

func (az *azStorage) getObjectNameString(b blob.ID) string {
	return az.Prefix + string(b)
}

// ListBlobs list azure blobs with given prefix
func (az *azStorage) ListBlobs(ctx context.Context, prefix blob.ID, callback func(blob.Metadata) error) error {
	li := az.bucket.List(&gblob.ListOptions{Prefix: az.getObjectNameString(prefix)})
	for {
		lo, err := li.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		bm := blob.Metadata{
			BlobID:    blob.ID(lo.Key[len(az.Prefix):]),
			Length:    lo.Size,
			Timestamp: lo.ModTime,
		}

		if err := callback(bm); err != nil {
			return err
		}
	}
	return nil
}

func (az *azStorage) ConnectionInfo() blob.ConnectionInfo {
	return blob.ConnectionInfo{
		Type:   azStorageType,
		Config: &az.Options,
	}
}

func (az *azStorage) Close(ctx context.Context) error {
	return az.bucket.Close()
}

func toBandwidth(bytesPerSecond int) iothrottler.Bandwidth {
	if bytesPerSecond <= 0 {
		return iothrottler.Unlimited
	}

	return iothrottler.Bandwidth(bytesPerSecond) * iothrottler.BytesPerSecond
}

func New(ctx context.Context, opt *Options) (blob.Storage, error) {
	if opt.Container == "" {
		return nil, errors.New("container name must be specified")
	}

	// Create a credentials object.
	credential, err := azureblob.NewCredential(azureblob.AccountName(opt.AccountName), azureblob.AccountKey(opt.AccountKey))
	if err != nil {
		return nil, err
	}

	// Create a Pipeline with credentials.
	pipeline := azureblob.NewPipeline(credential, azblob.PipelineOptions{})

	// Create a *blob.Bucket.
	bucket, err := azureblob.OpenBucket(ctx, pipeline, azureblob.AccountName(opt.AccountName), opt.Container, &azureblob.Options{Credential: credential})
	if err != nil {
		return nil, err
	}

	downloadThrottler := iothrottler.NewIOThrottlerPool(toBandwidth(opt.MaxDownloadSpeedBytesPerSecond))
	uploadThrottler := iothrottler.NewIOThrottlerPool(toBandwidth(opt.MaxUploadSpeedBytesPerSecond))

	return &azStorage{
		Options:           *opt,
		ctx:               ctx,
		bucket:            bucket,
		downloadThrottler: downloadThrottler,
		uploadThrottler:   uploadThrottler,
	}, nil
}

func init() {
	blob.AddSupportedStorage(
		azStorageType,
		func() interface{} {
			return &Options{}
		},
		func(ctx context.Context, o interface{}) (blob.Storage, error) {
			return New(ctx, o.(*Options))
		})
}
