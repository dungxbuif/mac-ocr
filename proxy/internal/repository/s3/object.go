package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"macocr/proxy/domain"
)

type Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

type Repository struct {
	svc    *s3.Client
	bucket string
	logger *slog.Logger
}

func New(cfg Config, logger *slog.Logger) (*Repository, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(
		context.Background(),
		awsconfig.WithRegion(cfg.Region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	svc := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
		o.UsePathStyle = cfg.ForcePathStyle
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})

	return &Repository{svc: svc, bucket: cfg.Bucket, logger: logger}, nil
}

func (r *Repository) Ping(ctx context.Context) error {
	_, err := r.svc.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(r.bucket),
	})
	if err != nil {
		return fmt.Errorf("%w: %s", domain.ErrStorageUnavailable, err)
	}
	r.logger.Debug("s3 ping ok", "bucket", r.bucket)
	return nil
}

func (r *Repository) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	buf, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("read body for %q: %w", key, err)
	}
	_, err = r.svc.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(r.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(buf),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("s3 put %q: %w", key, err)
	}
	r.logger.Debug("s3 put ok", "bucket", r.bucket, "key", key, "size", len(buf))
	return nil
}

func (r *Repository) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := r.svc.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get %q: %w", key, err)
	}
	return out.Body, nil
}

func (r *Repository) Exists(ctx context.Context, key string) (bool, error) {
	_, err := r.svc.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	return false, fmt.Errorf("s3 head %q: %w", key, err)
}

func (r *Repository) PresignGetURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	client := s3.NewPresignClient(r.svc)
	out, err := client.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("s3 presign get %q: %w", key, err)
	}
	return out.URL, nil
}

func (r *Repository) Delete(ctx context.Context, key string) error {
	_, err := r.svc.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("s3 delete %q: %w", key, err)
	}
	return nil
}
