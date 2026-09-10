package artifact_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/session"
)

// TestCreateBiometricUploadIntentPresignsBeforeAtomicReplacement is the retained
// user-authored tracer for Issue 008 Checkpoint 1.
func TestCreateBiometricUploadIntentPresignsBeforeAtomicReplacement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("62251b18-76f7-410f-8b5f-f72f02eac498")
	oldIntentID := uuid.MustParse("a69337ef-18c2-40c8-bda6-b12dac2175dd")
	rawToken := "create-upload-intent-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)

	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusIdentityDocumentUploaded,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: &artifact.UploadIntent{
			ID:                    oldIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "biometric_capture",
			StorageKey:            "verification-sessions/old",
			Status:                "pending",
			CreatedAt:             now.Add(-time.Minute),
			LatestStatusChangeAt:  now.Add(-time.Minute),
			ExpiresAt:             now.Add(4 * time.Minute),
		},
		sequence: &sequence,
	}

	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/signed",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}

	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		ctx,
		sessionID,
		rawToken,
		"biometric_capture",
	)

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if created.ID == uuid.Nil {
		t.Fatal("Create() ID is nil")
	}

	if created.ID == oldIntentID {
		t.Errorf("Create() ID = %s, want fresh ID different from %s", created.ID, oldIntentID)
	}

	if created.UploadURL != presigner.url {
		t.Errorf("Create() UploadURL = %q, want %q", created.UploadURL, presigner.url)
	}
	expectedKey := "verification-sessions/" + sessionID.String() +
		"/biometric_capture/" + created.ID.String()

	if presigner.storageKey != expectedKey {
		t.Errorf("presigned storage key = %q, want %q", presigner.storageKey, expectedKey)
	}
	if presigner.ttl != 5*time.Minute {
		t.Errorf("presign TTL = %s, want 5m", presigner.ttl)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}

	if transactions.lockSessionCalls != 1 {
		t.Errorf("lock session call got %d, want 1", transactions.lockSessionCalls)
	}

	if transactions.supersededIntent == nil {
		t.Fatal("superseded Upload Intent is nil")
	}
	if transactions.supersededIntent.ID != oldIntentID ||
		transactions.supersededIntent.Status != "superseded" ||
		!transactions.supersededIntent.LatestStatusChangeAt.Equal(now) {
		t.Errorf(
			"superseded Upload Intent = %#v, want old ID/status superseded/time now",
			transactions.supersededIntent,
		)
	}
	if transactions.insertedIntent == nil {
		t.Fatal("inserted Upload Intent is nil")
	}
	inserted := transactions.insertedIntent
	if inserted.ID != created.ID ||
		inserted.VerificationSessionID != sessionID ||
		inserted.Kind != "biometric_capture" ||
		inserted.StorageKey != expectedKey ||
		inserted.Status != "pending" ||
		!inserted.CreatedAt.Equal(now) ||
		!inserted.LatestStatusChangeAt.Equal(now) ||
		!inserted.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("inserted Upload Intent = %#v, want exact new pending intent", inserted)
	}
}

