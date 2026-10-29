// DEMO-ONLY: "expire staged data" (catalog F17). Deletes a staged batch's ROW PAGES straight from the claim-check
// backend, as if its TTL or lifecycle rule had fired, and keeps the manifest so Draft still starts the workflow.
// The draft's chunk Activities then get ClaimCheckNotFound (non-retryable) and park for a human decision.
//
// This is the ONLY deletion of claim-check data in the codebase, and it deliberately bypasses claimcheck.Store,
// which stays immutable (no Delete in its interface).
package control

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/redis/go-redis/v9"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func (s *Service) expireStaged(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FiID    string `json:"fiId"`
		BatchID string `json:"batchId"`
	}
	if err := decode(r, &body); err != nil || body.FiID == "" || body.BatchID == "" {
		writeError(w, http.StatusBadRequest, "send {fiId, batchId}")
		return
	}
	prefix := fmt.Sprintf("bulkwire/%s/%s/rows/", body.FiID, body.BatchID)
	ctx, cancel := timeout(r.Context(), 30*time.Second)
	defer cancel()
	n, err := s.deletePrefix(ctx, prefix)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.Log.Info("DEMO-ONLY expired staged rows", "fi", body.FiID, "batchId", body.BatchID, "objects", n)
	writeJSON(w, http.StatusOK, map[string]any{"backend": s.Cfg.ClaimCheck.Backend, "prefix": prefix, "deleted": n})
}

func (s *Service) deletePrefix(ctx context.Context, prefix string) (int, error) {
	cc := s.Cfg.ClaimCheck
	switch cc.Backend {
	case "redis":
		rdb := redis.NewClient(&redis.Options{Addr: cc.RedisAddr})
		defer rdb.Close()
		keys, err := rdb.Keys(ctx, prefix+"*").Result()
		if err != nil || len(keys) == 0 {
			return 0, err
		}
		return len(keys), rdb.Del(ctx, keys...).Err()
	case "s3":
		c := s3.New(s3.Options{BaseEndpoint: aws.String(cc.S3Endpoint), UsePathStyle: true, Region: "us-east-1",
			Credentials: credentials.NewStaticCredentialsProvider(cc.S3AccessKey, cc.S3SecretKey, "")})
		list, err := c.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(cc.S3Bucket), Prefix: aws.String(prefix)})
		if err != nil {
			return 0, err
		}
		for _, o := range list.Contents {
			if _, err := c.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(cc.S3Bucket), Key: o.Key}); err != nil {
				return 0, err
			}
		}
		return len(list.Contents), nil
	case "gcs":
		c, err := storage.NewClient(ctx, option.WithEndpoint(cc.GCSEndpoint), option.WithoutAuthentication())
		if err != nil {
			return 0, err
		}
		defer c.Close()
		n := 0
		it := c.Bucket(cc.GCSBucket).Objects(ctx, &storage.Query{Prefix: prefix})
		for {
			attrs, err := it.Next()
			if err == iterator.Done {
				return n, nil
			}
			if err != nil {
				return n, err
			}
			if err := c.Bucket(cc.GCSBucket).Object(attrs.Name).Delete(ctx); err != nil && !strings.Contains(err.Error(), "404") {
				return n, err
			}
			n++
		}
	}
	return 0, fmt.Errorf("unknown claim-check backend %q", cc.Backend)
}
