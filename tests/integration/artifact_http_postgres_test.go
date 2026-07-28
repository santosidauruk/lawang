package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestIdentityDocumentUploadAndConfirmOverHTTPWithPostgreSQL(t *testing.T) {
	now := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	rawToken := "http-artifact-token"
	identityNumber := "127100000000009"
	tokens := session.NewProductionCryptoTokens()

	var sessionID uuid.UUID
	if err := database.QueryRow(ctx, `
		INSERT INTO verification_sessions (
			resume_token_hash,
			status,
			expires_at
		)
		VALUES ($1, 'personal_details_submitted', $2)
		RETURNING id
	`, tokens.Hash(rawToken), now.Add(30*time.Minute)).Scan(&sessionID); err != nil {
		t.Fatalf("insert HTTP tracer Verification Session: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO personal_details (
			verification_session_id,
			full_name,
			date_of_birth,
			identity_number,
			address
		)
		VALUES ($1, 'HTTP Applicant', DATE '2000-02-29', $2, 'HTTP Address')
	`, sessionID, identityNumber); err != nil {
		t.Fatalf("insert HTTP tracer Personal Details: %v", err)
	}

	presigner := &artifactPostgresPresigner{
		url: "https://public-object-host/upload",
	}
	objectStorage := &fakeHTTPObjectStorage{
		metadata: make(map[string]artifact.ObjectMetadata),
		errors:   make(map[string]error),
	}
	extractor := &fakeDocumentExtractor{
		results: make(map[string]artifact.DocumentExtraction),
		errors:  make(map[string]error),
	}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	uploadIntents := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		tokens,
		fixedClock{now: now},
	)
	artifacts := artifact.NewService(
		postgresArtifacts,
		postgresArtifacts,
		objectStorage,
		extractor,
		tokens,
		fixedClock{now: now},
	)
	handler := httpapi.NewHandler(nil, nil, artifacts, uploadIntents)

	uploadRequest := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/upload-url",
		strings.NewReader(`{"kind":"identity_document"}`),
	)
	uploadRequest.Header.Set("Authorization", "Bearer "+rawToken)
	uploadRequest.Header.Set("Content-Type", "application/json")
	uploadResponse := httptest.NewRecorder()
	handler.ServeHTTP(uploadResponse, uploadRequest)

	if uploadResponse.Code != http.StatusCreated {
		t.Fatalf(
			"upload-url status = %d, want %d; body = %s",
			uploadResponse.Code,
			http.StatusCreated,
			uploadResponse.Body.String(),
		)
	}
	var uploadBody struct {
		UploadIntentID uuid.UUID `json:"uploadIntentId"`
		UploadURL      string    `json:"uploadUrl"`
	}
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &uploadBody); err != nil {
		t.Fatalf("decode upload-url response: %v", err)
	}
	if uploadBody.UploadIntentID == uuid.Nil {
		t.Fatal("upload-url returned nil Upload Intent ID")
	}
	if uploadBody.UploadURL != presigner.url {
		t.Errorf("uploadUrl = %q, want %q", uploadBody.UploadURL, presigner.url)
	}
	expectedStorageKey := "verification-sessions/" + sessionID.String() +
		"/identity_document/" + uploadBody.UploadIntentID.String()
	if presigner.storageKey != expectedStorageKey {
		t.Fatalf(
			"presigned storage key = %q, want %q",
			presigner.storageKey,
			expectedStorageKey,
		)
	}

	objectStorage.metadata[expectedStorageKey] = artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   1024,
		ETag:        "http-tracer-etag",
	}
	extractor.results[expectedStorageKey] = artifact.DocumentExtraction{
		IdentityNumber: identityNumber,
	}

	confirmRequest := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/confirm",
		strings.NewReader(
			`{"uploadIntentId":"`+uploadBody.UploadIntentID.String()+`"}`,
		),
	)
	confirmRequest.Header.Set("Authorization", "Bearer "+rawToken)
	confirmRequest.Header.Set("Content-Type", "application/json")
	confirmResponse := httptest.NewRecorder()
	handler.ServeHTTP(confirmResponse, confirmRequest)

	if confirmResponse.Code != http.StatusOK {
		t.Fatalf(
			"confirm status = %d, want %d; body = %s",
			confirmResponse.Code,
			http.StatusOK,
			confirmResponse.Body.String(),
		)
	}
	var confirmBody struct {
		ID        uuid.UUID      `json:"id"`
		Status    session.Status `json:"status"`
		ExpiresAt time.Time      `json:"expiresAt"`
	}
	if err := json.Unmarshal(confirmResponse.Body.Bytes(), &confirmBody); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if confirmBody.ID != sessionID {
		t.Errorf("confirmed session ID = %s, want %s", confirmBody.ID, sessionID)
	}
	if confirmBody.Status != session.StatusIdentityDocumentUploaded {
		t.Errorf(
			"confirmed status = %s, want %s",
			confirmBody.Status,
			session.StatusIdentityDocumentUploaded,
		)
	}
	if !confirmBody.ExpiresAt.Equal(now.Add(30 * time.Minute)) {
		t.Errorf(
			"confirmed expiresAt = %s, want %s",
			confirmBody.ExpiresAt,
			now.Add(30*time.Minute),
		)
	}
}

type fakeHTTPObjectStorage struct {
	metadata map[string]artifact.ObjectMetadata
	errors   map[string]error
}

func (storage *fakeHTTPObjectStorage) HeadObject(
	_ context.Context,
	storageKey string,
) (artifact.ObjectMetadata, error) {
	if err, exists := storage.errors[storageKey]; exists {
		return artifact.ObjectMetadata{}, err
	}
	metadata, exists := storage.metadata[storageKey]
	if !exists {
		return artifact.ObjectMetadata{}, errors.New("missing fake object metadata")
	}
	return metadata, nil
}

type fakeDocumentExtractor struct {
	results map[string]artifact.DocumentExtraction
	errors  map[string]error
}

func (extractor *fakeDocumentExtractor) Extract(
	_ context.Context,
	storageKey string,
) (artifact.DocumentExtraction, error) {
	if err, exists := extractor.errors[storageKey]; exists {
		return artifact.DocumentExtraction{}, err
	}
	result, exists := extractor.results[storageKey]
	if !exists {
		return artifact.DocumentExtraction{}, errors.New("missing fake document extraction")
	}
	return result, nil
}
