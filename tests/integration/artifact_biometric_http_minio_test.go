package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/session"
	"github.com/santosidauruk/lawang/internal/domain/sessionevent"
)

// TestBiometricCaptureUploadPutAndConfirmOverHTTPWithPostgreSQLAndMinIO is the
// Issue 008 Checkpoint 5 user-owned tracer.
//
// Keep this as one behavior: an authenticated client requests a Biometric Capture
// upload URL, PUTs one JPEG to that exact URL, confirms the returned Upload Intent,
// and observes the complete accepted outcome through public HTTP and durable data.
func TestBiometricCaptureUploadPutAndConfirmOverHTTPWithPostgreSQLAndMinIO(t *testing.T) {
	// ARRANGE 1 — fixed facts and disposable PostgreSQL + MinIO
	now := time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	tokens := session.NewProductionCryptoTokens()
	rawToken := "biometric-http-postgres-minio-token"
	sessionID := uuid.MustParse("4555c78f-0861-4bff-86b7-e4e1379b628c")
	sessionCreatedAt := now.Add(-10 * time.Minute)
	sessionExpiresAt := now.Add(30 * time.Minute)
	identityIntentID := uuid.MustParse("5f8ed48e-b89e-4054-aaf0-68cab01906af")
	identityConfirmedAt := now.Add(-5 * time.Minute)
	identityArtifactID := uuid.MustParse("19b67b49-c766-4074-a2b0-42c46fa6cd86")
	identityKey := "verification-sessions/" + sessionID.String() +
		"/identity_document/" + identityIntentID.String()
	identityIntentExpiresAt := now.Add(20 * time.Minute)

	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	// Seed a semantically complete identity_document_uploaded history:
	// immutable Personal Details plus submit_personal_details, a confirmed Identity
	// Document Upload Intent, its accepted Verification Artifact, and the accepted
	// confirm_identity_document event. Keep all session/kind/key ownership exact.
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id,
			resume_token_hash,
			status,
			created_at,
			updated_at,
			expires_at
		)
		VALUES ($1, $2, 'identity_document_uploaded', $3, $4, $5)
	`,
		sessionID,
		tokens.Hash(rawToken),
		sessionCreatedAt,
		identityConfirmedAt,
		sessionExpiresAt,
	); err != nil {
		t.Fatalf("insert Verification Session: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO personal_details (
			verification_session_id,
			full_name,
			date_of_birth,
			identity_number,
			address,
			created_at
		)
		VALUES ($1, 'Checkpoint Three', DATE '2000-01-01', '3173000000000008', 'jalan boulevard', $2)
	`, sessionID, sessionCreatedAt); err != nil {
		t.Fatalf("insert immutable Personal Details prerequisite: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_personal_details', $2)
	`, sessionID, sessionCreatedAt); err != nil {
		t.Fatalf("insert Personal Details Session Event prerequisite: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			status,
			created_at,
			latest_status_change_at,
			expires_at,
			confirmed_at
		)
		VALUES ($1, $2, 'identity_document', $3, 'confirmed', $4, $5, $6, $5)
	`,
		identityIntentID,
		sessionID,
		identityKey,
		sessionCreatedAt,
		identityConfirmedAt,
		identityIntentExpiresAt,
	); err != nil {
		t.Fatalf("insert confirmed Identity Document Upload Intent: %v", err)
	}

	const identityContentType = "image/jpeg"
	const identitySizeBytes int64 = 2048
	const identityETag = "identity-document-etag"
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_artifacts (
			id,
			upload_intent_id,
			verification_session_id,
			kind,
			storage_key,
			content_type,
			size_bytes,
			etag,
			created_at
		)
		VALUES ($1, $2, $3, 'identity_document', $4, $5, $6, $7, $8)
	`,
		identityArtifactID,
		identityIntentID,
		sessionID,
		identityKey,
		identityContentType,
		identitySizeBytes,
		identityETag,
		identityConfirmedAt,
	); err != nil {
		t.Fatalf("insert accepted Identity Document Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_identity_document', '{"outcome":"accepted"}', $2)
	`, sessionID, identityConfirmedAt); err != nil {
		t.Fatalf("insert accepted Identity Document Session Event: %v", err)
	}

	realObjectStorage := newMinIOTestStorage(t, ctx)
	objectStorage := &countingHTTPObjectStorage{delegate: realObjectStorage}
	extractor := &biometricHTTPFailFastExtractor{t: t}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	uploadIntents := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		objectStorage,
		tokens,
		fixedClock{now: now},
	)
	confirmCoordinator := postgresadapter.NewArtifactConfirmCoordinator(
		newArtifactConfirmAcquireFunc(pool),
	)
	artifacts := artifact.NewService(
		postgresArtifacts,
		confirmCoordinator,
		objectStorage,
		extractor,
		tokens,
		fixedClock{now: now},
	)
	handler := httpapi.NewHandler(nil, nil, artifacts, uploadIntents, nil, nil)

	// ACT + ASSERT 1 — public upload-url contract
	// Send an authenticated POST through handler with exact JSON
	// {"kind":"biometric_capture"}. Require status 201 and reject response fields
	// other than uploadIntentId and uploadUrl. Do not derive or guess the new storage
	// key in order to perform the upload.
	uploadRequest := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+sessionID.String()+"/artifacts/upload-url",
		strings.NewReader(`{"kind":"biometric_capture"}`),
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

	var uploadResponseFields map[string]json.RawMessage
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &uploadResponseFields); err != nil {
		t.Fatalf("decode MinIO upload-url response fields: %v", err)
	}

	_, hasUploadIntentID := uploadResponseFields["uploadIntentId"]
	_, hasUploadURL := uploadResponseFields["uploadUrl"]

	if len(uploadResponseFields) != 2 || !hasUploadIntentID || !hasUploadURL {
		t.Fatal("upload-url response fields want exactly 2 fields, uploadIntentId and uploadUrl")
	}

	var uploadBody struct {
		UploadIntentID uuid.UUID `json:"uploadIntentId"`
		UploadURL      string    `json:"uploadUrl"`
	}
	if err := json.Unmarshal(uploadResponse.Body.Bytes(), &uploadBody); err != nil {
		t.Fatalf("decode MinIO upload-url response: %v", err)
	}
	if uploadBody.UploadIntentID == uuid.Nil {
		t.Fatalf("uploadIntentId is nil")
	}
	if uploadBody.UploadURL == "" {
		t.Fatalf("uploadUrl is empty")
	}

	// ACT 2 — direct client upload
	// PUT a non-zero JPEG to the exact returned uploadUrl with
	// Content-Type image/jpeg. Use putObjectToURL; do not add credentials, call the
	// AWS SDK PutObject operation, or rewrite the signed host.
	uploadedJPEG := smallJPEG(t)
	putObjectToURL(
		t,
		ctx,
		uploadBody.UploadURL,
		"image/jpeg",
		uploadedJPEG,
	)

	// ACT + ASSERT 3 — public confirm contract
	// Send an authenticated confirm POST through handler using the exact
	// returned uploadIntentId. Require status 200 and an exact current-session summary
	// whose state is biometric_capture_uploaded.
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

	var confirmResponseFields map[string]json.RawMessage
	if err := json.Unmarshal(confirmResponse.Body.Bytes(), &confirmResponseFields); err != nil {
		t.Fatalf("decode MinIO upload-url response fields: %v", err)
	}

	_, hasID := confirmResponseFields["id"]
	_, hasStatus := confirmResponseFields["status"]
	_, hasExpiresAt := confirmResponseFields["expiresAt"]

	if len(confirmResponseFields) != 3 || !hasID || !hasStatus || !hasExpiresAt {
		t.Fatal("upload-url response fields, want exactly 3 fields, id, status and expiresAt")
	}

	var confirmResponseBody struct {
		ID        string         `json:"id"`
		Status    session.Status `json:"status"`
		ExpiresAt string         `json:"expiresAt"`
	}
	if err := json.Unmarshal(confirmResponse.Body.Bytes(), &confirmResponseBody); err != nil {
		t.Fatalf("decode confirm artifacts: %v", err)
	}
	if confirmResponseBody.Status != session.StatusBiometricCaptureUploaded {
		t.Errorf("status got %q, want %q", confirmResponseBody.Status.String(), session.StatusBiometricCaptureUploaded.String())
	}
	if confirmResponseBody.ID != sessionID.String() {
		t.Errorf("sessionID from confirm got %q, want %q", confirmResponseBody.ID, sessionID.String())
	}
	if confirmResponseBody.ExpiresAt != sessionExpiresAt.UTC().Format(time.RFC3339) {
		t.Errorf("expiresAt from response got %v, want %v", confirmResponseBody.ExpiresAt, sessionExpiresAt.UTC().Format(time.RFC3339))
	}

	// ASSERT 4 — durable accepted outcome
	// Query PostgreSQL for exactly one confirmed Biometric Capture Upload
	// Intent, one accepted Biometric Capture Verification Artifact, and one accepted
	// confirm_biometric_capture event. Require the pre-existing Identity Document
	// artifact to remain present.
	var sessionStatus string
	var intentStatus string
	var biometricArtifactKind string
	var biometricArtifactKey string
	var biometricArtifactContentType string
	var biometricArtifactSizeBytes int64
	var biometricArtifactETag string
	var artifactCount int
	var identityArtifactCount int
	var eventCount int
	var rawEventMetadata []byte
	if err := database.QueryRow(ctx, `
		SELECT
			vs.status,
			ui.status,
			va.kind,
			va.storage_key,
			va.content_type,
			va.size_bytes,
			va.etag,
			(SELECT count(*) FROM verification_artifacts va WHERE va.upload_intent_id = ui.id),
			(SELECT count(*)
			 FROM verification_artifacts identity_va
			 WHERE identity_va.id = $3
			   AND identity_va.upload_intent_id = $4
			   AND identity_va.verification_session_id = vs.id
			   AND identity_va.kind = 'identity_document'
			   AND identity_va.storage_key = $5),
			(SELECT count(*) FROM session_events se WHERE vs.id = se.session_id AND se.event_type = 'confirm_biometric_capture'),
			(SELECT metadata FROM session_events se WHERE vs.id = se.session_id AND se.event_type = 'confirm_biometric_capture' LIMIT 1)
		FROM verification_sessions vs
		JOIN upload_intents ui ON ui.verification_session_id = vs.id
		JOIN verification_artifacts va ON va.upload_intent_id = ui.id
		WHERE vs.id = $1 AND ui.id = $2
	`, sessionID, uploadBody.UploadIntentID, identityArtifactID, identityIntentID, identityKey).Scan(
		&sessionStatus,
		&intentStatus,
		&biometricArtifactKind,
		&biometricArtifactKey,
		&biometricArtifactContentType,
		&biometricArtifactSizeBytes,
		&biometricArtifactETag,
		&artifactCount,
		&identityArtifactCount,
		&eventCount,
		&rawEventMetadata,
	); err != nil {
		t.Fatalf("read MinIO HTTP tracer durable outcome: %v", err)
	}
	if sessionStatus != session.StatusBiometricCaptureUploaded.String() ||
		intentStatus != "confirmed" || artifactCount != 1 ||
		identityArtifactCount != 1 || eventCount != 1 {
		t.Errorf(
			"durable outcome = session:%s intent:%s biometric artifacts:%d identity artifacts:%d events:%d",
			sessionStatus,
			intentStatus,
			artifactCount,
			identityArtifactCount,
			eventCount,
		)
	}

	expectedBiometricKey := "verification-sessions/" + sessionID.String() +
		"/biometric_capture/" + uploadBody.UploadIntentID.String()
	headObjectMetadata := objectStorage.headObjectMetadata()
	if headObjectMetadata.ContentType != "image/jpeg" ||
		headObjectMetadata.SizeBytes != int64(len(uploadedJPEG)) ||
		headObjectMetadata.ETag == "" {
		t.Errorf(
			"real HeadObject metadata = content-type:%q size:%d etag:%q, want image/jpeg/%d/non-empty",
			headObjectMetadata.ContentType,
			headObjectMetadata.SizeBytes,
			headObjectMetadata.ETag,
			len(uploadedJPEG),
		)
	}
	if biometricArtifactKind != "biometric_capture" ||
		biometricArtifactContentType != headObjectMetadata.ContentType ||
		biometricArtifactSizeBytes != headObjectMetadata.SizeBytes ||
		biometricArtifactETag != headObjectMetadata.ETag {
		t.Errorf(
			"stored biometric artifact = kind:%q content-type:%q size:%d etag:%q",
			biometricArtifactKind,
			biometricArtifactContentType,
			biometricArtifactSizeBytes,
			biometricArtifactETag,
		)
	}

	if biometricArtifactKey != expectedBiometricKey {
		t.Errorf("wrong stored storage key")
	}

	eventMetadata, err := sessionevent.ParseMetadata(rawEventMetadata)
	if err != nil {
		t.Fatalf("parse confirm_biometric_capture event metadata: %v", err)
	}
	if eventMetadata.Outcome() != sessionevent.OutcomeAccepted {
		t.Errorf(
			"confirm_biometric_capture outcome = %s, want %s",
			eventMetadata.Outcome(),
			sessionevent.OutcomeAccepted,
		)
	}

	// ASSERT 5 — readiness and external boundaries
	// Call HasRequiredAcceptedArtifacts for sessionID and require true.
	// Require exactly one real HeadObject call and zero Extract calls.
	hasRequiredArtifact, err := postgresArtifacts.HasRequiredAcceptedArtifacts(ctx, sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts got error: %v", err)
	}
	if !hasRequiredArtifact {
		t.Error("HasRequiredAcceptedArtifacts got false, want true")
	}

	if objectStorage.headObjectCallCount() != 1 {
		t.Errorf("head object call count got %d, want 1", objectStorage.headObjectCallCount())
	}

	if extractor.callCount() != 0 {
		t.Errorf("extractor call count got %d, want 0", extractor.callCount())
	}
}

type biometricHTTPFailFastExtractor struct {
	t     *testing.T
	calls int
}

func (e *biometricHTTPFailFastExtractor) Extract(
	_ context.Context,
	storageKey string,
) (artifact.DocumentExtraction, error) {
	e.calls++
	e.t.Fatal("Extract() should not be called for Biometric Capture key")
	return artifact.DocumentExtraction{}, nil
}

func (e *biometricHTTPFailFastExtractor) callCount() int {
	return e.calls
}
