package s3storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/santosidauruk/lawang/internal/application/artifact"
)

type Adapter struct {
	presigner *s3.PresignClient
	bucket    string
	client    *s3.Client
}

// EnsureBucket creates the configured bucket when needed and verifies that the
// server-side client can reach it. It is safe to call again for an existing bucket.
func (a *Adapter) EnsureBucket(ctx context.Context) error {
	_, createErr := a.client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(a.bucket),
	})
	_, headErr := a.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(a.bucket),
	})
	if headErr == nil {
		return nil
	}
	if createErr != nil {
		return fmt.Errorf(
			"ensure object storage bucket: create: %v; readiness: %w",
			createErr,
			headErr,
		)
	}
	return fmt.Errorf("ensure object storage bucket readiness: %w", headErr)
}

func New(presigner *s3.PresignClient, bucket string, s3Client *s3.Client) *Adapter {
	return &Adapter{
		presigner: presigner,
		bucket:    bucket,
		client:    s3Client,
	}
}

func (a *Adapter) PresignUpload(ctx context.Context, storageKey string, expires time.Duration) (string, error) {
	request, err := a.presigner.PresignPutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket: aws.String(a.bucket),
			Key:    aws.String(storageKey),
		},
		func(options *s3.PresignOptions) {
			options.Expires = expires
		},
	)
	if err != nil {
		return "", err
	}
	return request.URL, nil
}

func (a *Adapter) HeadObject(ctx context.Context, storageKey string) (artifact.ObjectMetadata, error) {
	output, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(storageKey),
	})
	if err != nil {
		return artifact.ObjectMetadata{}, err
	}

	if output.ContentLength == nil || output.ContentType == nil || output.ETag == nil {
		return artifact.ObjectMetadata{}, errors.New("head object response missing required metadata")
	}
	return artifact.ObjectMetadata{
		ContentType: *output.ContentType,
		SizeBytes:   *output.ContentLength,
		ETag:        *output.ETag,
	}, nil
}
