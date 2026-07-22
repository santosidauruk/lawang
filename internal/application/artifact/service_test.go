package artifact_test

import (
	"context"
	"errors"
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
	state memoryState
}

type memoryTransaction struct {
	state *memoryState
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

// Checkpoint 2 is intentionally one tracer-bullet test. Complete this test and make
// it RED before adding sibling failure, replay, PostgreSQL, MinIO, or HTTP tests.
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

	service := artifact.NewService(
		transactions,
		transactions,
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

	service := artifact.NewService(
		transactions,
		transactions,
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
		transactions,
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

func (m *memoryTransactions) WithinTransaction(ctx context.Context, operation func(artifact.Transaction) error) error {
	next := m.state

	if m.state.details != nil {
		detailsCopy := *m.state.details
		next.details = &detailsCopy
	}

	next.artifacts = append([]artifact.VerificationArtifact(nil), m.state.artifacts...)

	next.events = append([]session.AppendEventParams(nil), next.events...)
	err := operation(&memoryTransaction{
		state: &next,
	})
	if err != nil {
		return err
	}

	m.state = next
	return nil
}

type stubObjectStorage struct {
	metadata artifact.ObjectMetadata
	err      error
}

type stubDocumentExtractor struct {
	extraction artifact.DocumentExtraction
	err        error
	onExtract  func(string)
}

func (s stubObjectStorage) HeadObject(_ context.Context, _ string) (artifact.ObjectMetadata, error) {
	return s.metadata, s.err
}

func (s stubDocumentExtractor) Extract(_ context.Context, storageKey string) (artifact.DocumentExtraction, error) {
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
	hash  []byte
	equal bool
}

func (s stubTokenIssuer) Issue() (string, []byte, error) {
	return "", nil, nil
}

func (s stubTokenIssuer) Hash(string) []byte {
	return s.hash
}

func (s stubTokenIssuer) Equal(_, _ []byte) bool {
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
	if m.state.uploadIntent.VerificationSessionID != sessionID || m.state.uploadIntent.ID != uploadIntentID {
		return artifact.UploadIntent{}, artifact.ErrUploadIntentNotFound
	}

	return m.state.uploadIntent, nil
}

var errMemoryPersonalDetailsNotFound = errors.New("memory personal details not found")

func loadPersonalDetails(state *memoryState, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
	if state.details == nil {
		return personaldetails.PersonalDetails{}, errMemoryPersonalDetailsNotFound
	}

	if state.details.SessionID != sessionID {
		return personaldetails.PersonalDetails{}, errMemoryPersonalDetailsNotFound
	}

	return *state.details, nil
}
func (m *memoryTransactions) LoadPersonalDetails(_ context.Context, sessionID uuid.UUID) (personaldetails.PersonalDetails, error) {
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
