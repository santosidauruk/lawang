package s3storage

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsretry "github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/santosidauruk/lawang/internal/application/cleanup"
)

func (a *Adapter) DeleteObject(ctx context.Context, storageKey string) error {
	_, err := a.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(storageKey),
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}

		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.ErrorCode() {
			case "NoSuchKey", "NotFound":
				return cleanup.ErrCleanupObjectNotFound
			}
		}
		if awsretry.NewStandard().IsErrorRetryable(err) {
			return cleanup.ErrTransientCleanup
		}
		return cleanup.ErrPermanentCleanup
	}
	return nil
}
