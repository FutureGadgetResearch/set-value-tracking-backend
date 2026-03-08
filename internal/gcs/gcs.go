package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

// Client wraps a GCS bucket handle.
type Client struct {
	bucket *storage.BucketHandle
}

// NewClient creates a GCS client using Application Default Credentials.
func NewClient(ctx context.Context, bucket string) (*Client, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcs.NewClient: %w", err)
	}
	return &Client{bucket: c.Bucket(bucket)}, nil
}

// Download fetches object from GCS and writes it to destPath (creates dirs as needed).
// If the object does not exist, returns nil (no-op) so callers that tolerate
// missing files (e.g. ev_history for a new set) continue to work.
func (c *Client) Download(ctx context.Context, object, destPath string) error {
	r, err := c.bucket.Object(object).NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil
		}
		return fmt.Errorf("gcs Download %s: %w", object, err)
	}
	defer r.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
		return fmt.Errorf("gcs Download mkdir %s: %w", destPath, err)
	}
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("gcs Download create %s: %w", destPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("gcs Download copy %s: %w", destPath, err)
	}
	return nil
}

// DownloadAll lists all objects with the given prefix and downloads each to
// the same relative local path. Used to sync data/ev_history/ en masse.
func (c *Client) DownloadAll(ctx context.Context, prefix string) error {
	it := c.bucket.Objects(ctx, &storage.Query{Prefix: prefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("gcs DownloadAll list %s: %w", prefix, err)
		}
		if strings.HasSuffix(attrs.Name, "/") {
			continue // skip directory placeholder objects
		}
		if err := c.Download(ctx, attrs.Name, attrs.Name); err != nil {
			return err
		}
	}
	return nil
}

// Upload reads srcPath from disk and writes it to object in GCS.
func (c *Client) Upload(ctx context.Context, object, srcPath string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("gcs Upload open %s: %w", srcPath, err)
	}
	defer f.Close()

	w := c.bucket.Object(object).NewWriter(ctx)
	if _, err := io.Copy(w, f); err != nil {
		_ = w.Close()
		return fmt.Errorf("gcs Upload copy %s: %w", object, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("gcs Upload close %s: %w", object, err)
	}
	return nil
}

// UploadAll uploads every local file matching prefix/* to GCS at the same path.
func (c *Client) UploadAll(ctx context.Context, prefix string) error {
	entries, err := os.ReadDir(prefix)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("gcs UploadAll readdir %s: %w", prefix, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		localPath := filepath.Join(prefix, e.Name())
		object := filepath.ToSlash(localPath)
		if err := c.Upload(ctx, object, localPath); err != nil {
			return err
		}
	}
	return nil
}
