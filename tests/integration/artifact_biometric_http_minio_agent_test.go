package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	s3storageadapter "github.com/santosidauruk/lawang-go/internal/adapter/s3storage"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestBiometricPNGConfirmOverHTTPWithPostgreSQLAndMinIOChangesReadiness(t *testing.T) {
	harness := newBiometricHTTPMinIOAgentHarness(t)
	fixture := seedBiometricPostgresConfirmationState(
		t,
		harness.ctx,
		harness.database,
		harness.now,
	)

	readyBefore, err := harness.postgresArtifacts.HasRequiredAcceptedArtifacts(
		harness.ctx,
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("read readiness before Biometric Capture confirm: %v", err)
	}
	if readyBefore {
		t.Fatal("readiness before Biometric Capture confirm = true, want false")
	}

	png := smallPNG(t)
	putMinIOObject(
		t,
		harness.ctx,
		harness.realObjectStorage,
		fixture.biometricKey,
		"image/png",
		png,
	)
	handler := harness.confirmHandler(t, harness.objectStorage)
	response := performBiometricAgentConfirm(t, handler, fixture)

	if response.Code != http.StatusOK {
		t.Fatalf("PNG confirm status = %d, want %d", response.Code, http.StatusOK)
	}
	var responseBody struct {
		ID        string         `json:"id"`
		Status    session.Status `json:"status"`
		ExpiresAt string         `json:"expiresAt"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &responseBody); err != nil {
		t.Fatalf("decode PNG confirm response: %v", err)
	}
	if responseBody.ID != fixture.sessionID.String() ||
		responseBody.Status != session.StatusBiometricCaptureUploaded ||
		responseBody.ExpiresAt != fixture.sessionExpiresAt.UTC().Format(time.RFC3339) {
		t.Errorf(
			"PNG confirm summary = id:%q status:%s expires:%q, want expected session summary",
			responseBody.ID,
			responseBody.Status,
			responseBody.ExpiresAt,
		)
	}

	readyAfter, err := harness.postgresArtifacts.HasRequiredAcceptedArtifacts(
		harness.ctx,
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("read readiness after Biometric Capture confirm: %v", err)
	}
	if !readyAfter {
		t.Fatal("readiness after Biometric Capture confirm = false, want true")
	}

	var contentType string
	var sizeBytes int64
	var etag string
	if err := harness.database.QueryRow(harness.ctx, `
		SELECT content_type, size_bytes, etag
		FROM verification_artifacts
		WHERE upload_intent_id = $1
	`, fixture.biometricIntentID).Scan(&contentType, &sizeBytes, &etag); err != nil {
		t.Fatalf("read accepted PNG Verification Artifact: %v", err)
	}
	metadata := harness.objectStorage.headObjectMetadata()
	if contentType != "image/png" ||
		sizeBytes != int64(len(png)) ||
		etag == "" ||
		contentType != metadata.ContentType ||
		sizeBytes != metadata.SizeBytes ||
		etag != metadata.ETag {
		t.Errorf(
			"stored PNG metadata = content-type:%q size:%d etag-present:%t, want exact real HeadObject metadata",
			contentType,
			sizeBytes,
			etag != "",
		)
	}
}

func TestBiometricHTTPMinIORejectsInvalidMetadataWithoutDurableOutcome(t *testing.T) {
	harness := newBiometricHTTPMinIOAgentHarness(t)

	tests := []struct {
		name        string
		contentType string
		body        func(*testing.T) []byte
		wantReason  artifact.FailureReason
	}{
		{
			name:        "PDF",
			contentType: "application/pdf",
			body:        func(*testing.T) []byte { return smallPDF() },
			wantReason:  artifact.ReasonUnsupportedContentType,
		},
		{
			name:        "empty object",
			contentType: "image/jpeg",
			body:        func(*testing.T) []byte { return nil },
			wantReason:  artifact.ReasonObjectEmpty,
		},
		{
			name:        "object above five MiB",
			contentType: "image/jpeg",
			body: func(*testing.T) []byte {
				return make([]byte, 5*1024*1024+1)
			},
			wantReason: artifact.ReasonObjectTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedBiometricPostgresConfirmationState(
				t,
				harness.ctx,
				harness.database,
				harness.now,
			)
			putMinIOObject(
				t,
				harness.ctx,
				harness.realObjectStorage,
				fixture.biometricKey,
				tt.contentType,
				tt.body(t),
			)

			callsBefore := harness.objectStorage.headObjectCallCount()
			handler := harness.confirmHandler(t, harness.objectStorage)
			response := performBiometricAgentConfirm(t, handler, fixture)

			assertBiometricAgentError(
				t,
				response,
				http.StatusUnprocessableEntity,
				string(artifact.CodeInvalidObjectMetadata),
				"uploaded object metadata is invalid",
				tt.wantReason,
			)
			if got := harness.objectStorage.headObjectCallCount() - callsBefore; got != 1 {
				t.Errorf("HeadObject() calls = %d, want 1", got)
			}
			assertBiometricAgentNoDurableOutcome(
				t,
				harness,
				fixture,
				session.StatusIdentityDocumentUploaded.String(),
				"pending",
			)
		})
	}
}

func TestBiometricHTTPRejectsInvalidIntentStateBeforeObjectStorage(t *testing.T) {
	harness := newBiometricHTTPMinIOAgentHarness(t)

	tests := []struct {
		name              string
		mutate            func(*testing.T, biometricPostgresFixture)
		wantCode          artifact.ErrorCode
		wantMessage       string
		wantSessionStatus string
		wantIntentStatus  string
	}{
		{
			name: "wrong session state",
			mutate: func(t *testing.T, fixture biometricPostgresFixture) {
				if _, err := harness.database.Exec(harness.ctx, `
					UPDATE verification_sessions
					SET status = 'personal_details_submitted'
					WHERE id = $1
				`, fixture.sessionID); err != nil {
					t.Fatalf("set wrong Biometric Capture session state: %v", err)
				}
			},
			wantCode:          artifact.CodeConfirmationStale,
			wantMessage:       "artifact confirmation state changed; retry the request",
			wantSessionStatus: session.StatusPersonalDetailsSubmitted.String(),
			wantIntentStatus:  "pending",
		},
		{
			name: "expired intent",
			mutate: func(t *testing.T, fixture biometricPostgresFixture) {
				if _, err := harness.database.Exec(harness.ctx, `
					UPDATE upload_intents
					SET expires_at = $2
					WHERE id = $1
				`, fixture.biometricIntentID, harness.now); err != nil {
					t.Fatalf("expire Biometric Capture Upload Intent: %v", err)
				}
			},
			wantCode:          artifact.CodeUploadIntentExpired,
			wantMessage:       "upload intent expired",
			wantSessionStatus: session.StatusIdentityDocumentUploaded.String(),
			wantIntentStatus:  "pending",
		},
		{
			name: "superseded intent",
			mutate: func(t *testing.T, fixture biometricPostgresFixture) {
				if _, err := harness.database.Exec(harness.ctx, `
					UPDATE upload_intents
					SET status = 'superseded', latest_status_change_at = $2
					WHERE id = $1
				`, fixture.biometricIntentID, harness.now); err != nil {
					t.Fatalf("supersede Biometric Capture Upload Intent: %v", err)
				}
			},
			wantCode:          artifact.CodeUploadIntentSuperseded,
			wantMessage:       "upload intent was superseded",
			wantSessionStatus: session.StatusIdentityDocumentUploaded.String(),
			wantIntentStatus:  "superseded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedBiometricPostgresConfirmationState(
				t,
				harness.ctx,
				harness.database,
				harness.now,
			)
			tt.mutate(t, fixture)

			callsBefore := harness.objectStorage.headObjectCallCount()
			handler := harness.confirmHandler(t, harness.objectStorage)
			response := performBiometricAgentConfirm(t, handler, fixture)

			assertBiometricAgentError(
				t,
				response,
				http.StatusConflict,
				string(tt.wantCode),
				tt.wantMessage,
				"",
			)
			if got := harness.objectStorage.headObjectCallCount() - callsBefore; got != 0 {
				t.Errorf("HeadObject() calls = %d, want 0", got)
			}
			assertBiometricAgentNoDurableOutcome(
				t,
				harness,
				fixture,
				tt.wantSessionStatus,
				tt.wantIntentStatus,
			)
		})
	}
}

func TestBiometricHTTPBoundsObjectStorageFailures(t *testing.T) {
	harness := newBiometricHTTPMinIOAgentHarness(t)

	tests := []struct {
		name    string
		storage artifact.ObjectStorage
	}{
		{name: "missing MinIO object", storage: harness.objectStorage},
		{name: "unavailable S3 endpoint", storage: newUnavailableS3TestStorage(t, harness.ctx)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := seedBiometricPostgresConfirmationState(
				t,
				harness.ctx,
				harness.database,
				harness.now,
			)
			handler := harness.confirmHandler(t, tt.storage)
			response := performBiometricAgentConfirm(t, handler, fixture)

			assertBiometricAgentError(
				t,
				response,
				http.StatusServiceUnavailable,
				string(artifact.CodeObjectStorageFailed),
				"object storage is temporarily unavailable",
				"",
			)
			assertBiometricAgentNoDurableOutcome(
				t,
				harness,
				fixture,
				session.StatusIdentityDocumentUploaded.String(),
				"pending",
			)
		})
	}
}

type biometricHTTPMinIOAgentHarness struct {
	ctx               context.Context
	database          *pgx.Conn
	pool              *pgxpool.Pool
	now               time.Time
	realObjectStorage *s3storageadapter.Adapter
	objectStorage     *countingHTTPObjectStorage
	postgresArtifacts *postgresadapter.ArtifactTransactions
}

func newBiometricHTTPMinIOAgentHarness(t *testing.T) biometricHTTPMinIOAgentHarness {
	t.Helper()

	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	realObjectStorage := newMinIOTestStorage(t, ctx)
	return biometricHTTPMinIOAgentHarness{
		ctx:               ctx,
		database:          database,
		pool:              pool,
		now:               now,
		realObjectStorage: realObjectStorage,
		objectStorage:     &countingHTTPObjectStorage{delegate: realObjectStorage},
		postgresArtifacts: postgresadapter.NewArtifactTransactions(database),
	}
}

func (h biometricHTTPMinIOAgentHarness) confirmHandler(
	t *testing.T,
	storage artifact.ObjectStorage,
) http.Handler {
	t.Helper()

	service := artifact.NewService(
		h.postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(h.pool)),
		storage,
		&biometricAgentFailFastExtractor{t: t},
		session.NewProductionCryptoTokens(),
		fixedClock{now: h.now},
	)
	return httpapi.NewHandler(nil, nil, service, nil, nil, nil)
}

func performBiometricAgentConfirm(
	t *testing.T,
	handler http.Handler,
	fixture biometricPostgresFixture,
) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(
		http.MethodPost,
		"/verification-sessions/"+fixture.sessionID.String()+"/artifacts/confirm",
		strings.NewReader(
			`{"uploadIntentId":"`+fixture.biometricIntentID.String()+`"}`,
		),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.rawToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertBiometricAgentError(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantStatus int,
	wantCode string,
	wantMessage string,
	wantReason artifact.FailureReason,
) {
	t.Helper()

	if response.Code != wantStatus {
		t.Fatalf("HTTP status = %d, want %d", response.Code, wantStatus)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decode bounded error fields: %v", err)
	}
	wantFieldCount := 2
	if wantReason != "" {
		wantFieldCount = 3
	}
	if len(fields) != wantFieldCount || fields["code"] == nil || fields["message"] == nil {
		t.Fatalf("bounded error has unexpected field names")
	}
	if wantReason != "" && fields["details"] == nil {
		t.Fatal("bounded metadata error is missing details")
	}

	var apiError httpapi.APIError
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatalf("decode bounded error response: %v", err)
	}
	if apiError.Code != wantCode || apiError.Message != wantMessage {
		t.Errorf(
			"bounded error = code:%q message:%q, want %q/%q",
			apiError.Code,
			apiError.Message,
			wantCode,
			wantMessage,
		)
	}
	if wantReason != "" {
		if reason, ok := apiError.Details["reason"].(string); !ok || reason != string(wantReason) {
			t.Errorf("bounded failure reason = %q, want %q", reason, wantReason)
		}
	} else if apiError.Details != nil {
		t.Errorf("bounded error details = %#v, want nil", apiError.Details)
	}
}

func assertBiometricAgentNoDurableOutcome(
	t *testing.T,
	harness biometricHTTPMinIOAgentHarness,
	fixture biometricPostgresFixture,
	wantSessionStatus string,
	wantIntentStatus string,
) {
	t.Helper()

	var sessionStatus string
	var intentStatus string
	var biometricArtifactCount int
	var biometricEventCount int
	if err := harness.database.QueryRow(harness.ctx, `
		SELECT
			vs.status,
			ui.status,
			(SELECT count(*) FROM verification_artifacts va
			 WHERE va.verification_session_id = vs.id AND va.kind = 'biometric_capture'),
			(SELECT count(*) FROM session_events se
			 WHERE se.session_id = vs.id AND se.event_type = 'confirm_biometric_capture')
		FROM verification_sessions vs
		JOIN upload_intents ui ON ui.verification_session_id = vs.id
		WHERE vs.id = $1 AND ui.id = $2
	`, fixture.sessionID, fixture.biometricIntentID).Scan(
		&sessionStatus,
		&intentStatus,
		&biometricArtifactCount,
		&biometricEventCount,
	); err != nil {
		t.Fatalf("read rejected Biometric Capture outcome: %v", err)
	}
	if sessionStatus != wantSessionStatus ||
		intentStatus != wantIntentStatus ||
		biometricArtifactCount != 0 ||
		biometricEventCount != 0 {
		t.Errorf(
			"rejected outcome = session:%q intent:%q artifacts:%d events:%d, want %q/%q/0/0",
			sessionStatus,
			intentStatus,
			biometricArtifactCount,
			biometricEventCount,
			wantSessionStatus,
			wantIntentStatus,
		)
	}

	ready, err := harness.postgresArtifacts.HasRequiredAcceptedArtifacts(
		harness.ctx,
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("read readiness after rejected confirm: %v", err)
	}
	if ready {
		t.Fatal("readiness after rejected confirm = true, want false")
	}
}

type biometricAgentFailFastExtractor struct {
	t *testing.T
}

func (e *biometricAgentFailFastExtractor) Extract(
	context.Context,
	string,
) (artifact.DocumentExtraction, error) {
	e.t.Fatal("Extract() called for Biometric Capture")
	return artifact.DocumentExtraction{}, nil
}
