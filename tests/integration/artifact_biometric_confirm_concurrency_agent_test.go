package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang/internal/adapter/postgres"
	"github.com/santosidauruk/lawang/internal/application/artifact"
	"github.com/santosidauruk/lawang/internal/application/session"
)

func TestPostgresBiometricConfirmedReplayReturnsRecordedSuccessWithoutExternalWork(t *testing.T) {
	now := time.Date(2026, 8, 21, 16, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	pool := openBiometricConfirmPool(t, ctx, database, 2)

	storage := &concurrentObjectStorage{metadata: artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "biometric-confirmed-replay-etag",
	}}
	extractor := &concurrentBiometricFailFastExtractor{}
	postgresArtifacts, service := newBiometricPostgresConfirmService(
		pool,
		storage,
		extractor,
		now,
	)

	first, err := service.Confirm(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		fixture.biometricIntentID,
	)
	if err != nil {
		t.Fatalf("first Confirm() error = %v", err)
	}
	second, err := service.Confirm(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		fixture.biometricIntentID,
	)
	if err != nil {
		t.Fatalf("replay Confirm() error = %v", err)
	}

	if first != second || second.Status != session.StatusBiometricCaptureUploaded {
		t.Errorf("replay summary = %#v, want recorded %#v", second, first)
	}
	if calls := storage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}
	requireBiometricAcceptedOutcome(t, ctx, database, postgresArtifacts, fixture)
}

func TestPostgresBiometricConcurrentSecondCallerWaitsForSameAdvisoryLock(t *testing.T) {
	now := time.Date(2026, 8, 21, 17, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	pool := openBiometricConfirmPool(t, ctx, database, 2)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "biometric-lock-waiter-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentBiometricFailFastExtractor{}
	postgresArtifacts, service := newBiometricPostgresConfirmService(
		pool,
		storage,
		extractor,
		now,
	)

	results := make(chan confirmResult, 2)
	callConfirm := func() {
		summary, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		results <- confirmResult{summary: summary, err: confirmErr}
	}

	go callConfirm()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Biometric Confirm did not reach HeadObject")
	}

	go callConfirm()
	requireAdvisoryLockWaiter(t, ctx, database)
	storage.release()

	for range 2 {
		select {
		case result := <-results:
			if result.err != nil {
				t.Errorf("Confirm() error = %v, want nil", result.err)
			}
			if result.summary.ID != fixture.sessionID ||
				result.summary.Status != session.StatusBiometricCaptureUploaded {
				t.Errorf("Confirm() summary = %#v, want recorded biometric success", result.summary)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent Biometric Confirm calls did not finish")
		}
	}

	if calls := storage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}
	requireBiometricAcceptedOutcome(t, ctx, database, postgresArtifacts, fixture)
}