func TestCreateBiometricUploadIntentRejectsWrongInitialSessionState(t *testing.T) {
	now := time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("d0847480-2fe3-4c2e-8346-cb50b40b66c8")
	rawToken := "biometric-wrong-state-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0)
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		sequence: &sequence,
	}
	presigner := &recordingUploadPresigner{
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)

	requireArtifactError(t, err, artifact.CodeUploadIntentStale, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 0 {
		t.Errorf("operation sequence = %#v, want no presign or transaction", sequence)
	}
	if transactions.lockSessionCalls != 0 ||
		transactions.supersededIntent != nil ||
		transactions.insertedIntent != nil {
		t.Errorf(
			"database effects = lock %d / superseded %#v / inserted %#v, want none",
			transactions.lockSessionCalls,
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
}

func TestCreateBiometricUploadIntentStaleRereadDiscardsURLAndWritesNothing(t *testing.T) {
	now := time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("36fc802e-d535-46d2-825c-1047110ef3af")
	rawToken := "biometric-stale-reread-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)
	storedSession := session.VerificationSession{
		ID:              sessionID,
		Status:          session.StatusIdentityDocumentUploaded,
		ResumeTokenHash: tokens.Hash(rawToken),
		ExpiresAt:       now.Add(30 * time.Minute),
	}
	staleSession := storedSession
	staleSession.Status = session.StatusBiometricCaptureUploaded
	transactions := &memoryUploadIntentTransactions{
		storedSession:         storedSession,
		lockedSessionOverride: &staleSession,
		sequence:              &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/discarded-biometric",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)

	requireArtifactError(t, err, artifact.CodeUploadIntentStale, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result with no returned URL", created)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}
	if transactions.lockSessionCalls != 1 {
		t.Errorf("LockSession() calls = %d, want 1", transactions.lockSessionCalls)
	}
	if transactions.supersededIntent != nil || transactions.insertedIntent != nil {
		t.Errorf(
			"database writes = superseded %#v / inserted %#v, want none",
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
}

func TestCreateBiometricUploadIntentPresignFailureWritesNothing(t *testing.T) {
	now := time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("c4b97336-96c6-42bd-baa8-b6462dff0903")
	rawToken := "biometric-presign-failure-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 1)
	pending := &artifact.UploadIntent{
		ID:                    uuid.MustParse("3bb07436-c341-49aa-92d2-a77b0473d4fb"),
		VerificationSessionID: sessionID,
		Kind:                  "biometric_capture",
		StorageKey:            "verification-sessions/existing-biometric",
		Status:                "pending",
	}
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusIdentityDocumentUploaded,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: pending,
		sequence:      &sequence,
	}
	presigner := &recordingUploadPresigner{
		err:      errors.New("presign unavailable"),
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)

	requireArtifactError(t, err, artifact.CodeObjectStorageFailed, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 1 || sequence[0] != "presign" {
		t.Errorf("operation sequence = %#v, want [presign]", sequence)
	}
	if transactions.lockSessionCalls != 0 ||
		transactions.supersededIntent != nil ||
		transactions.insertedIntent != nil {
		t.Errorf(
			"database effects = lock %d / superseded %#v / inserted %#v, want none",
			transactions.lockSessionCalls,
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
	if transactions.pendingIntent != pending {
		t.Error("pending biometric Upload Intent changed after presign failure")
	}
}

func TestCreateBiometricUploadIntentSupersedesOnlyPendingBiometricHistory(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("2c85c1ef-4dd0-4e9e-a5e7-9e93d851e5e7")
	rawToken := "biometric-history-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)
	identityHistory := []artifact.UploadIntent{
		{
			ID:                    uuid.MustParse("0c5bb257-e093-4400-8ba3-7761af57fc09"),
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "verification-sessions/identity-confirmed",
			Status:                "confirmed",
			CreatedAt:             now.Add(-10 * time.Minute),
			LatestStatusChangeAt:  now.Add(-8 * time.Minute),
			ExpiresAt:             now.Add(-5 * time.Minute),
		},
	}
	pendingBiometricID := uuid.MustParse("a32e6b9c-50ce-445f-b4f3-44e08fdb3ef7")
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusIdentityDocumentUploaded,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: &artifact.UploadIntent{
			ID:                    pendingBiometricID,
			VerificationSessionID: sessionID,
			Kind:                  "biometric_capture",
			StorageKey:            "verification-sessions/biometric-pending",
			Status:                "pending",
			CreatedAt:             now.Add(-time.Minute),
			LatestStatusChangeAt:  now.Add(-time.Minute),
			ExpiresAt:             now.Add(4 * time.Minute),
		},
		otherIntents: append([]artifact.UploadIntent(nil), identityHistory...),
		sequence:     &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/biometric-history",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	_, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if transactions.supersedeCalls != 1 || transactions.supersedeKind != "biometric_capture" {
		t.Errorf(
			"supersede calls/kind = %d/%q, want 1/biometric_capture",
			transactions.supersedeCalls,
			transactions.supersedeKind,
		)
	}
	if transactions.supersededIntent == nil ||
		transactions.supersededIntent.ID != pendingBiometricID {
		t.Errorf(
			"superseded Upload Intent = %#v, want pending biometric %s",
			transactions.supersededIntent,
			pendingBiometricID,
		)
	}
	if !slices.Equal(transactions.otherIntents, identityHistory) {
		t.Errorf(
			"identity history = %#v, want unchanged %#v",
			transactions.otherIntents,
			identityHistory,
		)
	}
}

func TestCreateBiometricUploadIntentRepeatedCreateKeepsOnlyLatestPendingEffect(t *testing.T) {
	now := time.Date(2026, 8, 13, 13, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("e397b3fe-8146-4858-916c-747ffda47768")
	rawToken := "biometric-repeated-create-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 4)
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusIdentityDocumentUploaded,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		sequence: &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/repeated-biometric",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	first, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)
	if err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	firstKey := presigner.storageKey

	second, err := service.Create(
		context.Background(),
		sessionID,
		rawToken,
		"biometric_capture",
	)
	if err != nil {
		t.Fatalf("second Create() error = %v", err)
	}
	secondKey := presigner.storageKey

	if first.ID == uuid.Nil || second.ID == uuid.Nil || first.ID == second.ID {
		t.Errorf("created IDs = %s then %s, want distinct non-nil IDs", first.ID, second.ID)
	}
	if firstKey == secondKey {
		t.Errorf("storage keys = %q then %q, want fresh keys", firstKey, secondKey)
	}
	if transactions.supersededCount != 1 ||
		transactions.supersededIntent == nil ||
		transactions.supersededIntent.ID != first.ID {
		t.Errorf(
			"superseded effects = count %d / intent %#v, want first intent only",
			transactions.supersededCount,
			transactions.supersededIntent,
		)
	}
	if transactions.insertCalls != 2 ||
		transactions.pendingIntent == nil ||
		transactions.pendingIntent.ID != second.ID ||
		transactions.pendingIntent.Kind != "biometric_capture" ||
		transactions.pendingIntent.StorageKey != secondKey {
		t.Errorf(
			"latest pending effect = inserts %d / intent %#v, want second biometric intent",
			transactions.insertCalls,
			transactions.pendingIntent,
		)
	}
	if transactions.lockSessionCalls != 2 {
		t.Errorf("LockSession() calls = %d, want 2", transactions.lockSessionCalls)
	}
	wantSequence := []string{"presign", "transaction", "presign", "transaction"}
	if !slices.Equal(sequence, wantSequence) {
		t.Errorf("operation sequence = %#v, want %#v", sequence, wantSequence)
	}
}

func TestCreateUploadIntentIsolatesIdentityAndBiometricKindsAndKeys(t *testing.T) {
	now := time.Date(2026, 8, 13, 14, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("16e39399-f30b-4cbb-940a-34e091cd2a1b")
	rawToken := "kind-isolation-token"
	tokens := session.NewProductionCryptoTokens()

	createForKind := func(
		t *testing.T,
		kind string,
		status session.Status,
	) (artifact.CreatedUploadIntent, string, *memoryUploadIntentTransactions) {
		t.Helper()
		sequence := make([]string, 0, 2)
		transactions := &memoryUploadIntentTransactions{
			storedSession: session.VerificationSession{
				ID:              sessionID,
				Status:          status,
				ResumeTokenHash: tokens.Hash(rawToken),
				ExpiresAt:       now.Add(30 * time.Minute),
			},
			sequence: &sequence,
		}
		presigner := &recordingUploadPresigner{
			url:      "https://uploads.example.test/" + kind,
			sequence: &sequence,
			active:   &transactions.transactionActive,
		}
		service := artifact.NewUploadIntentService(
			transactions,
			transactions,
			presigner,
			tokens,
			fixedClock{now: now},
		)

		created, err := service.Create(
			context.Background(),
			sessionID,
			rawToken,
			kind,
		)
		if err != nil {
			t.Fatalf("Create(%q) error = %v", kind, err)
		}
		return created, presigner.storageKey, transactions
	}

	identity, identityKey, identityTransactions := createForKind(
		t,
		"identity_document",
		session.StatusPersonalDetailsSubmitted,
	)
	biometric, biometricKey, biometricTransactions := createForKind(
		t,
		"biometric_capture",
		session.StatusIdentityDocumentUploaded,
	)

	wantIdentityKey := "verification-sessions/" + sessionID.String() +
		"/identity_document/" + identity.ID.String()
	wantBiometricKey := "verification-sessions/" + sessionID.String() +
		"/biometric_capture/" + biometric.ID.String()
	if identityKey != wantIdentityKey || biometricKey != wantBiometricKey {
		t.Errorf(
			"storage keys = identity %q / biometric %q, want %q / %q",
			identityKey,
			biometricKey,
			wantIdentityKey,
			wantBiometricKey,
		)
	}
	if identityKey == biometricKey {
		t.Errorf("identity and biometric storage keys collide at %q", identityKey)
	}
	if identityTransactions.insertedIntent == nil ||
		identityTransactions.insertedIntent.Kind != "identity_document" ||
		biometricTransactions.insertedIntent == nil ||
		biometricTransactions.insertedIntent.Kind != "biometric_capture" {
		t.Errorf(
			"inserted kinds = identity %#v / biometric %#v, want isolated exact kinds",
			identityTransactions.insertedIntent,
			biometricTransactions.insertedIntent,
		)
	}
}

func TestCreateUploadIntentRejectsInvalidKindBeforePresignOrTransaction(t *testing.T) {
	now := time.Date(2026, 8, 13, 15, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("0ab8e0d3-9441-49d6-a7f5-b008439941f8")
	sequence := make([]string, 0)
	transactions := &memoryUploadIntentTransactions{sequence: &sequence}
	presigner := &recordingUploadPresigner{
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		"invalid-kind-token",
		"passport",
	)

	requireArtifactError(t, err, artifact.CodeInvalidUploadIntentKind, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 0 || presigner.storageKey != "" {
		t.Errorf(
			"external effects = sequence %#v / presigned key %q, want none",
			sequence,
			presigner.storageKey,
		)
	}
	if transactions.lockSessionCalls != 0 ||
		transactions.supersedeCalls != 0 ||
		transactions.insertCalls != 0 {
		t.Errorf(
			"database effects = lock %d / supersede %d / insert %d, want none",
			transactions.lockSessionCalls,
			transactions.supersedeCalls,
			transactions.insertCalls,
		)
	}
}

func TestCreateIdentityUploadIntentPresignsBeforeAtomicReplacement(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("62251b18-76f7-410f-8b5f-f72f02eac498")
	oldIntentID := uuid.MustParse("a69337ef-18c2-40c8-bda6-b12dac2175dd")
	rawToken := "create-upload-intent-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)

	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: &artifact.UploadIntent{
			ID:                    oldIntentID,
			VerificationSessionID: sessionID,
			Kind:                  "identity_document",
			StorageKey:            "verification-sessions/old",
			Status:                "pending",
			CreatedAt:             now.Add(-time.Minute),
			LatestStatusChangeAt:  now.Add(-time.Minute),
			ExpiresAt:             now.Add(4 * time.Minute),
		},
		sequence: &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/signed",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(
		ctx,
		sessionID,
		rawToken,
		"identity_document",
	)

	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("Create() ID is nil")
	}
	if created.UploadURL != presigner.url {
		t.Errorf("Create() UploadURL = %q, want %q", created.UploadURL, presigner.url)
	}
	expectedKey := "verification-sessions/" + sessionID.String() +
		"/identity_document/" + created.ID.String()
	if presigner.storageKey != expectedKey {
		t.Errorf("presigned storage key = %q, want %q", presigner.storageKey, expectedKey)
	}
	if presigner.ttl != 5*time.Minute {
		t.Errorf("presign TTL = %s, want 5m", presigner.ttl)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}

	if transactions.supersededIntent == nil {
		t.Fatal("superseded Upload Intent is nil")
	}
	if transactions.supersededIntent.ID != oldIntentID ||
		transactions.supersededIntent.Status != "superseded" ||
		!transactions.supersededIntent.LatestStatusChangeAt.Equal(now) {
		t.Errorf(
			"superseded Upload Intent = %#v, want old ID/status superseded/time now",
			transactions.supersededIntent,
		)
	}
	if transactions.insertedIntent == nil {
		t.Fatal("inserted Upload Intent is nil")
	}
	inserted := transactions.insertedIntent
	if inserted.ID != created.ID ||
		inserted.VerificationSessionID != sessionID ||
		inserted.Kind != "identity_document" ||
		inserted.StorageKey != expectedKey ||
		inserted.Status != "pending" ||
		!inserted.CreatedAt.Equal(now) ||
		!inserted.LatestStatusChangeAt.Equal(now) ||
		!inserted.ExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf("inserted Upload Intent = %#v, want exact new pending intent", inserted)
	}
}

func TestCreateIdentityUploadIntentPresignFailureWritesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 13, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("67571efe-af5a-4cc2-85c4-a85f24b7c92e")
	rawToken := "presign-failure-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 1)
	pending := &artifact.UploadIntent{
		ID:                    uuid.MustParse("25ca5d02-1ce5-4a7e-826a-c8a7f87ef249"),
		VerificationSessionID: sessionID,
		Kind:                  "identity_document",
		Status:                "pending",
	}
	transactions := &memoryUploadIntentTransactions{
		storedSession: session.VerificationSession{
			ID:              sessionID,
			Status:          session.StatusPersonalDetailsSubmitted,
			ResumeTokenHash: tokens.Hash(rawToken),
			ExpiresAt:       now.Add(30 * time.Minute),
		},
		pendingIntent: pending,
		sequence:      &sequence,
	}
	presigner := &recordingUploadPresigner{
		err:      errors.New("presign unavailable"),
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(ctx, sessionID, rawToken, "identity_document")

	requireArtifactError(t, err, artifact.CodeObjectStorageFailed, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 1 || sequence[0] != "presign" {
		t.Errorf("operation sequence = %#v, want [presign]", sequence)
	}
	if transactions.supersededIntent != nil || transactions.insertedIntent != nil {
		t.Errorf(
			"database writes = superseded %#v / inserted %#v, want none",
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
	if transactions.pendingIntent != pending {
		t.Errorf("pending Upload Intent changed after presign failure")
	}
}

func TestCreateIdentityUploadIntentStaleRereadDiscardsURLAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 24, 14, 0, 0, 0, time.UTC)
	sessionID := uuid.MustParse("71642919-3f18-4edb-babc-fe8d86c114d6")
	rawToken := "stale-create-token"
	tokens := session.NewProductionCryptoTokens()
	sequence := make([]string, 0, 2)
	storedSession := session.VerificationSession{
		ID:              sessionID,
		Status:          session.StatusPersonalDetailsSubmitted,
		ResumeTokenHash: tokens.Hash(rawToken),
		ExpiresAt:       now.Add(30 * time.Minute),
	}
	staleSession := storedSession
	staleSession.Status = session.StatusIdentityDocumentUploaded
	transactions := &memoryUploadIntentTransactions{
		storedSession:         storedSession,
		lockedSessionOverride: &staleSession,
		sequence:              &sequence,
	}
	presigner := &recordingUploadPresigner{
		url:      "https://uploads.example.test/discarded",
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		tokens,
		fixedClock{now: now},
	)

	created, err := service.Create(ctx, sessionID, rawToken, "identity_document")

	requireArtifactError(t, err, artifact.CodeUploadIntentStale, "")
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result with no returned URL", created)
	}
	if len(sequence) != 2 || sequence[0] != "presign" || sequence[1] != "transaction" {
		t.Errorf("operation sequence = %#v, want [presign transaction]", sequence)
	}
	if transactions.supersededIntent != nil || transactions.insertedIntent != nil {
		t.Errorf(
			"database writes = superseded %#v / inserted %#v, want none",
			transactions.supersededIntent,
			transactions.insertedIntent,
		)
	}
}

func TestCreateIdentityUploadIntentMapsMissingSession(t *testing.T) {
	sessionID := uuid.MustParse("9091d98f-6413-4e1f-9074-b491f511082c")
	sequence := make([]string, 0)
	transactions := &memoryUploadIntentTransactions{sequence: &sequence}
	presigner := &recordingUploadPresigner{
		sequence: &sequence,
		active:   &transactions.transactionActive,
	}
	service := artifact.NewUploadIntentService(
		transactions,
		transactions,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: time.Date(2026, 7, 24, 15, 0, 0, 0, time.UTC)},
	)

	created, err := service.Create(
		context.Background(),
		sessionID,
		"missing-session-token",
		"identity_document",
	)

	var serviceError *session.Error
	if !errors.As(err, &serviceError) || serviceError.Code != session.CodeSessionNotFound {
		t.Fatalf("Create() error = %#v, want %s", err, session.CodeSessionNotFound)
	}
	if created != (artifact.CreatedUploadIntent{}) {
		t.Errorf("Create() result = %#v, want zero result", created)
	}
	if len(sequence) != 0 {
		t.Errorf("operation sequence = %#v, want no presign or transaction", sequence)
	}
}

type recordingUploadPresigner struct {
	url        string
	err        error
	storageKey string
	ttl        time.Duration
	sequence   *[]string
	active     *bool
}

func (p *recordingUploadPresigner) PresignUpload(
	_ context.Context,
	storageKey string,
	ttl time.Duration,
) (string, error) {
	if *p.active {
		panic("presign called inside database transaction")
	}
	*p.sequence = append(*p.sequence, "presign")
	p.storageKey = storageKey
	p.ttl = ttl
	return p.url, p.err
}

type memoryUploadIntentTransactions struct {
	storedSession         session.VerificationSession
	lockedSessionOverride *session.VerificationSession
	pendingIntent         *artifact.UploadIntent
	otherIntents          []artifact.UploadIntent
	supersededIntent      *artifact.UploadIntent
	insertedIntent        *artifact.UploadIntent
	sequence              *[]string
	transactionActive     bool
	lockSessionCalls      int
	supersedeCalls        int
	supersedeKind         string
	supersededCount       int
	insertCalls           int
}

func (m *memoryUploadIntentTransactions) LoadSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if m.storedSession.ID != sessionID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	return m.storedSession, nil
}

func (m *memoryUploadIntentTransactions) WithinUploadIntentTransaction(
	_ context.Context,
	operation func(artifact.UploadIntentTransaction) error,
) error {
	*m.sequence = append(*m.sequence, "transaction")
	m.transactionActive = true
	defer func() { m.transactionActive = false }()
	return operation(m)
}

func (m *memoryUploadIntentTransactions) LockSession(
	_ context.Context,
	sessionID uuid.UUID,
) (session.VerificationSession, error) {
	if m.storedSession.ID != sessionID {
		return session.VerificationSession{}, session.ErrSessionNotFound
	}
	m.lockSessionCalls++
	if m.lockedSessionOverride != nil {
		return *m.lockedSessionOverride, nil
	}
	return m.storedSession, nil
}

func (m *memoryUploadIntentTransactions) SupersedePendingUploadIntent(
	_ context.Context,
	sessionID uuid.UUID,
	kind string,
	supersededAt time.Time,
) error {
	m.supersedeCalls++
	m.supersedeKind = kind
	if m.pendingIntent == nil ||
		m.pendingIntent.VerificationSessionID != sessionID ||
		m.pendingIntent.Kind != kind ||
		m.pendingIntent.Status != "pending" {
		return nil
	}
	superseded := *m.pendingIntent
	superseded.Status = "superseded"
	superseded.LatestStatusChangeAt = supersededAt
	m.supersededIntent = &superseded
	m.supersededCount++
	m.pendingIntent = nil
	return nil
}

func (m *memoryUploadIntentTransactions) InsertUploadIntent(
	_ context.Context,
	intent artifact.UploadIntent,
) error {
	m.insertCalls++
	inserted := intent
	m.insertedIntent = &inserted
	m.pendingIntent = &inserted
	return nil
}
