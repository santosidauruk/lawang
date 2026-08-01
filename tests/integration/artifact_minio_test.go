package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	s3storageadapter "github.com/santosidauruk/lawang-go/internal/adapter/s3storage"
	"github.com/testcontainers/testcontainers-go"
	tcminio "github.com/testcontainers/testcontainers-go/modules/minio"
)

// checkpoint5MinIOPresignPutHeadObject is the Checkpoint 5 user workbench.
//
// Rename it to TestMinIOPresignPutAndHeadObjectReturnsActualMetadata, then replace
// the fatal marker while completing one numbered section at a time. Keep this as
// one behavior only: a URL signed for the configured public host accepts a direct
// HTTP JPEG upload, and HeadObject maps the stored object's actual metadata.
//
//  1. Start disposable MinIO. A Docker startup error must call t.Fatalf, never
//     t.Skip. Register container cleanup with the test lifecycle.
//  2. Create one unique bucket through AWS SDK v2 using the fixture's reachable
//     endpoint, fixed test credentials, us-east-1, and path-style addressing.
//  3. Build the smallest adapter required by artifact.UploadPresigner and
//     artifact.ObjectStorage. Keep AWS SDK input/output types inside the adapter.
//  4. Use fixed session/intent UUIDs to build the already-approved key:
//     verification-sessions/{sessionID}/identity_document/{intentID}.
//  5. Call PresignUpload with a five-minute expiry. Parse the returned URL and
//     assert its host equals the configured public endpoint before sending it.
//  6. PUT non-zero JPEG bytes with net/http directly to the returned URL. Set
//     Content-Type: image/jpeg and fail on any non-2xx status with a bounded body.
//  7. Call HeadObject through the application-owned port. Assert image/jpeg, exact
//     byte length, and a non-empty opaque ETag.
//  8. Stop for review. Do not add PNG, PDF, size/error matrices, Compose, config,
//     runtime wiring, extractor behavior, PostgreSQL, or HTTP routes in this test.

const (
	minioImage    = "minio/minio:RELEASE.2024-01-16T16-07-38Z"
	minioUsername = "lawang-test"
	minioPassword = "lawang-test-password"
	minioRegion   = "us-east-1"
)

func TestMinIOPresignPutAndHeadObjectReturnsActualMetadata(t *testing.T) {
	ctx := context.Background()

	minioContainer, err := tcminio.Run(
		ctx,
		minioImage,
		tcminio.WithUsername(minioUsername),
		tcminio.WithPassword(minioPassword),
	)
	testcontainers.CleanupContainer(t, minioContainer)

	if err != nil {
		t.Fatalf("start disposable MinIO; ensure Docker is running: %v", err)
	}

	endpoint, err := minioContainer.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("get MinIO endpoint: %v", err)
	}

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx, awsconfig.WithRegion(minioRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				minioUsername,
				minioPassword,
				"",
			),
		),
	)
	if err != nil {
		t.Fatalf("load AWS configuration: %v", err)
	}

	s3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})

	bucketName := "lawang-checkpoint5-" + uuid.NewString()

	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		t.Fatalf("create isolated MinIO bucket %q: %v", bucketName, err)
	}

	presignClient := s3.NewPresignClient(s3Client)
	sessionID := uuid.MustParse("d01e1070-8d02-4b5e-825c-d25e9d4386c3")
	uploadIntentID := uuid.MustParse("65bad214-8a57-4449-981d-16a16d862c2b")
	storageKey := fmt.Sprintf("verification-sessions/%s/identity_document/%s", sessionID.String(), uploadIntentID.String())
	storage := s3storageadapter.New(presignClient, bucketName, s3Client)

	presignUrl, err := storage.PresignUpload(ctx, storageKey, 5*time.Minute)
	if err != nil {
		t.Fatalf("presign upload: %v", err)
	}

	signedURL, err := url.Parse(presignUrl)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}

	publicEndpoint, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse public endpoint: %v", err)
	}

	if signedURL.Host != publicEndpoint.Host {
		t.Errorf(
			"presigned URL host = %q, want configured public host %q",
			signedURL.Host,
			publicEndpoint.Host)
	}
	if expires := signedURL.Query().Get("X-Amz-Expires"); expires != "300" {
		t.Errorf("presigned URL expiry = %q seconds, want 300", expires)
	}

	client := http.Client{Timeout: 5 * time.Second}
	jpegBytes := smallJPEG(t)
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signedURL.String(), bytes.NewReader(jpegBytes))
	if err != nil {
		t.Fatalf("failed to request: %v", err)
	}

	request.Header.Set("Content-Type", "image/jpeg")
	resp, err := client.Do(request)
	if err != nil {
		t.Fatalf("failed to upload image to url: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		limited := io.LimitReader(resp.Body, 4*1024)
		respBody, err := io.ReadAll(limited)
		if err != nil {
			t.Fatalf("failed to read body error: %v", err)
		}

		t.Fatalf("status upload image to url: %d, want 200, body %q", resp.StatusCode, respBody)
	}

	respHeaderEtag := resp.Header.Get("ETag")
	if respHeaderEtag == "" {
		t.Errorf("reponse ETag should not empty")
	}

	metadata, err := storage.HeadObject(ctx, storageKey)
	if err != nil {
		t.Fatalf("head object error %v", err)
	}

	if metadata.ContentType != "image/jpeg" {
		t.Errorf("metadata content type got %s, want image/jpeg", metadata.ContentType)
	}

	wantSizes := int64(len(jpegBytes))
	if metadata.SizeBytes != wantSizes {
		t.Errorf("metadata sizebytes got %d, want %d", metadata.SizeBytes, wantSizes)
	}

	if metadata.ETag == "" {
		t.Error("metadata ETag should not be empty")
	}
}

