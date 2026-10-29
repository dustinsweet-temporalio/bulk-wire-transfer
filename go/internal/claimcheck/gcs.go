// GCS backend (profile claimcheck-gcs), against fake-gcs-server in this demo.
// PRODUCTION NOTE: fake-gcs-server does not enforce lifecycle rules; on GCP configure Object Lifecycle
// Management (delete after 7 days) and CMEK as required, and use workload identity instead of no auth.
package claimcheck

import (
	"context"
	"errors"
	"io"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

// GCS is a Store backed by one GCS bucket.
type GCS struct {
	client *storage.Client
	bucket string
}

// NewGCS builds a client for a GCS endpoint. An empty endpoint means real GCS with default credentials.
func NewGCS(ctx context.Context, endpoint, bucket string) (*GCS, error) {
	var opts []option.ClientOption
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint), option.WithoutAuthentication())
	}
	client, err := storage.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &GCS{client: client, bucket: bucket}, nil
}

func (g *GCS) Name() string { return "gcs" }

func (g *GCS) Ping(ctx context.Context) error {
	_, err := g.client.Bucket(g.bucket).Attrs(ctx)
	return err
}

func (g *GCS) Put(ctx context.Context, key string, data []byte, contentType string) error {
	w := g.client.Bucket(g.bucket).Object(key).NewWriter(ctx)
	w.ContentType = contentType
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

func (g *GCS) Get(ctx context.Context, key string) ([]byte, error) {
	r, err := g.client.Bucket(g.bucket).Object(key).NewReader(ctx)
	if errors.Is(err, storage.ErrObjectNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
