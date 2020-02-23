package s3

import (
	"context"
	"crypto/rand"
	//"crypto/sha1"
	"fmt"
	"log"
	"net"
	//"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sts"
	minio "github.com/minio/minio-go/v6"
	"github.com/minio/minio/pkg/madmin"

	"github.com/kopia/kopia/internal/blobtesting"
	"github.com/kopia/kopia/internal/retry"
	"github.com/kopia/kopia/repo/blob"
)

// https://github.com/minio/minio-go
const (
	endpoint        = "play.minio.io:9000"
	host            = "play.minio.io"
	accessKeyID     = "Q3AM3UQ867SPQQA43P2F"                     //nolint:gosec
	secretAccessKey = "zuf+tfteSlswRu7BJ86wekitnifILbZam1KYY3TG" //nolint:gosec
	useSSL          = true

	// the test takes a few seconds, delete stuff older than 1h to avoid accumulating cruft
	cleanupAge = 1 * time.Hour
)

var (
	kopiaUserName   = generateName("kopiauser")
	kopiaUserPasswd = generateName("kopiapassword")
)

// var bucketName = "kopia-test-09a37beed6c32d0f"

func generateName(name string) string {
	b := make([]byte, 8)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("%s-1", name)

	}
	return fmt.Sprintf("%s-%x", name, b)
}

func endpointReachable() bool {
	conn, err := net.DialTimeout("tcp4", endpoint, 5*time.Second)
	if err == nil {
		conn.Close()
		return true
	}

	return false
}

func TestS3Storage(t *testing.T) {
	// recreate per-host bucket, which sometimes get cleaned up by play.minio.io
	bucketName := generateName("kopia-test")
	createBucket(t, bucketName)
	testStorage(t, bucketName, accessKeyID, secretAccessKey, "")
	deleteBucket(t, bucketName)
}

func TestS3StorageWithSessionToken(t *testing.T) {
	// create test bucket
	bucketName := generateName("kopia-test")
	createBucket(t, bucketName)
	// create kopia user and session token
	createUser(t)
	kopiaAccessKeyID, kopiaSecretKey, kopiaSessionToken := createTemporaryCreds(t, bucketName)
	testStorage(t, bucketName, kopiaAccessKeyID, kopiaSecretKey, kopiaSessionToken)
	deleteBucket(t, bucketName)
}

