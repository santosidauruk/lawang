package artifact_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/personaldetails"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/domain/sessionevent"
	"github.com/santosidauruk/lawang-go/internal/domain/verificationsession"
)

type memoryState struct {
	session      session.VerificationSession
	details      *personaldetails.PersonalDetails
	uploadIntent artifact.UploadIntent
	artifacts    []artifact.VerificationArtifact
	events       []session.AppendEventParams
}

type memoryTransactions struct {
	state                 memoryState
	loadUploadIntentCalls int
	loadDetailsCalls      int
	appendEventErr        error
	transactionActive     bool
}

type memoryTransaction struct {
	state          *memoryState
	appendEventErr error
}

type confirmFixture struct {
	now            time.Time
	sessionID      uuid.UUID
	uploadIntentID uuid.UUID
	identityNumber string
	transactions   *memoryTransactions
	storageCalls   int
	extractorCalls int
	storage        stubObjectStorage
	extractor      stubDocumentExtractor
	tokens         stubTokenIssuer
	coordinator    stubConfirmCoordinator
}

func newConfirmFixture() *confirmFixture {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("38807233-96a4-4ce2-81a3-48e23e3d3e6b")
	uploadIntentID := uuid.MustParse("b902e282-229d-4a9b-8898-b8cb2082c399")
	identityNumber := "3173000000000001"
	f := &confirmFixture{
		now:            now,
		sessionID:      sessionID,
		uploadIntentID: uploadIntentID,
		identityNumber: identityNumber,
		transactions: newMemoryTransactions(
			session.VerificationSession{
				ID:              sessionID,
				Status:          session.StatusPersonalDetailsSubmitted,
				ResumeTokenHash: []byte("stored-hash"),
				ExpiresAt:       now.Add(time.Minute),
			},
			personaldetails.PersonalDetails{
				SessionID: sessionID,
				Input: personaldetails.Input{
					IdentityNumber: identityNumber,
				},
			},
			artifact.UploadIntent{
				ID:                    uploadIntentID,
				VerificationSessionID: sessionID,
				Kind:                  "identity_document",
				StorageKey:            "identity-document-key",
				Status:                "pending",
				ExpiresAt:             now.Add(5 * time.Minute),
			},
		),
		tokens: stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
	}
	f.storage = stubObjectStorage{
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "identity-document-etag",
		},
		calls: &f.storageCalls,
	}
	f.extractor = stubDocumentExtractor{
		extraction: artifact.DocumentExtraction{IdentityNumber: identityNumber},
		calls:      &f.extractorCalls,
	}
	f.coordinator = stubConfirmCoordinator{
		reader:     f.transactions,
		transactor: f.transactions,
	}
	return f
}

func (f *confirmFixture) service() *artifact.Service {
	return artifact.NewService(
		f.transactions,
		f.coordinator,
		f.storage,
		f.extractor,
		f.tokens,
		fixedClock{now: f.now},
	)
}

func (f *confirmFixture) confirm() (session.Summary, error) {
	return f.service().Confirm(
		context.Background(),
		f.sessionID,
		"raw-resume-token",
		f.uploadIntentID,
	)
}

func requireArtifactError(t *testing.T, err error, code artifact.ErrorCode, reason artifact.FailureReason) {
	t.Helper()
	var serviceError *artifact.Error
	if !errors.As(err, &serviceError) || serviceError.Code != code || serviceError.Reason != reason {
		t.Fatalf("error = %#v, want code %s and reason %s", err, code, reason)
	}
}

func newMemoryTransactions(storedSession session.VerificationSession, storedDetails personaldetails.PersonalDetails, storedUploadIntent artifact.UploadIntent) *memoryTransactions {
	return &memoryTransactions{
		state: memoryState{
			session:      storedSession,
			details:      &storedDetails,
			uploadIntent: storedUploadIntent,
			artifacts:    nil,
			events:       nil,
		},
	}
}