func TestMinIOPresignPutAndHeadObjectReturnsSiblingMediaMetadata(t *testing.T) {
	ctx := context.Background()
	storage := newMinIOTestStorage(t, ctx)
	sessionID := uuid.MustParse("d01e1070-8d02-4b5e-825c-d25e9d4386c3")

	tests := []struct {
		name           string
		uploadIntentID uuid.UUID
		contentType    string
		body           []byte
	}{
		{
			name:           "PNG",
			uploadIntentID: uuid.MustParse("6f6f21ee-589a-4dc1-8753-5fb29d130406"),
			contentType:    "image/png",
			body:           smallPNG(t),
		},
		{
			name:           "PDF",
			uploadIntentID: uuid.MustParse("1bf85df4-80f1-4d97-b605-572172e228a2"),
			contentType:    "application/pdf",
			body:           smallPDF(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storageKey := fmt.Sprintf(
				"verification-sessions/%s/identity_document/%s",
				sessionID,
				tt.uploadIntentID,
			)

			assertMinIOPresignedUploadMetadata(
				t,
				ctx,
				storage,
				storageKey,
				tt.contentType,
				tt.body,
			)
		})
	}
}

func newMinIOTestStorage(t *testing.T, ctx context.Context) *s3storageadapter.Adapter {
	t.Helper()

	minioContainer, err := tcminio.Run(
		ctx,
		minioImage,
		tcminio.WithUsername(minioUsername),
		tcminio.WithPassword(minioPassword),
	)
	testcontainers.CleanupContainer(t, minioContainer)
	if err != nil {
		t.Fatalf("start disposable MinIO; ensure Docker is running: %v", err)
	}

	endpoint, err := minioContainer.PortEndpoint(ctx, "9000/tcp", "http")
	if err != nil {
		t.Fatalf("get MinIO endpoint: %v", err)
	}

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(minioRegion),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				minioUsername,
				minioPassword,
				"",
			),
		),
	)
	if err != nil {
		t.Fatalf("load AWS configuration: %v", err)
	}

	s3Client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	bucketName := "lawang-checkpoint5-sibling-" + uuid.NewString()

	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(bucketName),
	})
	if err != nil {
		t.Fatalf("create isolated MinIO bucket %q: %v", bucketName, err)
	}

	return s3storageadapter.New(s3.NewPresignClient(s3Client), bucketName, s3Client)
}

func assertMinIOPresignedUploadMetadata(
	t *testing.T,
	ctx context.Context,
	storage *s3storageadapter.Adapter,
	storageKey string,
	contentType string,
	body []byte,
) {
	t.Helper()

	presignedURL, err := storage.PresignUpload(ctx, storageKey, 5*time.Minute)
	if err != nil {
		t.Fatalf("presign %s upload: %v", contentType, err)
	}

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		presignedURL,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("create %s upload request: %v", contentType, err)
	}
	request.Header.Set("Content-Type", contentType)

	client := http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("upload %s object: %v", contentType, err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		if readErr != nil {
			t.Fatalf("read failed %s upload response: %v", contentType, readErr)
		}
		t.Fatalf(
			"upload %s status = %d, want 200, body = %q",
			contentType,
			response.StatusCode,
			responseBody,
		)
	}

	metadata, err := storage.HeadObject(ctx, storageKey)
	if err != nil {
		t.Fatalf("head %s object: %v", contentType, err)
	}
	if metadata.ContentType != contentType {
		t.Errorf("metadata content type = %q, want %q", metadata.ContentType, contentType)
	}
	if metadata.SizeBytes != int64(len(body)) {
		t.Errorf("metadata size = %d, want %d", metadata.SizeBytes, len(body))
	}
	if metadata.ETag == "" {
		t.Error("metadata ETag should not be empty")
	}
}

func smallJPEG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.White)

	var buffer bytes.Buffer
	err := jpeg.Encode(
		&buffer, imageData, &jpeg.Options{Quality: 80},
	)
	if err != nil {
		t.Fatalf("encode test JPEG: %v", err)
	}
	if buffer.Len() == 0 {
		t.Fatalf("encoded test JPEG is empty")
	}
	return buffer.Bytes()
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.White)

	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageData); err != nil {
		t.Fatalf("encode test PNG: %v", err)
	}
	if buffer.Len() == 0 {
		t.Fatal("encoded test PNG is empty")
	}
	return buffer.Bytes()
}

func smallPDF() []byte {
	return []byte("%PDF-1.4\n% lawang integration fixture\n%%EOF\n")
}
