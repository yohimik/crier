package stage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog"
	"github.com/yohimik/crier/internal/config"
)

// S3 uploads to an S3-compatible bucket.
//
// It uses minio-go rather than the AWS SDK because the endpoints crier is
// pointed at are as often MinIO, R2, B2 or Spaces as they are S3 itself, and
// minio-go treats a custom endpoint as the normal case.
type S3 struct {
	cfg    config.S3
	client *minio.Client
	log    zerolog.Logger
}

// NewS3 builds the S3 stager and its client.
func NewS3(cfg config.S3, log zerolog.Logger) (*S3, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("stage.s3.endpoint is empty")
	}
	// An endpoint written with a scheme is what most people copy out of a
	// dashboard, and minio-go wants it without one.
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" && u.Scheme != "" {
		cfg.UseSSL = u.Scheme == "https"
		endpoint = u.Host
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", endpoint, err)
	}
	return &S3{cfg: cfg, client: client, log: log}, nil
}

// Name implements Stager.
func (s *S3) Name() string { return string(ModeS3) }

// Stage uploads the asset and returns a URL the platform can fetch.
func (s *S3) Stage(ctx context.Context, a Asset) (*Object, error) {
	key := s.key(a)
	opts := minio.PutObjectOptions{ContentType: a.ContentType}
	if acl := strings.TrimSpace(s.cfg.ACL); acl != "" {
		// minio-go passes an x-amz-acl entry through as a header rather than as
		// user metadata, which is how a canned ACL is set.
		opts.UserMetadata = map[string]string{"x-amz-acl": acl}
	}

	start := time.Now()
	if _, err := s.client.FPutObject(ctx, s.cfg.Bucket, key, a.Path, opts); err != nil {
		return nil, fmt.Errorf("uploading to s3://%s/%s: %w", s.cfg.Bucket, key, err)
	}
	s.log.Debug().
		Str("bucket", s.cfg.Bucket).Str("key", key).
		Int64("bytes", a.Size).Dur("elapsed", time.Since(start)).
		Msg("uploaded the staged file")

	link, err := s.url(ctx, key)
	if err != nil {
		// The object is up but unreachable; removing it keeps the bucket clean.
		_ = s.remove(context.WithoutCancel(ctx), key)
		return nil, err
	}

	obj := &Object{URL: link, Remove: noRemove}
	if s.cfg.DeleteAfter {
		obj.Remove = func(ctx context.Context) error { return s.remove(ctx, key) }
	}
	return obj, nil
}

// Close implements Stager. The client holds no resources of its own.
func (s *S3) Close(context.Context) error { return nil }

// url is where the uploaded object can be fetched.
//
// A presigned URL is the default because it works on a private bucket, which
// is the safer thing to have configured; a public base URL is what a CDN in
// front of the bucket wants.
func (s *S3) url(ctx context.Context, key string) (string, error) {
	if !s.cfg.Presign {
		base := strings.TrimRight(s.cfg.PublicBaseURL, "/")
		if base == "" {
			return "", fmt.Errorf("stage.s3.presign is false and stage.s3.public-base-url is empty")
		}
		return base + "/" + key, nil
	}
	expiry := config.Duration(s.cfg.PresignExpiry)
	if expiry <= 0 {
		expiry = time.Hour
	}
	u, err := s.client.PresignedGetObject(ctx, s.cfg.Bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("signing a URL for s3://%s/%s: %w", s.cfg.Bucket, key, err)
	}
	return u.String(), nil
}

func (s *S3) remove(ctx context.Context, key string) error {
	if err := s.client.RemoveObject(ctx, s.cfg.Bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("removing s3://%s/%s: %w", s.cfg.Bucket, key, err)
	}
	s.log.Debug().Str("bucket", s.cfg.Bucket).Str("key", key).Msg("removed the staged file")
	return nil
}

// key is the object name: the configured prefix, a random component and the
// asset's own name.
//
// The random component is what keeps two runs from overwriting each other, and
// what keeps a URL from being guessable on a bucket that is public.
func (s *S3) key(a Asset) string {
	name := a.Name
	if name == "" {
		name = filepath.Base(a.Path)
	}
	name = sanitiseName(name)
	return path.Join(strings.Trim(s.cfg.Prefix, "/"), randomToken()+"-"+name)
}

// randomToken is 8 random bytes as hex.
func randomToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on any platform crier runs on, and a
		// timestamp is a good enough fallback for a name.
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// sanitiseName keeps an object key to characters that survive every S3
// implementation and every URL.
func sanitiseName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "asset"
	}
	return out
}

// Ping checks the bucket is there and the keys are accepted.
//
// A HEAD on the bucket is the cheapest call an object store offers, and it
// fails for every reason an upload would: wrong endpoint, wrong keys, wrong
// region, a bucket that does not exist.
func (s *S3) Ping(ctx context.Context) (string, error) {
	ok, err := s.client.BucketExists(ctx, s.cfg.Bucket)
	if err != nil {
		return "", fmt.Errorf("reaching s3://%s: %w", s.cfg.Bucket, err)
	}
	if !ok {
		return "", fmt.Errorf("the bucket %q does not exist, or these credentials cannot see it", s.cfg.Bucket)
	}
	return fmt.Sprintf("bucket %s at %s", s.cfg.Bucket, s.client.EndpointURL().Host), nil
}