// This was the Checkpoint 2 tracer bullet. Its sibling application behaviors were
// added only after this public success path became GREEN. PostgreSQL, MinIO, HTTP,
// replay, and concurrency remain separate checkpoints.
func TestConfirmIdentityDocumentAcceptsValidatedUploadAtomically(t *testing.T) {

	// ARRANGE 1 — fixed facts
	// Define a fixed clock, session UUID, Upload Intent UUID, and raw resume token.
	// Start the Verification Session in personal_details_submitted with a future
	// session expiry. Start the Identity Document Upload Intent in pending with a
	// future intent expiry and a complete storage key.
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	id := uuid.MustParse("c6ef7d1c-f625-45e0-81ea-b50d3a3bd91b")
	uploadIntentID := uuid.MustParse("5f08096e-cc4d-49e8-b657-c42c04e61c91")
	storageKey := "storage-key"
	identityNumber := "81eab50d3a3bd91c"
	rawToken := "raw-resume-token"

	transactions := newMemoryTransactions(session.VerificationSession{
		ID:              id,
		Status:          session.StatusPersonalDetailsSubmitted,
		ResumeTokenHash: []byte("stored-hash"), ExpiresAt: now.Add(time.Minute),
	},
		personaldetails.PersonalDetails{
			SessionID: id,
			Input: personaldetails.Input{
				IdentityNumber: identityNumber,
			},
		},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: id,
			Status:                "pending",
			Kind:                  "identity_document",
			StorageKey:            storageKey,
			CreatedAt:             now,
			LatestStatusChangeAt:  now,
			ExpiresAt:             now.Add(5 * time.Minute),
		})

	// ARRANGE 2 — committed application state
	// Build one small in-memory transactor containing:
	//   - the Verification Session and its stored resume-token hash;
	//   - immutable Personal Details with one identity number;
	//   - the pending Upload Intent;
	//   - empty Verification Artifact and Session Event collections.
	// Its transaction callback must copy state and publish the copy only when the
	// callback returns nil, like the Issue 006 memory transactor.

	// ARRANGE 3 — external boundaries
	// Make ObjectStorage metadata describe an existing JPEG object with non-zero size,
	// no more than 10 MiB, and a stable ETag. Make DocumentExtractor return the same
	// identity number as immutable Personal Details. These are boundary fakes, not
	// assertions about internal method-call order.

	objectStorage := stubObjectStorage{
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "identity-document-etag",
		},
	}

	extractor := stubDocumentExtractor{
		extraction: artifact.DocumentExtraction{
			IdentityNumber: identityNumber,
		},
	}
	// ARRANGE 4 — public application service
	// Construct artifact.Service from only the dependencies consumed by confirmation.
	// The intended public call is:
	//
	//   got, err := service.Confirm(ctx, sessionID, rawToken, uploadIntentID)
	//
	// and its successful result is session.Summary. Let this test reveal the smallest
	// useful Transaction, ObjectStorage, and DocumentExtractor interfaces; do not add
	// methods for later failure/replay scenarios yet.
	tokens := stubTokenIssuer{
		hash:  []byte("stored-hash"),
		equal: true,
	}

	coordinator := stubConfirmCoordinator{
		reader:     transactions,
		transactor: transactions,
	}

	service := artifact.NewService(
		transactions,
		coordinator,
		objectStorage,
		extractor,
		tokens,
		fixedClock{now: now},
	)
	// ACT
	// Call Confirm exactly once through that public method.

	got, err := service.Confirm(
		context.Background(),
		id,
		rawToken,
		uploadIntentID,
	)

	if err != nil {
		t.Fatalf("Confirm() error = %#v", err)
	}

	if got.ID != id ||
		got.Status != session.StatusIdentityDocumentUploaded ||
		!got.ExpiresAt.Equal(now.Add(time.Minute)) {
		t.Errorf("Confirm() summary = %#v, want confirmed identity document %s", got, id)
	}

	storedIntent := transactions.state.uploadIntent
	if storedIntent.Status != "confirmed" ||
		storedIntent.ConfirmedAt == nil ||
		!storedIntent.ConfirmedAt.Equal(now) ||
		!storedIntent.LatestStatusChangeAt.Equal(now) ||
		storedIntent.FailureCode != nil ||
		storedIntent.ObjectDeletedAt != nil {
		t.Errorf("stored upload intent = %#v", storedIntent)
	}

	if !storedIntent.ExpiresAt.Equal(now.Add(5 * time.Minute)) {
		t.Errorf(
			"stored Upload Intent expiry = %s, want unchanged %s",
			storedIntent.ExpiresAt,
			now.Add(5*time.Minute),
		)
	}

	if len(transactions.state.artifacts) != 1 {
		t.Fatalf("stored artifacts summary = %d, want exactly 1", len(transactions.state.artifacts))
	}

	storedArtifact := transactions.state.artifacts[0]
	if storedArtifact.ID == uuid.Nil ||
		storedArtifact.VerificationSessionID != id ||
		storedArtifact.UploadIntentID != uploadIntentID ||
		storedArtifact.Kind != "identity_document" ||
		storedArtifact.StorageKey != storageKey ||
		storedArtifact.ContentType != "image/jpeg" ||
		storedArtifact.SizeBytes != 1024 ||
		storedArtifact.ETag != "identity-document-etag" ||
		!storedArtifact.CreatedAt.Equal(now) {
		t.Errorf("stored artifact = %#v, want saved 1 accepted Identity Document", storedArtifact)
	}

	if transactions.state.session.Status !=
		session.StatusIdentityDocumentUploaded {
		t.Errorf(
			"stored session status = %q, want %q",
			transactions.state.session.Status,
			session.StatusIdentityDocumentUploaded,
		)
	}

	if !transactions.state.session.UpdatedAt.Equal(now) {
		t.Errorf(
			"stored session updatedAt = %s, want %s",
			transactions.state.session.UpdatedAt,
			now,
		)
	}

	if len(transactions.state.events) != 1 {
		t.Fatalf("stored events = %d, want exactly 1", len(transactions.state.events))
	}

	event := transactions.state.events[0]
	if event.SessionID != id ||
		event.Type != sessionevent.ConfirmIdentityDocument ||
		event.Metadata.Outcome() != sessionevent.OutcomeAccepted ||
		!event.OccurredAt.Equal(now) {
		t.Fatalf("stored event = %#v, want safe confirm_identity_document with accepted metadata", event)
	}

	// ASSERT — one successful atomic behavior
	// Verify all caller/domain-visible effects of that one action:
	//   1. err is nil;
	//   2. the returned summary has the same session ID and expiry, with status
	//      identity_document_uploaded;
	//   3. the Upload Intent is confirmed at the fixed time;
	//   4. exactly one accepted Verification Artifact records the intent, session,
	//      identity_document kind, storage key, JPEG content type, size, and ETag;
	//   5. the session state advanced once to identity_document_uploaded; and
	//   6. exactly one confirm_identity_document Session Event exists at the fixed
	//      time with safe outcome metadata equal to accepted.
	//
	// Assert the committed in-memory state, not private helper calls. This group of
	// assertions is one logical behavior: successful confirmation commits its accepted
	// result atomically.
}

