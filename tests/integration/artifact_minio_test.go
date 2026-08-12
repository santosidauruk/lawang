package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/deterministicextractor"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	s3storageadapter "github.com/santosidauruk/lawang-go/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
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

	container := startDisposableMinIO(t, ctx)

	endpoint, err := container.PortEndpoint(ctx, "9000/tcp", "http")
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
	objectStorage := s3storageadapter.New(presignClient, bucketName, s3Client)

	presignUrl, err := objectStorage.PresignUpload(ctx, storageKey, 5*time.Minute)
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

	metadata, err := objectStorage.HeadObject(ctx, storageKey)
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

func TestMinIOObjectMetadataDrivesIdentityDocumentRejection(t *testing.T) {
	now := time.Date(2026, 8, 3, 13, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create postgresql pool: %v", err)
	}
	t.Cleanup(pool.Close)

	storage := newMinIOTestStorage(t, ctx)
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool))

	service := artifact.NewService(
		postgresArtifacts,
		confirmCoordinator,
		storage,
		deterministicextractor.New(nil, nil),
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	tests := []struct {
		name        string
		contentType string
		body        []byte
		wantReason  artifact.FailureReason
	}{
		{
			name:        "empty object",
			contentType: "image/jpeg",
			body:        nil,
			wantReason:  artifact.ReasonObjectEmpty,
		},
		{
			name:        "object above ten MiB",
			contentType: "image/jpeg",
			body:        make([]byte, 10*1024*1024+1),
			wantReason:  artifact.ReasonObjectTooLarge,
		},
		{
			name:        "unsupported content type",
			contentType: "text/plain",
			body:        []byte("not an identity document"),
			wantReason:  artifact.ReasonUnsupportedContentType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedArtifactConfirmationState(t, ctx, database, now)
			storageKey := "artifact/" + fixture.uploadIntentID.String()
			putMinIOObject(t, ctx, storage, storageKey, tt.contentType, tt.body)

			_, err := service.Confirm(
				ctx,
				fixture.sessionID,
				fixture.rawToken,
				fixture.uploadIntentID,
			)
			var artifactError *artifact.Error
			if !errors.As(err, &artifactError) {
				t.Fatalf("Confirm() error = %v, want artifact.Error", err)
			}
			if artifactError.Code != artifact.CodeInvalidObjectMetadata ||
				artifactError.Reason != tt.wantReason {
				t.Errorf(
					"Confirm() error = %s/%s, want %s/%s",
					artifactError.Code,
					artifactError.Reason,
					artifact.CodeInvalidObjectMetadata,
					tt.wantReason,
				)
			}
		})
	}
}

func TestS3StorageFailuresRemainBoundedAtHTTPBoundary(t *testing.T) {
	now := time.Date(2026, 8, 3, 13, 30, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create postgresql pool: %v", err)
	}
	t.Cleanup(pool.Close)

	minioStorage := newMinIOTestStorage(t, ctx)
	unavailableStorage := newUnavailableS3TestStorage(t, ctx)

	tests := []struct {
		name    string
		storage *s3storageadapter.Adapter
	}{
		{name: "missing MinIO object", storage: minioStorage},
		{name: "unavailable S3 endpoint", storage: unavailableStorage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedArtifactConfirmationState(t, ctx, database, now)
			postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
			confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool))
			service := artifact.NewService(
				postgresArtifacts,
				confirmCoordinator,
				tt.storage,
				deterministicextractor.New(nil, nil),
				session.NewProductionCryptoTokens(),
				fixedClock{now: now},
			)
			handler := httpapi.NewHandler(nil, nil, service, nil)
			request := httptest.NewRequest(
				http.MethodPost,
				"/verification-sessions/"+fixture.sessionID.String()+"/artifacts/confirm",
				strings.NewReader(
					`{"uploadIntentId":"`+fixture.uploadIntentID.String()+`"}`,
				),
			)
			request.Header.Set("Authorization", "Bearer "+fixture.rawToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)

			if response.Code != http.StatusServiceUnavailable {
				t.Fatalf(
					"confirm status = %d, want %d; body = %s",
					response.Code,
					http.StatusServiceUnavailable,
					response.Body.String(),
				)
			}
			var apiError httpapi.APIError
			if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
				t.Fatalf("decode storage failure response: %v", err)
			}
			if apiError.Code != string(artifact.CodeObjectStorageFailed) ||
				apiError.Message != "object storage is temporarily unavailable" {
				t.Errorf("storage failure response = %#v", apiError)
			}
			storageKey := "artifact/" + fixture.uploadIntentID.String()
			for _, forbidden := range []string{storageKey, "127.0.0.1:1", "lawang-unavailable"} {
				if strings.Contains(response.Body.String(), forbidden) {
					t.Errorf("storage failure response leaked %q", forbidden)
				}
			}
		})
	}
}

