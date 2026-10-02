// Package storage wraps the S3-compatible object store that holds product
// images.
//
// The critical invariant: Endpoint is the ONE address used both to sign URLs
// and to fetch them from the browser. An S3 presigned signature covers the
// Host header, so rewriting the host after signing invalidates it. Keeping a
// single value is what makes presigned PUT/GET work in the browser with no
// rewriting — see the README section "Why one MinIO endpoint".
package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// Config holds the object storage settings, all env-driven so the same
// binary talks to MinIO locally and real S3 in production (CLAUDE.md §3).
type Config struct {
	// Provider is "minio" or "s3". Informational — the wire protocol is
	// identical; only endpoint and addressing style differ.
	Provider string
	// Endpoint is used for BOTH signing and browser fetches. See the package
	// comment before adding any second address.
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// ForcePathStyle must be true for MinIO, which does not support the
	// virtual-host bucket addressing real S3 prefers.
	ForcePathStyle bool
	PresignPutTTL  time.Duration
	PresignGetTTL  time.Duration
}

// Client is the catalog service's handle on object storage.
type Client struct {
	s3      *s3.Client
	presign *s3.PresignClient
	cfg     Config
}

// New builds a storage client. It does not touch the network.
func New(ctx context.Context, cfg Config) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
		// Static credentials from our own env, never the ambient AWS
		// credential chain: on a developer laptop that chain could silently
		// pick up a real AWS profile and write product images into someone's
		// personal S3 account.
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: loading aws config: %w", err)
	}

	s3Client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
	})

	return &Client{
		s3:      s3Client,
		presign: s3.NewPresignClient(s3Client),
		cfg:     cfg,
	}, nil
}

// Bucket returns the configured bucket name.
func (c *Client) Bucket() string { return c.cfg.Bucket }

// Endpoint returns the single signing/browser endpoint.
func (c *Client) Endpoint() string { return c.cfg.Endpoint }

// EnsureBucket creates the bucket if it does not exist.
//
// Called on startup (CLAUDE.md §6.6) so a fresh `make dev` needs no manual
// console step and no one-shot init container.
func (c *Client) EnsureBucket(ctx context.Context, logger *slog.Logger) error {
	_, err := c.s3.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(c.cfg.Bucket),
	})
	if err == nil {
		logger.InfoContext(ctx, "created object storage bucket",
			slog.String("bucket", c.cfg.Bucket))
		return nil
	}

	// Already existing is the steady state on every restart after the first,
	// and is success, not failure.
	var owned *types.BucketAlreadyOwnedByYou
	var exists *types.BucketAlreadyExists
	if errors.As(err, &owned) || errors.As(err, &exists) {
		logger.DebugContext(ctx, "object storage bucket already present",
			slog.String("bucket", c.cfg.Bucket))
		return nil
	}
	return fmt.Errorf("storage: ensuring bucket %q: %w", c.cfg.Bucket, err)
}

// PresignPut returns a URL the browser can PUT an object to directly.
//
// Uploading straight to storage keeps multi-megabyte image bodies out of the
// API process entirely (CLAUDE.md §6.6). contentType is bound into the
// signature, so a client that promises an image cannot then upload something
// else.
func (c *Client) PresignPut(ctx context.Context, key, contentType string) (string, error) {
	req, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.cfg.Bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, s3.WithPresignExpires(c.cfg.PresignPutTTL))
	if err != nil {
		return "", fmt.Errorf("storage: presigning upload for %q: %w", key, err)
	}
	return req.URL, nil
}

// PresignGet returns a time-limited URL for reading an object.
//
// The bucket stays private; reads are always through a short-lived signed
// URL generated at response time, so revoking access is a matter of not
// issuing another one.
func (c *Client) PresignGet(ctx context.Context, key string) (string, error) {
	req, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.cfg.Bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(c.cfg.PresignGetTTL))
	if err != nil {
		return "", fmt.Errorf("storage: presigning download for %q: %w", key, err)
	}
	return req.URL, nil
}

// PresignPutTTL returns how long an upload URL stays valid, for the
// expires_in field a client uses to decide when to request a fresh one.
func (c *Client) PresignPutTTL() time.Duration { return c.cfg.PresignPutTTL }

// PutObject writes an object directly from this service.
//
// Used only by the CSV importer, which re-hosts images referenced by URL
// (CLAUDE.md §6.5). Browser uploads never come through here — they go
// straight to storage via a presigned PUT, so image bodies never traverse
// this process.
func (c *Client) PutObject(ctx context.Context, key, contentType string, body []byte) error {
	if _, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.cfg.Bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(body),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(body))),
	}); err != nil {
		return fmt.Errorf("storage: putting %q: %w", key, err)
	}
	return nil
}

// Delete removes an object. Used to clean up after the storage self-test.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.cfg.Bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: deleting %q: %w", key, err)
	}
	return nil
}

// Check returns a readiness probe confirming the bucket is reachable.
func (c *Client) Check() func(ctx context.Context) error {
	return func(ctx context.Context) error {
		_, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{
			Bucket: aws.String(c.cfg.Bucket),
		})
		return err
	}
}