func TestConfirmIdentityDocumentRecordsMismatchAtomically(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	previousUpdatedAt := now.Add(-time.Minute)

	sessionID := uuid.MustParse("ee59ea2a-a3b0-4ef9-86e5-e796f86dbad4")
	uploadIntentID := uuid.MustParse("df53ff28-a833-42bb-848d-8f02f24784bb")

	storedIdentityNumber := "3173000000000001"
	extractedIdentityNumber := "3173000000000099"

	transactions := newMemoryTransactions(
		session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: []byte("stored-hash"),
			ExpiresAt:       now.Add(time.Minute),
			UpdatedAt:       previousUpdatedAt,
		},
		personaldetails.PersonalDetails{
			SessionID: sessionID,
			Input: personaldetails.Input{
				IdentityNumber: storedIdentityNumber,
			},
		},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "identity-document-key",
			Status:                "pending",
			CreatedAt:             now,
			LatestStatusChangeAt:  previousUpdatedAt,
			ExpiresAt:             now.Add(5 * time.Minute),
		},
	)

	objectStorage := stubObjectStorage{
		metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "identity-document-etag",
		},
	}

	extractor := stubDocumentExtractor{
		extraction: artifact.DocumentExtraction{
			IdentityNumber: extractedIdentityNumber,
		},
	}

	tokens := stubTokenIssuer{
		hash:  []byte("stored-hash"),
		equal: true,
	}

	coordinator := stubConfirmCoordinator{
		reader:     transactions,
		transactor: transactions,
	}

	service := artifact.NewService(
		transactions,
		coordinator,
		objectStorage,
		extractor,
		tokens,
		fixedClock{now: now},
	)

	_, err := service.Confirm(
		context.Background(),
		sessionID,
		"raw-resume-token",
		uploadIntentID,
	)

	if err == nil {
		t.Fatalf("Confirm() error = nil, want Local Validation Failure")
	}
	var serviceError *artifact.Error
	if !errors.As(err, &serviceError) ||
		serviceError.Code != artifact.CodeLocalValidationFailed ||
		serviceError.Reason != artifact.ReasonIdentityNumberMismatch {
		t.Fatalf(
			"Confirm() error = %#v, want %s with reason %s",
			err,
			artifact.CodeLocalValidationFailed,
			artifact.ReasonIdentityNumberMismatch,
		)
	}
	if got := err.Error(); strings.Contains(got, storedIdentityNumber) || strings.Contains(got, extractedIdentityNumber) {
		t.Fatalf("Confirm() error leaked an identity number: %q", got)
	}

	storedIntent := transactions.state.uploadIntent
	if storedIntent.Status != "validation_failed" {
		t.Errorf("stored Upload Intent status = %q, want validation_failed", storedIntent.Status)
	}

	if storedIntent.FailureCode == nil ||
		*storedIntent.FailureCode != "identity_number_mismatch" {
		t.Errorf("stored Upload Intent failure code = %v, want identity_number_mismatch", storedIntent.FailureCode)
	}

	if storedIntent.ConfirmedAt != nil {
		t.Errorf("store Upload Intent confirmedAt = %v, want nil", storedIntent.ConfirmedAt)
	}

	if !storedIntent.LatestStatusChangeAt.Equal(now) {
		t.Errorf(
			"latest status change = %s, want %s",
			storedIntent.LatestStatusChangeAt,
			now,
		)
	}

	if len(transactions.state.artifacts) != 0 {
		t.Errorf("stored artifacts = %d, want 0", len(transactions.state.artifacts))
	}

	storedSession := transactions.state.session

	if storedSession.Status != session.StatusPersonalDetailsSubmitted {
		t.Errorf(
			"stored session status = %q, want %q",
			storedSession.Status,
			session.StatusPersonalDetailsSubmitted,
		)
	}

	if !storedSession.UpdatedAt.Equal(previousUpdatedAt) {
		t.Errorf(
			"stored session updatedAt = %s, want unchanged %s",
			storedSession.UpdatedAt,
			previousUpdatedAt,
		)
	}

	if len(transactions.state.events) != 1 {
		t.Fatalf(
			"stored events = %d, want exactly 1",
			len(transactions.state.events),
		)
	}

	event := transactions.state.events[0]
	if event.SessionID != sessionID ||
		event.Type != sessionevent.ConfirmIdentityDocument ||
		event.Metadata.Outcome() != sessionevent.OutcomeLocalValidationFailed ||
		!event.OccurredAt.Equal(now) {
		t.Errorf("stored event = %#v, want safe local-validation-failed confirmation event", event)
	}
	rawMetadata, err := event.Metadata.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON() error = %#v", err)
	}
	if strings.Contains(string(rawMetadata), storedIdentityNumber) ||
		strings.Contains(string(rawMetadata), extractedIdentityNumber) {
		t.Fatalf("event metadata leaked identity data: %s", rawMetadata)
	}

}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenStorageKeyChanges(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	previousUpdatedAt := now.Add(-time.Minute)
	sessionID := uuid.MustParse("aa77cecb-e281-4d42-8960-fe9b68e7d976")
	uploadIntentID := uuid.MustParse("ccfa6ef3-2d45-4141-8b0f-caa51f6ae493")
	identityNumber := "3173000000000001"
	originalStorageKey := "identity-document-original"
	changedStorageKey := "identity-document-changed"
	transactions := newMemoryTransactions(
		session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: []byte("stored-hash"),
			ExpiresAt:       now.Add(time.Minute),
			UpdatedAt:       previousUpdatedAt,
		},
		personaldetails.PersonalDetails{
			SessionID: sessionID,
			Input: personaldetails.Input{
				IdentityNumber: identityNumber,
			},
		},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            originalStorageKey,
			Status:                "pending",
			CreatedAt:             now,
			LatestStatusChangeAt:  previousUpdatedAt,
			ExpiresAt:             now.Add(5 * time.Minute),
		},
	)

	service := artifact.NewService(
		transactions,
		stubConfirmCoordinator{
			reader:     transactions,
			transactor: transactions,
		},
		stubObjectStorage{metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "identity-document-etag",
		}},
		stubDocumentExtractor{
			extraction: artifact.DocumentExtraction{IdentityNumber: identityNumber},
			onExtract: func(storageKey string) {
				if storageKey != originalStorageKey {
					t.Errorf("Extract() storage key = %q, want %q", storageKey, originalStorageKey)
				}
				transactions.state.uploadIntent.StorageKey = changedStorageKey
			},
		},
		stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Confirm(
		context.Background(),
		sessionID,
		"raw-resume-token",
		uploadIntentID,
	)
	if err == nil {
		t.Fatal("Confirm() error = nil, want stale Upload Intent error")
	}
	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")

	storedIntent := transactions.state.uploadIntent
	if storedIntent.StorageKey != changedStorageKey ||
		storedIntent.Status != "pending" ||
		storedIntent.ConfirmedAt != nil ||
		storedIntent.FailureCode != nil ||
		!storedIntent.LatestStatusChangeAt.Equal(previousUpdatedAt) {
		t.Errorf("stored Upload Intent = %#v, want changed key with no confirmation effects", storedIntent)
	}
	if len(transactions.state.artifacts) != 0 {
		t.Errorf("stored artifacts = %d, want 0", len(transactions.state.artifacts))
	}
	if len(transactions.state.events) != 0 {
		t.Errorf("stored events = %d, want 0", len(transactions.state.events))
	}
	if transactions.state.session.Status != session.StatusPersonalDetailsSubmitted ||
		!transactions.state.session.UpdatedAt.Equal(previousUpdatedAt) {
		t.Errorf("stored session = %#v, want unchanged", transactions.state.session)
	}
}