func TestMinIOEnsureBucketCreatesAndReusesConfiguredBucket(t *testing.T) {
	ctx := context.Background()
	container := startDisposableMinIO(t, ctx)
	endpoint, err := container.PortEndpoint(ctx, "9000/tcp", "http")
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
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(endpoint)
		options.UsePathStyle = true
	})
	bucketName := "lawang-ready-" + uuid.NewString()
	storage := s3storageadapter.New(s3.NewPresignClient(client), bucketName, client)

	if err := storage.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket() first call error = %v", err)
	}
	if err := storage.EnsureBucket(ctx); err != nil {
		t.Fatalf("EnsureBucket() repeated call error = %v", err)
	}
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucketName),
	}); err != nil {
		t.Fatalf("HeadBucket() after readiness error = %v", err)
	}
}

func TestS3EnsureBucketFailsWhenInternalEndpointIsUnavailable(t *testing.T) {
	ctx := context.Background()
	storage := newUnavailableS3TestStorage(t, ctx)

	if err := storage.EnsureBucket(ctx); err == nil {
		t.Fatal("EnsureBucket() error = nil, want unavailable endpoint error")
	}
}

func startDisposableMinIO(t *testing.T, ctx context.Context) *tcminio.MinioContainer {
	t.Helper()

	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf(
				"start disposable MinIO: Docker provider unavailable: %v",
				recovered,
			)
		}
	}()
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

	return minioContainer
}

func newMinIOTestStorage(t *testing.T, ctx context.Context) *s3storageadapter.Adapter {
	t.Helper()

	minioContainer := startDisposableMinIO(t, ctx)

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

func newUnavailableS3TestStorage(
	t *testing.T,
	ctx context.Context,
) *s3storageadapter.Adapter {
	t.Helper()

	awsConfig, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(minioRegion),
		awsconfig.WithRetryMaxAttempts(1),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				minioUsername,
				minioPassword,
				"",
			),
		),
	)
	if err != nil {
		t.Fatalf("load unavailable S3 test configuration: %v", err)
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String("http://127.0.0.1:1")
		options.UsePathStyle = true
	})
	return s3storageadapter.New(
		s3.NewPresignClient(client),
		"lawang-unavailable",
		client,
	)
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

func putMinIOObject(
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
	putObjectToURL(t, ctx, presignedURL, contentType, body)
}

func putObjectToURL(
	t *testing.T,
	ctx context.Context,
	uploadURL string,
	contentType string,
	body []byte,
) {
	t.Helper()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPut,
		uploadURL,
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("create %s upload request: %v", contentType, err)
	}
	request.Header.Set("Content-Type", contentType)

	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("upload %s object: %v", contentType, err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, 4*1024))
		if readErr != nil {
			t.Fatalf("read failed %s upload response: %v", contentType, readErr)
		}
		t.Fatalf(
			"upload %s status = %d, want 2xx, body = %q",
			contentType,
			response.StatusCode,
			responseBody,
		)
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
