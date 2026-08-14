package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
	svc            *s3.Client
	bucket         string
	endpoint       string
	forcePathStyle bool
	logger         *slog.Logger
	cleanupMu      sync.Mutex
	cleanupToken   *string
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

	return &Repository{svc: svc, bucket: cfg.Bucket, endpoint: strings.TrimRight(cfg.Endpoint, "/"), forcePathStyle: cfg.ForcePathStyle, logger: logger}, nil
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

func (r *Repository) Stat(ctx context.Context, key string) (*domain.ObjectInfo, error) {
	out, err := r.svc.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(r.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var notFound *types.NotFound
		if errors.As(err, &notFound) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("s3 head %q: %w", key, err)
	}
	contentType := ""
	if out.ContentType != nil {
		contentType = *out.ContentType
	}
	size := int64(0)
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	lastModified := time.Time{}
	if out.LastModified != nil {
		lastModified = *out.LastModified
	}
	return &domain.ObjectInfo{Key: key, ContentType: contentType, SizeBytes: size, LastModified: lastModified}, nil
}

// ListExpiredUploads incrementally scans the uploads prefix. The continuation
// token is retained between cleanup cycles so large buckets are covered without
// performing an unbounded full-bucket listing every minute.
func (r *Repository) ListExpiredUploads(ctx context.Context, before time.Time, limit int) ([]domain.ObjectInfo, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	r.cleanupMu.Lock()
	defer r.cleanupMu.Unlock()

	token := r.cleanupToken
	expired := make([]domain.ObjectInfo, 0, limit)
	for page := 0; page < 10 && len(expired) < limit; page++ {
		out, err := r.svc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(r.bucket),
			Prefix:            aws.String("uploads/"),
			ContinuationToken: token,
			MaxKeys:           aws.Int32(int32(limit)),
		})
		if err != nil {
			return nil, fmt.Errorf("list expired uploads: %w", err)
		}
		for _, object := range out.Contents {
			if object.Key == nil || object.LastModified == nil || object.LastModified.After(before) {
				continue
			}
			expired = append(expired, domain.ObjectInfo{Key: *object.Key, SizeBytes: aws.ToInt64(object.Size), LastModified: *object.LastModified})
			if len(expired) == limit {
				break
			}
		}
		if !aws.ToBool(out.IsTruncated) || out.NextContinuationToken == nil {
			token = nil
			break
		}
		token = out.NextContinuationToken
	}
	r.cleanupToken = token
	return expired, nil
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

func (r *Repository) PresignPutURL(ctx context.Context, key, contentType string, contentLength int64, ttl time.Duration) (string, http.Header, error) {
	client := s3.NewPresignClient(r.svc)
	input := &s3.PutObjectInput{
		Bucket:        aws.String(r.bucket),
		Key:           aws.String(key),
		ContentLength: aws.Int64(contentLength),
	}
	if strings.TrimSpace(contentType) != "" {
		input.ContentType = aws.String(contentType)
	}
	out, err := client.PresignPutObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", nil, fmt.Errorf("s3 presign put %q: %w", key, err)
	}
	return out.URL, out.SignedHeader, nil
}

func (r *Repository) SourceURLForKey(key string) string {
	return "s3://" + r.bucket + "/" + strings.TrimLeft(key, "/")
}

func (r *Repository) KeyFromOwnURL(rawURL string, userID int64) (string, bool, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false, nil
	}

	var key string
	switch strings.ToLower(u.Scheme) {
	case "s3":
		if u.Host != r.bucket {
			return "", false, nil
		}
		key = strings.TrimLeft(u.Path, "/")
	case "http", "https":
		if r.endpoint == "" {
			return "", false, nil
		}
		endpoint, err := url.Parse(r.endpoint)
		if err != nil || !sameEndpoint(u, endpoint) {
			return "", false, nil
		}
		path := strings.TrimLeft(u.EscapedPath(), "/")
		unescapedPath, err := url.PathUnescape(path)
		if err != nil {
			return "", true, fmt.Errorf("%w: invalid object URL path", domain.ErrInvalidURL)
		}
		if r.forcePathStyle {
			prefix := r.bucket + "/"
			if !strings.HasPrefix(unescapedPath, prefix) {
				return "", false, nil
			}
			key = strings.TrimPrefix(unescapedPath, prefix)
		} else {
			return "", false, nil
		}
	default:
		return "", false, nil
	}

	key = strings.TrimLeft(key, "/")
	if key == "" || strings.Contains(key, "..") {
		return "", true, fmt.Errorf("%w: invalid upload object key", domain.ErrInvalidURL)
	}
	ownerPrefix := fmt.Sprintf("uploads/%d/", userID)
	if !strings.HasPrefix(key, ownerPrefix) {
		return "", true, domain.ErrNotFound
	}
	return key, true, nil
}

func sameEndpoint(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func (r *Repository) Delete(ctx context.Context, key string) error {
	_, err := r.svc.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(r.bucket), Key: aws.String(key)})
	if err != nil {
		return fmt.Errorf("s3 delete %q: %w", key, err)
	}
	return nil
}
