//go:build integration

// FILES-V2 S3 integration: the SAME backend-conformance suite that gates the
// local driver, run against a REAL S3-compatible server (MinIO in Docker) in
// both serve modes, plus the Store-level round trip (upload validation, dedup,
// signed URLs) over the S3 backend. Passing here is what makes
// "interchangeable backends" a verified claim, not a design intention.
//
// Run: go test -tags integration -race -run TestS3 ./pkg/files/
package files

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	minioUser   = "minioadmin"
	minioPass   = "minioadmin"
	minioBucket = "appximo-test"
)

// startMinIO launches a MinIO container and returns its S3 endpoint.
func startMinIO(t *testing.T, ctx context.Context) string {
	t.Helper()
	req := testcontainers.ContainerRequest{
		Image:        "minio/minio:latest",
		Cmd:          []string{"server", "/data"},
		Env:          map[string]string{"MINIO_ROOT_USER": minioUser, "MINIO_ROOT_PASSWORD": minioPass},
		ExposedPorts: []string{"9000/tcp"},
		WaitingFor:   wait.ForHTTP("/minio/health/live").WithPort("9000/tcp").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req, Started: true,
	})
	if err != nil {
		t.Fatalf("start minio: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("minio host: %v", err)
	}
	port, err := c.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatalf("minio port: %v", err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())

	// Create the bucket with the raw SDK (bucket lifecycle is not a blob-API op).
	client := s3.New(s3.Options{
		Region:       "us-east-1",
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(minioUser, minioPass, "")),
	})
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(minioBucket)}); err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	return endpoint
}

func s3TestConfig(endpoint string, mode S3ServeMode) S3Config {
	return S3Config{
		Bucket:         minioBucket,
		Endpoint:       endpoint,
		Region:         "us-east-1",
		AccessKey:      minioUser,
		SecretKey:      minioPass,
		ForcePathStyle: true, // MinIO requires path-style addressing
		ServeMode:      mode,
	}
}

func TestS3Backend_Conformance(t *testing.T) {
	ctx := context.Background()
	endpoint := startMinIO(t, ctx)

	for _, mode := range []S3ServeMode{S3ServeRedirect, S3ServeProxy} {
		t.Run(string(mode), func(t *testing.T) {
			b, err := NewS3Backend(ctx, s3TestConfig(endpoint, mode))
			if err != nil {
				t.Fatalf("NewS3Backend: %v", err)
			}
			defer b.Close() //nolint:errcheck
			backendConformance(t, ctx, b)
		})
	}
}

// TestS3Store_RoundTrip proves the Store logic (validation, hashing, dedup,
// tenancy, signed URLs) is byte-identical over the S3 driver — the suite the
// local Store already passes.
func TestS3Store_RoundTrip(t *testing.T) {
	ctx := context.Background()
	endpoint := startMinIO(t, ctx)
	b, err := NewS3Backend(ctx, s3TestConfig(endpoint, S3ServeRedirect))
	if err != nil {
		t.Fatalf("NewS3Backend: %v", err)
	}
	defer b.Close() //nolint:errcheck
	store := NewStore(b, newMemStore())

	content := []byte("s3 round trip payload")
	m, err := store.Put(ctx, "acme", bytes.NewReader(content), PutMeta{ContentType: "text/plain", OriginalName: "s3.txt"})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// Round trip.
	rc, gm, err := store.Get(ctx, "acme", m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close() //nolint:errcheck
	if !bytes.Equal(got, content) || gm.OriginalName != "s3.txt" {
		t.Fatalf("round trip mismatch: %+v", gm)
	}

	// Dedup: same content again → same sha, distinct id, ONE object in the bucket.
	m2, err := store.Put(ctx, "acme", bytes.NewReader(content), PutMeta{OriginalName: "copy.txt"})
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if m2.SHA256 != m.SHA256 || m2.ID == m.ID {
		t.Fatalf("dedup identity: %s/%s vs %s/%s", m.SHA256, m.ID, m2.SHA256, m2.ID)
	}
	objs, err := b.List(ctx, "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("bucket objects = %d, want 1 (dedup)", len(objs))
	}

	// Tenant isolation: the id is meaningless under another tenant.
	if _, _, err := store.Get(ctx, "otherco", m.ID); err == nil {
		t.Fatal("cross-tenant Get must fail")
	}

	// Native presigned URL (Signature v4, short-lived).
	u, err := store.SignedURL(ctx, "acme", m.ID, 90*time.Second)
	if err != nil {
		t.Fatalf("SignedURL: %v", err)
	}
	if !strings.Contains(u, "X-Amz-Signature") {
		t.Fatalf("expected a presigned URL, got %q", u)
	}

	// Upload validation applies identically on S3 (spoofed jpg → rejected).
	if _, err := store.Put(ctx, "acme", strings.NewReader("<?php system('id');"), PutMeta{OriginalName: "x.jpg", ContentType: "image/jpeg"}); err == nil {
		t.Fatal("spoofed upload must be rejected on the s3 backend too")
	}

	// Delete removes metadata and the object.
	if err := store.Delete(ctx, "acme", m.ID); err != nil {
		t.Fatalf("Delete m1: %v", err)
	}
	if err := store.Delete(ctx, "acme", m2.ID); err != nil {
		t.Fatalf("Delete m2: %v", err)
	}
	objs, _ = b.List(ctx, "acme")
	if len(objs) != 0 {
		t.Fatalf("bucket objects after delete = %d, want 0", len(objs))
	}
}