func TestPostgresBiometricConfirmCancellationWhileWaitingDoesNotLeakConnectionOrLock(t *testing.T) {
	now := time.Date(2026, 8, 21, 18, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	pool := openBiometricConfirmPool(t, ctx, database, 2)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "biometric-canceled-waiter-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentBiometricFailFastExtractor{}
	postgresArtifacts, service := newBiometricPostgresConfirmService(
		pool,
		storage,
		extractor,
		now,
	)

	firstResult := make(chan confirmResult, 1)
	go func() {
		summary, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		firstResult <- confirmResult{summary: summary, err: confirmErr}
	}()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Biometric Confirm did not reach HeadObject")
	}

	waiterCtx, cancelWaiter := context.WithCancel(ctx)
	secondResult := make(chan error, 1)
	go func() {
		_, confirmErr := service.Confirm(
			waiterCtx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		secondResult <- confirmErr
	}()
	requireAdvisoryLockWaiter(t, ctx, database)
	cancelWaiter()

	select {
	case confirmErr := <-secondResult:
		if !errors.Is(confirmErr, context.Canceled) {
			t.Fatalf("waiting Biometric Confirm error = %v, want context canceled", confirmErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled Biometric advisory-lock waiter did not return")
	}

	storage.release()
	select {
	case result := <-firstResult:
		if result.err != nil {
			t.Fatalf("first Biometric Confirm error = %v", result.err)
		}
		if result.summary.Status != session.StatusBiometricCaptureUploaded {
			t.Errorf("first Biometric Confirm summary = %#v, want recorded success", result.summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Biometric Confirm did not finish after waiter cancellation")
	}

	replayResult := make(chan confirmResult, 1)
	go func() {
		summary, replayErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		replayResult <- confirmResult{summary: summary, err: replayErr}
	}()
	select {
	case result := <-replayResult:
		if result.err != nil {
			t.Fatalf("post-cancellation Biometric replay error = %v", result.err)
		}
		if result.summary.Status != session.StatusBiometricCaptureUploaded {
			t.Errorf("post-cancellation replay summary = %#v, want recorded success", result.summary)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("post-cancellation Biometric replay remained blocked")
	}

	if calls := storage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}
	requireBiometricAcceptedOutcome(t, ctx, database, postgresArtifacts, fixture)
}

func TestPostgresBiometricConcurrentConfirmDiscardsExternalResultWhenIntentIsSuperseded(t *testing.T) {
	runBiometricConcurrentIntentMutation(
		t,
		"superseded-biometric-stale-result-etag",
		artifact.CodeUploadIntentSuperseded,
		func(ctx context.Context, database *pgx.Conn, fixture biometricPostgresFixture, now time.Time) error {
			_, err := database.Exec(ctx, `
				UPDATE upload_intents
				SET status = 'superseded', latest_status_change_at = $2
				WHERE id = $1
			`, fixture.biometricIntentID, now)
			return err
		},
	)
}

func TestPostgresBiometricConcurrentConfirmDiscardsExternalResultWhenIntentExpires(t *testing.T) {
	runBiometricConcurrentIntentMutation(
		t,
		"expired-biometric-stale-result-etag",
		artifact.CodeUploadIntentExpired,
		func(ctx context.Context, database *pgx.Conn, fixture biometricPostgresFixture, now time.Time) error {
			_, err := database.Exec(ctx, `
				UPDATE upload_intents
				SET expires_at = $2
				WHERE id = $1
			`, fixture.biometricIntentID, now.Add(-time.Minute))
			return err
		},
	)
}

func TestPostgresCreateBiometricUploadIntentSupersedesInFlightConfirm(t *testing.T) {
	now := time.Date(2026, 8, 21, 20, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	pool := openBiometricConfirmPool(t, ctx, database, 2)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "biometric-create-versus-confirm-etag",
	})
	t.Cleanup(storage.release)
	extractor := &concurrentBiometricFailFastExtractor{}
	postgresArtifacts, confirmService := newBiometricPostgresConfirmService(
		pool,
		storage,
		extractor,
		now,
	)
	presigner := &artifactPostgresPresigner{
		url: "https://uploads.example.test/biometric-replacement",
	}
	createService := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	confirmResultChannel := make(chan error, 1)
	go func() {
		_, confirmErr := confirmService.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		confirmResultChannel <- confirmErr
	}()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Biometric Confirm did not reach HeadObject")
	}

	created, err := createService.Create(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		"biometric_capture",
	)
	if err != nil {
		t.Fatalf("Create(biometric_capture) during Confirm error = %v", err)
	}
	storage.release()

	select {
	case confirmErr := <-confirmResultChannel:
		requireArtifactErrorCode(t, confirmErr, artifact.CodeUploadIntentSuperseded)
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight Biometric Confirm did not finish")
	}
	requireNoBiometricAcceptedOutcome(t, ctx, database, postgresArtifacts, fixture)

	var oldStatus string
	var replacementStatus string
	var replacementKind string
	var replacementKey string
	if err := database.QueryRow(ctx, `
		SELECT
			(SELECT status FROM upload_intents WHERE id = $1),
			status,
			kind,
			storage_key
		FROM upload_intents
		WHERE id = $2
	`, fixture.biometricIntentID, created.ID).Scan(
		&oldStatus,
		&replacementStatus,
		&replacementKind,
		&replacementKey,
	); err != nil {
		t.Fatalf("read Biometric create-versus-confirm outcome: %v", err)
	}
	wantKey := "verification-sessions/" + fixture.sessionID.String() +
		"/biometric_capture/" + created.ID.String()
	if oldStatus != "superseded" ||
		replacementStatus != "pending" ||
		replacementKind != "biometric_capture" ||
		replacementKey != wantKey ||
		replacementKey == fixture.biometricKey {
		t.Errorf(
			"replacement = old:%q new:%q kind:%q key:%q, want superseded/pending/biometric_capture/%q",
			oldStatus,
			replacementStatus,
			replacementKind,
			replacementKey,
			wantKey,
		)
	}
	if calls := storage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}
}

func TestPostgresConcurrentCreateKeepsOnePendingIntentPerKindAndIsolatedKeys(t *testing.T) {
	now := time.Date(2026, 8, 21, 21, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	sessionID := uuid.New()
	tokens := session.NewProductionCryptoTokens()
	if _, err := database.Exec(ctx, `
		INSERT INTO verification_sessions (
			id, resume_token_hash, status, created_at, updated_at, expires_at
		)
		VALUES ($1, $2, 'identity_document_uploaded', $3, $3, $4)
	`, sessionID, tokens.Hash("concurrent-per-kind-"+uuid.NewString()), now, now.Add(30*time.Minute)); err != nil {
		t.Fatalf("insert concurrent-create Verification Session: %v", err)
	}

	pool := openBiometricConfirmPool(t, ctx, database, 4)
	type candidate struct {
		id   uuid.UUID
		kind string
	}
	candidates := []candidate{
		{id: uuid.New(), kind: "identity_document"},
		{id: uuid.New(), kind: "identity_document"},
		{id: uuid.New(), kind: "biometric_capture"},
		{id: uuid.New(), kind: "biometric_capture"},
	}
	type createResult struct {
		candidate candidate
		err       error
	}
	start := make(chan struct{})
	ready := make(chan struct{}, len(candidates))
	results := make(chan createResult, len(candidates))
	for _, item := range candidates {
		item := item
		go func() {
			ready <- struct{}{}
			<-start
			key := "verification-sessions/" + sessionID.String() + "/" +
				item.kind + "/" + item.id.String()
			_, insertErr := pool.Exec(ctx, `
				INSERT INTO upload_intents (
					id, verification_session_id, kind, storage_key,
					created_at, latest_status_change_at, expires_at
				)
				VALUES ($1, $2, $3, $4, $5, $5, $6)
			`, item.id, sessionID, item.kind, key, now, now.Add(5*time.Minute))
			results <- createResult{candidate: item, err: insertErr}
		}()
	}
	for range candidates {
		<-ready
	}
	close(start)

	successByKind := map[string]int{}
	conflictByKind := map[string]int{}
	for range candidates {
		select {
		case result := <-results:
			if result.err == nil {
				successByKind[result.candidate.kind]++
				continue
			}
			var postgresError *pgconn.PgError
			if !errors.As(result.err, &postgresError) ||
				postgresError.Code != "23505" ||
				postgresError.ConstraintName != "upload_intents_one_pending_per_session_kind_idx" {
				t.Fatalf("concurrent %s create error = %#v, want one-pending unique violation", result.candidate.kind, result.err)
			}
			conflictByKind[result.candidate.kind]++
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent per-kind Upload Intent creates did not finish")
		}
	}
	for _, kind := range []string{"identity_document", "biometric_capture"} {
		if successByKind[kind] != 1 || conflictByKind[kind] != 1 {
			t.Errorf(
				"concurrent %s creates = %d success/%d conflict, want 1/1",
				kind,
				successByKind[kind],
				conflictByKind[kind],
			)
		}
	}

	rows, err := database.Query(ctx, `
		SELECT id, kind, storage_key
		FROM upload_intents
		WHERE verification_session_id = $1 AND status = 'pending'
		ORDER BY kind
	`, sessionID)
	if err != nil {
		t.Fatalf("read committed per-kind Upload Intents: %v", err)
	}
	defer rows.Close()
	pendingCount := 0
	seenKinds := map[string]bool{}
	seenKeys := map[string]bool{}
	for rows.Next() {
		var intentID uuid.UUID
		var kind string
		var key string
		if err := rows.Scan(&intentID, &kind, &key); err != nil {
			t.Fatalf("scan committed per-kind Upload Intent: %v", err)
		}
		wantKey := "verification-sessions/" + sessionID.String() + "/" +
			kind + "/" + intentID.String()
		if key != wantKey {
			t.Errorf("%s storage key = %q, want %q", kind, key, wantKey)
		}
		if seenKinds[kind] {
			t.Errorf("more than one pending %s Upload Intent committed", kind)
		}
		if seenKeys[key] {
			t.Errorf("pending Upload Intents share storage key %q", key)
		}
		seenKinds[kind] = true
		seenKeys[key] = true
		pendingCount++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate committed per-kind Upload Intents: %v", err)
	}
	if pendingCount != 2 || !seenKinds["identity_document"] || !seenKinds["biometric_capture"] {
		t.Errorf("committed pending intents = %d kinds=%v, want one per required kind", pendingCount, seenKinds)
	}
}

func runBiometricConcurrentIntentMutation(
	t *testing.T,
	etag string,
	wantCode artifact.ErrorCode,
	mutate func(context.Context, *pgx.Conn, biometricPostgresFixture, time.Time) error,
) {
	t.Helper()
	now := time.Date(2026, 8, 21, 19, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	pool := openBiometricConfirmPool(t, ctx, database, 2)

	storage := newBlockingConcurrentObjectStorage(artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        etag,
	})
	t.Cleanup(storage.release)
	extractor := &concurrentBiometricFailFastExtractor{}
	postgresArtifacts, service := newBiometricPostgresConfirmService(
		pool,
		storage,
		extractor,
		now,
	)

	results := make(chan error, 2)
	callConfirm := func() {
		_, confirmErr := service.Confirm(
			ctx,
			fixture.sessionID,
			fixture.rawToken,
			fixture.biometricIntentID,
		)
		results <- confirmErr
	}
	go callConfirm()
	select {
	case <-storage.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first Biometric Confirm did not reach HeadObject")
	}
	go callConfirm()
	requireAdvisoryLockWaiter(t, ctx, database)

	if err := mutate(ctx, database, fixture, now); err != nil {
		t.Fatalf("mutate Biometric Upload Intent during Confirm: %v", err)
	}
	storage.release()

	for range 2 {
		select {
		case confirmErr := <-results:
			requireArtifactErrorCode(t, confirmErr, wantCode)
		case <-time.After(5 * time.Second):
			t.Fatal("stale Biometric Confirm calls did not finish")
		}
	}
	requireNoBiometricAcceptedOutcome(t, ctx, database, postgresArtifacts, fixture)
	if calls := storage.callCount(); calls != 1 {
		t.Errorf("HeadObject() calls = %d, want 1", calls)
	}
	if calls := extractor.callCount(); calls != 0 {
		t.Errorf("DocumentExtractor.Extract() calls = %d, want 0", calls)
	}
}

func openBiometricConfirmPool(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	maxConns int32,
) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(database.Config().ConnString())
	if err != nil {
		t.Fatalf("parse Biometric Confirm pool config: %v", err)
	}
	config.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open Biometric Confirm pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func newBiometricPostgresConfirmService(
	pool *pgxpool.Pool,
	storage artifact.ObjectStorage,
	extractor artifact.DocumentExtractor,
	now time.Time,
) (*postgresadapter.ArtifactTransactions, *artifact.Service) {
	postgresArtifacts := postgresadapter.NewArtifactTransactions(pool)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		storage,
		extractor,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)
	return postgresArtifacts, service
}

func (o *concurrentObjectStorage) callCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.calls
}

func requireBiometricAcceptedOutcome(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	postgresArtifacts *postgresadapter.ArtifactTransactions,
	fixture biometricPostgresFixture,
) {
	t.Helper()

	var intentStatus string
	var sessionStatus string
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			ui.status,
			vs.status,
			(SELECT count(*) FROM verification_artifacts va
			 WHERE va.upload_intent_id = ui.id AND va.kind = 'biometric_capture'),
			(SELECT count(*) FROM session_events se
			 WHERE se.session_id = vs.id AND se.event_type = 'confirm_biometric_capture')
		FROM upload_intents ui
		JOIN verification_sessions vs ON vs.id = ui.verification_session_id
		WHERE ui.id = $1 AND ui.verification_session_id = $2
	`, fixture.biometricIntentID, fixture.sessionID).Scan(
		&intentStatus,
		&sessionStatus,
		&artifactCount,
		&eventCount,
	); err != nil {
		t.Fatalf("read accepted Biometric Capture outcome: %v", err)
	}
	if intentStatus != "confirmed" ||
		sessionStatus != session.StatusBiometricCaptureUploaded.String() ||
		artifactCount != 1 ||
		eventCount != 1 {
		t.Errorf(
			"Biometric Capture outcome = intent:%q session:%q artifacts:%d events:%d",
			intentStatus,
			sessionStatus,
			artifactCount,
			eventCount,
		)
	}

	ready, err := postgresArtifacts.HasRequiredAcceptedArtifacts(ctx, fixture.sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if !ready {
		t.Error("HasRequiredAcceptedArtifacts() = false, want true")
	}
}

func requireNoBiometricAcceptedOutcome(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	postgresArtifacts *postgresadapter.ArtifactTransactions,
	fixture biometricPostgresFixture,
) {
	t.Helper()

	var sessionStatus string
	var artifactCount int
	var eventCount int
	if err := database.QueryRow(ctx, `
		SELECT
			status,
			(SELECT count(*) FROM verification_artifacts
			 WHERE upload_intent_id = $1 AND kind = 'biometric_capture'),
			(SELECT count(*) FROM session_events
			 WHERE session_id = $2 AND event_type = 'confirm_biometric_capture')
		FROM verification_sessions
		WHERE id = $2
	`, fixture.biometricIntentID, fixture.sessionID).Scan(
		&sessionStatus,
		&artifactCount,
		&eventCount,
	); err != nil {
		t.Fatalf("read absent Biometric Capture outcome: %v", err)
	}
	if sessionStatus != session.StatusIdentityDocumentUploaded.String() ||
		artifactCount != 0 ||
		eventCount != 0 {
		t.Errorf(
			"absent Biometric outcome = session:%q artifacts:%d events:%d, want identity_document_uploaded/0/0",
			sessionStatus,
			artifactCount,
			eventCount,
		)
	}

	ready, err := postgresArtifacts.HasRequiredAcceptedArtifacts(ctx, fixture.sessionID)
	if err != nil {
		t.Fatalf("HasRequiredAcceptedArtifacts() error = %v", err)
	}
	if ready {
		t.Error("HasRequiredAcceptedArtifacts() = true without accepted biometric outcome")
	}
}
