package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gocloud.dev/blob"
	"gocloud.dev/blob/s3blob"
	"gocloud.dev/gcerrors"
)

// S3ServeMode picks how GET /api/files/{id} delivers S3-stored bytes.
type S3ServeMode string

const (
	// S3ServeRedirect (default) answers with a 302 to a short-lived presigned
	// URL: the engine AUTHORIZES (tenant + JWT + RBAC ran before the redirect)
	// but never proxies the bytes — the FILES-V1 contract. The client egresses
	// straight from the bucket (with R2 that egress is $0), and the engine is
	// never the bandwidth bottleneck.
	S3ServeRedirect S3ServeMode = "redirect"
	// S3ServeProxy streams the bytes THROUGH the engine (ServeContent over the
	// bucket reader): uniform headers, Range honored by the engine, and the
	// bucket never exposed to clients at all — at the cost of the bytes
	// transiting the engine (PocketBase's always-proxy trade-off). Pick it when
	// the bucket must stay fully private or clients cannot follow redirects.
	S3ServeProxy S3ServeMode = "proxy"
)

// S3Config is the PROVIDER-AGNOSTIC S3 configuration: endpoint + region +
// credentials + bucket + path-style covers Cloudflare R2 (the recommended
// default: $0 egress), DigitalOcean Spaces, self-hosted MinIO and AWS S3 —
// switching provider is a config change, never a code change.
type S3Config struct {
	Bucket    string
	Endpoint  string // empty ⇒ AWS S3 (the SDK default resolution)
	Region    string // empty ⇒ "auto" (R2's spelling; harmless elsewhere)
	AccessKey string
	SecretKey string
	// ForcePathStyle addresses the bucket as <endpoint>/<bucket> instead of
	// <bucket>.<endpoint> — required by MinIO, harmless on R2.
	ForcePathStyle bool
	// Prefix namespaces every key inside the bucket (default "tenants/"), so a
	// shared bucket can carry other content without collision.
	Prefix string
	// ServeMode: redirect (default) or proxy — see S3ServeMode.
	ServeMode S3ServeMode
}

// S3Backend stores blobs in any S3-compatible bucket via gocloud.dev/blob
// (the staged approach the storage investigation recommended: a proven
// portable driver first; an owned client only if binary weight is MEASURED as
// a problem — the PocketBase trajectory). Large uploads go through the SDK's
// transfer manager (automatic multipart); reads are ranged (the gocloud
// Reader seeks lazily, so ServeContent never downloads more than requested).
type S3Backend struct {
	bucket *blob.Bucket
	mode   S3ServeMode
}