func testStorage(t *testing.T, bucketName, accessID, secretKey, sessionToken string) {
	if !endpointReachable() {
		t.Skip("endpoint not reachable")
	}

	ctx := context.Background()

	cleanupOldData(ctx, t, bucketName)

	data := make([]byte, 8)
	rand.Read(data) //nolint:errcheck

	attempt := func() (interface{}, error) {
		return New(context.Background(), &Options{
			AccessKeyID:     accessID,
			SecretAccessKey: secretKey,
			SessionToken:    sessionToken,
			Endpoint:        endpoint,
			BucketName:      bucketName,
			Prefix:          fmt.Sprintf("test-%v-%x-", time.Now().Unix(), data),
		})
	}

	v, err := retry.WithExponentialBackoff("New() S3 storage", attempt, func(err error) bool { return err != nil })
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	st := v.(blob.Storage)
	blobtesting.VerifyStorage(ctx, t, st)
	blobtesting.AssertConnectionInfoRoundTrips(ctx, t, st)

	if err := st.Close(ctx); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func createBucket(t *testing.T, bucketName string) {
	fmt.Printf("create bucket %s\n", bucketName)
	minioClient, err := minio.New(endpoint, accessKeyID, secretAccessKey, useSSL)
	if err != nil {
		t.Fatalf("can't initialize minio client: %v", err)
	}
	// ignore error
	_ = minioClient.MakeBucket(bucketName, "us-east-1")
}

func deleteBucket(t *testing.T, bucketName string) {
	fmt.Printf("delete bucket %s\n", bucketName)
	minioClient, err := minio.New(endpoint, accessKeyID, secretAccessKey, useSSL)
	if err != nil {
		t.Fatalf("can't initialize minio client: %v", err)
	}

	// delete all objects
	doneCh := make(chan struct{})
	defer close(doneCh)
	// Recurively list all objects in 'mytestbucket'
	for obj := range minioClient.ListObjects(bucketName, "", true, doneCh) {
		err := minioClient.RemoveObject(bucketName, obj.Key)
		if err != nil {
			t.Fatalf("can't object in a bucket: %v", err)
		}
	}

	err = minioClient.RemoveBucket(bucketName)
	if err != nil {
		t.Fatalf("can't delete minio bucket: %v", err)
	}
}

func createUser(t *testing.T) {
	// create minio admin client
	adminCli, err := madmin.New(endpoint, accessKeyID, secretAccessKey, useSSL)
	if err != nil {
		t.Fatalf("can't initialize minio admin client: %v", err)
	}

	// add new kopia user
	if err = adminCli.AddUser(kopiaUserName, kopiaUserPasswd); err != nil {
		t.Fatalf("failed to add new minio user: %v", err)
	}

	// set user policy
	if err = adminCli.SetPolicy("readwrite", kopiaUserName, false); err != nil {
		t.Fatalf("failed to set user policy: %v", err)
	}
	fmt.Printf("create user %s\n", kopiaUserName)
}

func deleteUser(t *testing.T) {
	// create minio admin client
	adminCli, err := madmin.New(endpoint, accessKeyID, secretAccessKey, useSSL)
	if err != nil {
		t.Fatalf("can't initialize minio admin client: %v", err)
	}

	// delete temp kopia user
	if err = adminCli.RemoveUser(kopiaUserName); err != nil {
		t.Fatalf("failed to remove new minio user: %v", err)
	}
	fmt.Printf("deleted user\n")
}

func createTemporaryCreds(t *testing.T, bucketName string) (accessID, secretKey, sessionToken string) {
	// Configure to use MinIO Server
	awsConfig := &aws.Config{
		Credentials:      credentials.NewStaticCredentials(kopiaUserName, kopiaUserPasswd, ""),
		Endpoint:         aws.String(host),
		Region:           aws.String("us-east-1"),
		S3ForcePathStyle: aws.Bool(true),
	}

	awsSession, err := session.NewSession(awsConfig)
	if err != nil {
		t.Fatalf("failed to create aws session: %v", err)
	}

	svc := sts.New(awsSession)

	input := &sts.AssumeRoleInput{
		// give access to only S3 bucket with name bucketName
		Policy: aws.String(fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"Stmt1","Effect":"Allow","Action":"s3:*","Resource":"arn:aws:s3:::%s/*"}]}`, bucketName)),
		// RoleArn and RoleSessionName are not meaningful for MinIO and can be set to any value
		RoleArn:         aws.String("arn:xxx:xxx:xxx:xxxx"),
		RoleSessionName: aws.String("kopiaTestSession"),
		DurationSeconds: aws.Int64(900), // in seconds
	}

	result, err := svc.AssumeRole(input)
	if err != nil {
		t.Fatalf("failed to create session with aws assume role: %v", err)
	}

	if result.Credentials == nil {
		t.Fatalf("couldn't find aws creds in aws assume role response")
	}

	log.Printf("created session token with assume role: expiration: %s", result.Credentials.Expiration)

	return *result.Credentials.AccessKeyId, *result.Credentials.SecretAccessKey, *result.Credentials.SessionToken
}

func cleanupOldData(ctx context.Context, t *testing.T, bucketName string) {
	// cleanup old data from the bucket
	st, err := New(context.Background(), &Options{
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		Endpoint:        endpoint,
		BucketName:      bucketName,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}

	_ = st.ListBlobs(ctx, "", func(it blob.Metadata) error {
		age := time.Since(it.Timestamp)
		if age > cleanupAge {
			if err := st.DeleteBlob(ctx, it.BlobID); err != nil {
				t.Errorf("warning: unable to delete %q: %v", it.BlobID, err)
			}
		} else {
			log.Printf("keeping %v", it.BlobID)
		}
		return nil
	})
}
