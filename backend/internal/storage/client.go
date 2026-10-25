// Package storage wraps the S3-compatible object storage client (MinIO
// locally, any S3-compatible provider in production) used to issue
// short-lived presigned URLs and to verify that a chunk a client claims to
// have uploaded actually exists. The API process never proxies chunk
// bytes itself — see docs/backup-protocol.md.
package storage

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"deeparchive/internal/config"
)

type Client struct {
	mc     *minio.Client
	signer *minio.Client // same credentials, client-reachable host; signing is local, no network
	bucket string
}

func NewClient(cfg config.S3Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("parsing S3 endpoint %q: %w", cfg.Endpoint, err)
	}

	mc, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: u.Scheme == "https",
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("creating object storage client: %w", err)
	}

	signer := mc
	if cfg.PublicEndpoint != "" && cfg.PublicEndpoint != cfg.Endpoint {
		pu, err := url.Parse(cfg.PublicEndpoint)
		if err != nil {
			return nil, fmt.Errorf("parsing S3 public endpoint %q: %w", cfg.PublicEndpoint, err)
		}
		signer, err = minio.New(pu.Host, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			Secure: pu.Scheme == "https",
			Region: cfg.Region,
		})
		if err != nil {
			return nil, fmt.Errorf("creating presigning client: %w", err)
		}
	}

	return &Client{mc: mc, signer: signer, bucket: cfg.Bucket}, nil
}

// PresignedPutURL returns a short-lived URL the client can PUT the
// ciphertext chunk to directly, bypassing the API process entirely.
func (c *Client) PresignedPutURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	u, err := c.signer.PresignedPutObject(ctx, c.bucket, objectKey, expiry)
	if err != nil {
		return "", fmt.Errorf("presigning upload for %s: %w", objectKey, err)
	}
	return u.String(), nil
}

// PresignedGetURL returns a short-lived URL the client can GET the
// ciphertext chunk from directly, for restore.
func (c *Client) PresignedGetURL(ctx context.Context, objectKey string, expiry time.Duration) (string, error) {
	u, err := c.signer.PresignedGetObject(ctx, c.bucket, objectKey, expiry, url.Values{})
	if err != nil {
		return "", fmt.Errorf("presigning download for %s: %w", objectKey, err)
	}
	return u.String(), nil
}

// ErrNotFound is returned by Stat when the object does not exist.
var ErrNotFound = fmt.Errorf("object not found")

// Stat verifies an object actually exists in storage and returns its real
// size. Never trust a client's claimed size or existence — this is the one
// real check that an "uploaded" chunk was actually uploaded before the API
// commits to it in a snapshot. See internal/backups.Service.CommitSnapshot.
func (c *Client) Stat(ctx context.Context, objectKey string) (sizeBytes int64, err error) {
	info, err := c.mc.StatObject(ctx, c.bucket, objectKey, minio.StatObjectOptions{})
	if err != nil {
		if resp := minio.ToErrorResponse(err); resp.Code == "NoSuchKey" {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("checking object %s: %w", objectKey, err)
	}
	return info.Size, nil
}

// Remove deletes an object. Used by chunk garbage collection
// (internal/backups.Service.DeleteSnapshot via internal/retention) once a
// chunk is no longer referenced by any snapshot — object storage, not just
// the database row, must actually shrink, or retention doesn't free
// anything. Deleting an already-absent object is not an error (MinIO's
// RemoveObject is idempotent), since GC and a retried sweep could both
// race to remove the same orphaned chunk.
func (c *Client) Remove(ctx context.Context, objectKey string) error {
	if err := c.mc.RemoveObject(ctx, c.bucket, objectKey, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("removing object %s: %w", objectKey, err)
	}
	return nil
}