// NewS3Backend opens the bucket. It fails loud on missing config — a
// misconfigured file store must stop boot, not surface as runtime 500s.
func NewS3Backend(ctx context.Context, cfg S3Config) (*S3Backend, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("files: s3 backend requires a bucket name")
	}
	if cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, fmt.Errorf("files: s3 backend requires access and secret keys")
	}
	region := cfg.Region
	if region == "" {
		region = "auto"
	}
	opts := s3.Options{
		Region:       region,
		UsePathStyle: cfg.ForcePathStyle,
		Credentials:  aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")),
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	bucket, err := s3blob.OpenBucket(ctx, s3.New(opts), cfg.Bucket, nil)
	if err != nil {
		return nil, fmt.Errorf("files: open s3 bucket %q: %w", cfg.Bucket, err)
	}
	prefix := cfg.Prefix
	if prefix == "" {
		prefix = "tenants/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	bucket = blob.PrefixedBucket(bucket, prefix)

	mode := cfg.ServeMode
	if mode == "" {
		mode = S3ServeRedirect
	}
	if mode != S3ServeRedirect && mode != S3ServeProxy {
		return nil, fmt.Errorf("files: unknown s3 serve mode %q (use %q or %q)", mode, S3ServeRedirect, S3ServeProxy)
	}
	return &S3Backend{bucket: bucket, mode: mode}, nil
}

var _ Backend = (*S3Backend)(nil)

// Close releases the bucket's connections.
func (b *S3Backend) Close() error { return b.bucket.Close() }

// Put streams r to the bucket. gocloud's writer hands large bodies to the S3
// transfer manager (automatic multipart upload), so a 5 GiB object streams in
// parts without ever being buffered whole.
func (b *S3Backend) Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error {
	if err := validKey(key); err != nil {
		return err
	}
	w, err := b.bucket.NewWriter(ctx, key, &blob.WriterOptions{
		ContentType:        opts.ContentType,
		ContentDisposition: opts.ContentDisposition,
		CacheControl:       opts.CacheControl,
	})
	if err != nil {
		return fmt.Errorf("files: s3 open writer: %w", err)
	}
	if _, err := io.CopyBuffer(w, r, make([]byte, copyBufSize)); err != nil {
		_ = w.Close()
		return fmt.Errorf("files: s3 stream upload: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("files: s3 commit upload: %w", err)
	}
	return nil
}

// Get opens a seekable reader over the object (seeks translate to ranged GETs
// lazily — no full download).
func (b *S3Backend) Get(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	r, err := b.bucket.NewReader(ctx, key, nil)
	if err != nil {
		return nil, b.mapErr("open", key, err)
	}
	return r, nil
}

// Delete removes the object; an absent key is a no-op.
func (b *S3Backend) Delete(ctx context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if err := b.bucket.Delete(ctx, key); err != nil && gcerrors.Code(err) != gcerrors.NotFound {
		return fmt.Errorf("files: s3 delete: %w", err)
	}
	return nil
}

// Stat describes the object, or ErrNotFound.
func (b *S3Backend) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	if err := validKey(key); err != nil {
		return ObjectInfo{}, err
	}
	attrs, err := b.bucket.Attributes(ctx, key)
	if err != nil {
		return ObjectInfo{}, b.mapErr("stat", key, err)
	}
	return ObjectInfo{
		Key: key, Size: attrs.Size, ContentType: attrs.ContentType,
		ModTime: attrs.ModTime, ETag: attrs.ETag,
	}, nil
}

// List enumerates the objects under prefix.
func (b *S3Backend) List(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	if err := validKeyPrefix(prefix); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	iter := b.bucket.List(&blob.ListOptions{Prefix: prefix})
	var out []ObjectInfo
	for {
		obj, err := iter.Next(ctx)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("files: s3 list: %w", err)
		}
		out = append(out, ObjectInfo{Key: obj.Key, Size: obj.Size, ModTime: obj.ModTime})
	}
}

// Serve delivers the object per the configured mode: redirect (302 to a
// short-lived presigned URL — the engine authorized, the bucket serves) or
// proxy (ServeContent over the lazy-seeking bucket reader — Range/ETag from
// the engine, bucket never exposed).
func (b *S3Backend) Serve(w http.ResponseWriter, r *http.Request, key string, info ServeInfo) error {
	if err := validKey(key); err != nil {
		return err
	}
	if b.mode == S3ServeRedirect {
		u, err := b.bucket.SignedURL(r.Context(), key, &blob.SignedURLOptions{Expiry: DefaultSignedURLTTL})
		if err != nil {
			return b.mapErr("sign", key, err)
		}
		// The object was stored with its Content-Type/Disposition, so the bucket
		// replays them on the presigned GET.
		http.Redirect(w, r, u, http.StatusFound)
		return nil
	}

	rd, err := b.bucket.NewReader(r.Context(), key, nil)
	if err != nil {
		return b.mapErr("open", key, err)
	}
	defer rd.Close() //nolint:errcheck

	setServeHeaders(w, info)
	http.ServeContent(w, r, "", info.ModTime, rd)
	return nil
}

// SignedURL mints a native presigned GET (Signature v4) valid for expiry.
func (b *S3Backend) SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	if expiry <= 0 {
		expiry = DefaultSignedURLTTL
	}
	u, err := b.bucket.SignedURL(ctx, key, &blob.SignedURLOptions{Expiry: expiry})
	if err != nil {
		return "", b.mapErr("sign", key, err)
	}
	return u, nil
}

func (b *S3Backend) mapErr(op, key string, err error) error {
	if gcerrors.Code(err) == gcerrors.NotFound {
		return ErrNotFound
	}
	return fmt.Errorf("files: s3 %s %s: %w", op, key, err)
}
