// S3 backend (profile claimcheck-s3). Plain aws-sdk-go-v2 against any S3-compatible endpoint: RustFS in this
// demo (MinIO no longer ships images), AWS S3 in production. Expiry is a
// bucket lifecycle rule (7 days) created by rustfs-init.
// PRODUCTION NOTE: on AWS, use a bucket lifecycle rule and SSE-KMS, and IAM roles instead of static keys.
package claimcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// S3 is a Store backed by one S3 bucket.
type S3 struct {
	client *s3.Client
	bucket string
}

// NewS3 builds a client for an S3-compatible endpoint with path-style addressing and static credentials.
func NewS3(_ context.Context, endpoint, bucket, accessKey, secretKey string) (*S3, error) {
	client := s3.New(s3.Options{
		BaseEndpoint: aws.String(endpoint),
		UsePathStyle: true, // bucket in the path: http://rustfs:9000/bulkwire-claimcheck/key
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
	})
	return &S3{client: client, bucket: bucket}, nil
}

func (s *S3) Name() string { return "s3" }

func (s *S3) Ping(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)})
	return err
}

func (s *S3) Put(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	return err
}

func (s *S3) Get(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	defer out.Body.Close()
	return io.ReadAll(out.Body)
}

func isNotFound(err error) bool {
	var noKey *types.NoSuchKey
	if errors.As(err, &noKey) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && (apiErr.ErrorCode() == "NoSuchKey" || apiErr.ErrorCode() == "NotFound") {
		return true
	}
	var respErr *smithyhttp.ResponseError
	return errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound
}