func TestConfirmIdentityDocumentRejectsExpiredIntentBeforeExternalIO(t *testing.T) {
	f := newConfirmFixture()
	f.transactions.state.uploadIntent.ExpiresAt = f.now

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeUploadIntentExpired, "")
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsSupersededIntentBeforeExternalIO(t *testing.T) {
	f := newConfirmFixture()
	f.transactions.state.uploadIntent.Status = "superseded"

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeUploadIntentSuperseded, "")
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentReturnsBoundedErrorWhenIntentIsNotFound(t *testing.T) {
	f := newConfirmFixture()
	f.uploadIntentID = uuid.MustParse("60600965-b2af-4760-9728-87c0736c9ba9")

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeUploadIntentNotFound, "")
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentReturnsBoundedErrorWhenSessionIsNotFound(t *testing.T) {
	f := newConfirmFixture()
	f.sessionID = uuid.MustParse("82470eb8-a69a-4af2-8ba7-1debbf927568")

	_, err := f.confirm()

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionNotFound {
		t.Fatalf("Confirm() error = %#v, want %s", err, session.CodeSessionNotFound)
	}
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsWrongInitialSessionStatusBeforeExternalIO(t *testing.T) {
	f := newConfirmFixture()
	f.transactions.state.session.Status = session.StatusCreated

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsDifferentIntentKindBeforeExternalIO(t *testing.T) {
	f := newConfirmFixture()
	f.transactions.state.uploadIntent.Kind = "biometric_capture"

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeInvalidUploadIntentKind, "")
	if f.storageCalls != 0 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want 0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsEmptyObjectWithBoundedReason(t *testing.T) {
	f := newConfirmFixture()
	f.storage.metadata.SizeBytes = 0

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeInvalidObjectMetadata, artifact.ReasonObjectEmpty)
	if f.storageCalls != 1 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want storage:1 extractor:0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsObjectAboveTenMiBWithBoundedReason(t *testing.T) {
	f := newConfirmFixture()
	f.storage.metadata.SizeBytes = 10*1024*1024 + 1

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeInvalidObjectMetadata, artifact.ReasonObjectTooLarge)
	if f.storageCalls != 1 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want storage:1 extractor:0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsUnsupportedContentTypeWithBoundedReason(t *testing.T) {
	f := newConfirmFixture()
	f.storage.metadata.ContentType = "text/plain"

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeInvalidObjectMetadata, artifact.ReasonUnsupportedContentType)
	if f.storageCalls != 1 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want storage:1 extractor:0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentAcceptsPNGAndPDFAtMaximumSize(t *testing.T) {
	for _, contentType := range []string{"image/png", "application/pdf"} {
		t.Run(contentType, func(t *testing.T) {
			f := newConfirmFixture()
			f.storage.metadata.ContentType = contentType
			f.storage.metadata.SizeBytes = 10 * 1024 * 1024

			_, err := f.confirm()

			if err != nil {
				t.Fatalf("Confirm() error = %#v", err)
			}
			if len(f.transactions.state.artifacts) != 1 ||
				f.transactions.state.artifacts[0].ContentType != contentType ||
				f.transactions.state.artifacts[0].SizeBytes != 10*1024*1024 {
				t.Errorf("stored artifacts = %#v, want accepted %s at 10 MiB", f.transactions.state.artifacts, contentType)
			}
		})
	}
}

func TestConfirmIdentityDocumentBoundsObjectStorageFailure(t *testing.T) {
	f := newConfirmFixture()
	rawBoundaryError := errors.New("sdk secret: bucket=private-identity-documents")
	f.storage.err = rawBoundaryError

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeObjectStorageFailed, "")
	if strings.Contains(err.Error(), rawBoundaryError.Error()) {
		t.Fatalf("Confirm() error leaked storage details: %q", err)
	}
	if f.storageCalls != 1 || f.extractorCalls != 0 {
		t.Errorf("external calls = storage:%d extractor:%d, want storage:1 extractor:0", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentBoundsExtractorFailure(t *testing.T) {
	f := newConfirmFixture()
	rawBoundaryError := errors.New("extractor secret: raw OCR payload")
	f.extractor.err = rawBoundaryError

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeDocumentExtractionFailed, "")
	if strings.Contains(err.Error(), rawBoundaryError.Error()) {
		t.Fatalf("Confirm() error leaked extractor details: %q", err)
	}
	if f.storageCalls != 1 || f.extractorCalls != 1 {
		t.Errorf("external calls = storage:%d extractor:%d, want storage:1 extractor:1", f.storageCalls, f.extractorCalls)
	}
}

func TestConfirmIdentityDocumentPerformsExternalIOWithoutOpenTransaction(t *testing.T) {
	f := newConfirmFixture()
	f.storage.onHead = func(string) {
		if f.transactions.transactionActive {
			t.Error("HeadObject() called while transaction is active")
		}
	}
	f.extractor.onExtract = func(string) {
		if f.transactions.transactionActive {
			t.Error("Extract() called while transaction is active")
		}
	}

	_, err := f.confirm()

	if err != nil {
		t.Fatalf("Confirm() error = %#v", err)
	}
}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenSessionStatusChanges(t *testing.T) {
	f := newConfirmFixture()
	f.extractor.onExtract = func(string) {
		f.transactions.state.session.Status = session.StatusCreated
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenSessionExpiryChanges(t *testing.T) {
	f := newConfirmFixture()
	f.extractor.onExtract = func(string) {
		f.transactions.state.session.ExpiresAt = f.now.Add(2 * time.Minute)
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenResumeTokenHashChanges(t *testing.T) {
	f := newConfirmFixture()
	f.tokens.equalFn = bytes.Equal
	f.extractor.onExtract = func(string) {
		f.transactions.state.session.ResumeTokenHash = []byte("rotated-hash")
	}

	_, err := f.confirm()

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeInvalidResumeToken {
		t.Fatalf("Confirm() error = %#v, want %s", err, session.CodeInvalidResumeToken)
	}
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenIntentExpiryChanges(t *testing.T) {
	f := newConfirmFixture()
	f.extractor.onExtract = func(string) {
		f.transactions.state.uploadIntent.ExpiresAt = f.now.Add(6 * time.Minute)
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentRejectsIntentSupersededDuringExternalIO(t *testing.T) {
	f := newConfirmFixture()
	f.extractor.onExtract = func(string) {
		f.transactions.state.uploadIntent.Status = "superseded"
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeUploadIntentSuperseded, "")
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentDiscardsExternalResultWhenPersonalDetailsChange(t *testing.T) {
	f := newConfirmFixture()
	rawChangedIdentityNumber := "3173000000000099"
	f.extractor.onExtract = func(string) {
		f.transactions.state.details.IdentityNumber = rawChangedIdentityNumber
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if strings.Contains(err.Error(), rawChangedIdentityNumber) {
		t.Fatalf("Confirm() error leaked changed identity number: %q", err)
	}
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentReturnsStaleWhenPersonalDetailsDisappear(t *testing.T) {
	f := newConfirmFixture()
	f.extractor.onExtract = func(string) {
		f.transactions.state.details = nil
	}

	_, err := f.confirm()

	requireArtifactError(t, err, artifact.CodeConfirmationStale, "")
	if len(f.transactions.state.artifacts) != 0 || len(f.transactions.state.events) != 0 ||
		f.transactions.state.uploadIntent.Status != "pending" {
		t.Errorf("state = %#v, want no confirmation writes", f.transactions.state)
	}
}

func TestConfirmIdentityDocumentRejectsInvalidTokenBeforeReadingIntentOrExternalIO(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("26b36016-e4ce-4e7a-9e40-d8c6b229746a")
	uploadIntentID := uuid.MustParse("58bc726b-9175-4569-9980-64d315f36486")
	transactions := newMemoryTransactions(
		session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: []byte("stored-hash"),
			ExpiresAt:       now.Add(time.Minute),
		},
		personaldetails.PersonalDetails{SessionID: sessionID},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "identity-document-key",
			Status:                "pending",
			ExpiresAt:             now.Add(5 * time.Minute),
		},
	)
	storageCalls := 0
	extractorCalls := 0
	service := artifact.NewService(
		transactions,
		stubConfirmCoordinator{
			reader:     transactions,
			transactor: transactions,
		},
		stubObjectStorage{
			metadata: artifact.ObjectMetadata{ContentType: "image/jpeg", SizeBytes: 1},
			calls:    &storageCalls,
		},
		stubDocumentExtractor{
			extraction: artifact.DocumentExtraction{},
			calls:      &extractorCalls,
		},
		stubTokenIssuer{hash: []byte("wrong-hash"), equal: false},
		fixedClock{now: now},
	)

	_, err := service.Confirm(context.Background(), sessionID, "wrong-token", uploadIntentID)

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeInvalidResumeToken {
		t.Fatalf("Confirm() error = %#v, want %s", err, session.CodeInvalidResumeToken)
	}
	if transactions.loadUploadIntentCalls != 0 || transactions.loadDetailsCalls != 0 {
		t.Errorf(
			"reader calls after invalid token = intent:%d details:%d, want 0 for both",
			transactions.loadUploadIntentCalls,
			transactions.loadDetailsCalls,
		)
	}
	if storageCalls != 0 || extractorCalls != 0 {
		t.Errorf("external calls after invalid token = storage:%d extractor:%d, want 0 for both", storageCalls, extractorCalls)
	}
}

func TestConfirmIdentityDocumentRejectsSessionAtExactExpiryBeforeExternalIO(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("ce93a4f4-aa9b-4531-b216-384d47fd9c57")
	uploadIntentID := uuid.MustParse("bd4518e0-45e2-4a42-ac42-8d3b9f247f02")
	transactions := newMemoryTransactions(
		session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: []byte("stored-hash"),
			ExpiresAt:       now,
		},
		personaldetails.PersonalDetails{SessionID: sessionID},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "identity-document-key",
			Status:                "pending",
			ExpiresAt:             now.Add(5 * time.Minute),
		},
	)
	storageCalls := 0
	extractorCalls := 0
	service := artifact.NewService(
		transactions,
		stubConfirmCoordinator{
			reader:     transactions,
			transactor: transactions,
		},
		stubObjectStorage{calls: &storageCalls},
		stubDocumentExtractor{calls: &extractorCalls},
		stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Confirm(context.Background(), sessionID, "raw-resume-token", uploadIntentID)

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionExpired {
		t.Fatalf("Confirm() error = %#v, want %s", err, session.CodeSessionExpired)
	}
	if transactions.loadUploadIntentCalls != 0 || transactions.loadDetailsCalls != 0 ||
		storageCalls != 0 || extractorCalls != 0 {
		t.Errorf(
			"calls after expired session = intent:%d details:%d storage:%d extractor:%d, want all 0",
			transactions.loadUploadIntentCalls,
			transactions.loadDetailsCalls,
			storageCalls,
			extractorCalls,
		)
	}
}

func TestConfirmIdentityDocumentRollsBackAllWritesWhenEventAppendFails(t *testing.T) {
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	previousUpdatedAt := now.Add(-time.Minute)
	sessionID := uuid.MustParse("5dfef7f4-f44f-4df8-b1e4-df88d8046c31")
	uploadIntentID := uuid.MustParse("62097a78-abf2-4c9c-990d-6845f9376d86")
	identityNumber := "3173000000000001"
	transactions := newMemoryTransactions(
		session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: []byte("stored-hash"),
			ExpiresAt:       now.Add(time.Minute),
			UpdatedAt:       previousUpdatedAt,
		},
		personaldetails.PersonalDetails{
			SessionID: sessionID,
			Input: personaldetails.Input{
				IdentityNumber: identityNumber,
			},
		},
		artifact.UploadIntent{
			ID:                    uploadIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "identity-document-key",
			Status:                "pending",
			LatestStatusChangeAt:  previousUpdatedAt,
			ExpiresAt:             now.Add(5 * time.Minute),
		},
	)

	injectedErr := errors.New("injected append event failure")
	transactions.appendEventErr = injectedErr
	service := artifact.NewService(
		transactions,
		stubConfirmCoordinator{
			reader:     transactions,
			transactor: transactions,
		},
		stubObjectStorage{metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   1024,
			ETag:        "identity-document-etag",
		}},
		stubDocumentExtractor{extraction: artifact.DocumentExtraction{IdentityNumber: identityNumber}},
		stubTokenIssuer{hash: []byte("stored-hash"), equal: true},
		fixedClock{now: now},
	)

	_, err := service.Confirm(context.Background(), sessionID, "raw-resume-token", uploadIntentID)

	if !errors.Is(err, injectedErr) {
		t.Fatalf("Confirm() error = %#v, want injected append failure", err)
	}
	storedIntent := transactions.state.uploadIntent
	if storedIntent.Status != "pending" || storedIntent.ConfirmedAt != nil || storedIntent.FailureCode != nil ||
		!storedIntent.LatestStatusChangeAt.Equal(previousUpdatedAt) {
		t.Errorf("stored Upload Intent = %#v, want original pending state", storedIntent)
	}
	if len(transactions.state.artifacts) != 0 {
		t.Errorf("stored artifacts = %d, want 0 after rollback", len(transactions.state.artifacts))
	}
	if transactions.state.session.Status != session.StatusPersonalDetailsSubmitted ||
		!transactions.state.session.UpdatedAt.Equal(previousUpdatedAt) {
		t.Errorf("stored session = %#v, want original state after rollback", transactions.state.session)
	}
	if len(transactions.state.events) != 0 {
		t.Errorf("stored events = %d, want 0 after rollback", len(transactions.state.events))
	}
}

func (m *memoryTransactions) WithinTransaction(ctx context.Context, operation func(artifact.Transaction) error) error {
	next := m.state

	if m.state.details != nil {
		detailsCopy := *m.state.details
		next.details = &detailsCopy
	}

	next.artifacts = append([]artifact.VerificationArtifact(nil), m.state.artifacts...)

	next.events = append([]session.AppendEventParams(nil), next.events...)
	m.transactionActive = true
	err := operation(&memoryTransaction{
		state:          &next,
		appendEventErr: m.appendEventErr,
	})
	m.transactionActive = false
	if err != nil {
		return err
	}

	m.state = next
	return nil
}

type stubObjectStorage struct {
	metadata artifact.ObjectMetadata
	err      error
	calls    *int
	onHead   func(string)
}

type stubDocumentExtractor struct {
	extraction artifact.DocumentExtraction
	err        error
	onExtract  func(string)
	calls      *int
}

func (s stubObjectStorage) HeadObject(_ context.Context, storageKey string) (artifact.ObjectMetadata, error) {
	if s.calls != nil {
		*s.calls++
	}
	if s.onHead != nil {
		s.onHead(storageKey)
	}
	return s.metadata, s.err
}

func (s stubDocumentExtractor) Extract(_ context.Context, storageKey string) (artifact.DocumentExtraction, error) {
	if s.calls != nil {
		*s.calls++
	}
	if s.onExtract != nil {
		s.onExtract(storageKey)
	}
	return s.extraction, s.err
}

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type stubTokenIssuer struct {
	hash    []byte
	equal   bool
	equalFn func([]byte, []byte) bool
}

func (s stubTokenIssuer) Issue() (string, []byte, error) {
	return "", nil, nil
}

func (s stubTokenIssuer) Hash(string) []byte {
	return s.hash
}

func (s stubTokenIssuer) Equal(left, right []byte) bool {
	if s.equalFn != nil {
		return s.equalFn(left, right)
	}
	return s.equal
}

func (m *memoryTransactions) LoadSession(
	_ context.Context,
	id uuid.UUID,
) (session.VerificationSession, error) {
	if m.state.session.ID != id {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}

	return m.state.session, nil
}

func (m *memoryTransactions) LoadUploadIntent(_ context.Context, sessionID uuid.UUID, uploadIntentID uuid.UUID) (artifact.UploadIntent, error) {
	m.loadUploadIntentCalls++
	if m.state.uploadIntent.VerificationSessionID != sessionID || m.state.uploadIntent.ID != uploadIntentID {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}

	return m.state.uploadIntent, nil
}

func loadPersonalDetails(state *memoryState, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	if state.details == nil {
		return personaldetails.PersonalDetails{}, artifact.ErrPersonalDetailsNotFound
	}

	if state.details.SessionID != sessionID {
		return personaldetails.PersonalDetails{}, artifact.ErrPersonalDetailsNotFound
	}

	return *state.details, nil
}
func (m *memoryTransactions) LoadPersonalDetails(_ context.Context, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	m.loadDetailsCalls++
	return loadPersonalDetails(&m.state, sessionID)
}

func (m *memoryTransaction) LockSession(_ context.Context, sessionID uuid.UUID) (session.VerificationSession, error) {
	if m.state.session.ID != sessionID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}

	return m.state.session, nil
}

func (m *memoryTransaction) LockUploadIntent(_ context.Context, sessionID uuid.UUID, uploadIntentID uuid.UUID) (artifact.UploadIntent, error) {
	if m.state.uploadIntent.VerificationSessionID != sessionID {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}

	if m.state.uploadIntent.ID != uploadIntentID {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}

	return m.state.uploadIntent, nil
}

func (m *memoryTransaction) LoadPersonalDetails(_ context.Context, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	return loadPersonalDetails(m.state, sessionID)
}

var errMemoryUploadIntentNotFound = errors.New("memory upload intent not found")

func (m *memoryTransaction) ConfirmUploadIntent(_ context.Context, intentID uuid.UUID, confirmedAt time.Time) error {
	if m.state.uploadIntent.ID != intentID {
		return errMemoryUploadIntentNotFound
	}

	m.state.uploadIntent.Status = "confirmed"
	m.state.uploadIntent.ConfirmedAt = &confirmedAt
	m.state.uploadIntent.LatestStatusChangeAt = confirmedAt

	return nil
}

func (m *memoryTransaction) InsertVerificationArtifact(_ context.Context, artifact artifact.VerificationArtifact) error {
	m.state.artifacts = append(m.state.artifacts, artifact)
	return nil
}

func (m *memoryTransaction) UpdateSessionState(
	_ context.Context,
	sessionID uuid.UUID,
	expected verificationsession.State,
	next verificationsession.State,
	updatedAt time.Time,
) error {
	if m.state.session.ID != sessionID {
		return session.ErrSessionNotFound
	}

	if m.state.session.Status != expected {
		return session.ErrSessionTransitionStale
	}
	m.state.session.Status = next
	m.state.session.UpdatedAt = updatedAt
	return nil
}

func (m *memoryTransaction) AppendEvent(_ context.Context, event session.AppendEventParams) error {
	if m.appendEventErr != nil {
		return m.appendEventErr
	}
	m.state.events = append(m.state.events, event)
	return nil
}

func (m *memoryTransaction) MarkUploadIntentValidationFailed(_ context.Context, intentID uuid.UUID, failureCode string, failedAt time.Time) error {
	if m.state.uploadIntent.ID != intentID {
		return errMemoryUploadIntentNotFound
	}

	failureCodeCopy := failureCode

	m.state.uploadIntent.Status = "validation_failed"
	m.state.uploadIntent.FailureCode = &failureCodeCopy
	m.state.uploadIntent.ConfirmedAt = nil
	m.state.uploadIntent.LatestStatusChangeAt = failedAt

	return nil
}

type stubConfirmCoordinator struct {
	reader     artifact.Reader
	transactor artifact.Transactor
}

func (c stubConfirmCoordinator) WithinConfirm(ctx context.Context, intentID uuid.UUID, operation func(artifact.Reader, artifact.Transactor) error) error {
	err := operation(c.reader, c.transactor)
	if err != nil {
		return err
	}
	return nil
}
