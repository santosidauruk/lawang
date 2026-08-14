package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/artifact"
	"github.com/santosidauruk/lawang-go/internal/application/session"
)

func TestBiometricPostgresConfirmRollsBackWhenEventWriteFails(t *testing.T) {
	now := time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
	if _, err := database.Exec(ctx, `
		CREATE FUNCTION reject_confirm_biometric_capture_event()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.event_type = 'confirm_biometric_capture' THEN
				RAISE EXCEPTION 'forced biometric confirm event failure';
			END IF;
			RETURN NEW;
		END;
		$$;

		CREATE TRIGGER reject_confirm_biometric_capture_event
		BEFORE INSERT ON session_events
		FOR EACH ROW
		EXECUTE FUNCTION reject_confirm_biometric_capture_event();
	`); err != nil {
		t.Fatalf("install forced Biometric Capture event failure: %v", err)
	}

	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	service := artifact.NewService(
		postgresArtifacts,
		postgresadapter.NewArtifactConfirmCoordinator(newArtifactConfirmAcquireFunc(pool)),
		artifactPostgresObjectStorage{metadata: artifact.ObjectMetadata{
			ContentType: "image/jpeg",
			SizeBytes:   4096,
			ETag:        "biometric-rollback-etag",
		}},
		&biometricPostgresFailFastExtractor{t: t},
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	got, err := service.Confirm(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		fixture.biometricIntentID,
	)

	if err == nil {
		t.Fatal("Confirm() error = nil, want forced event-write failure")
	}
	if got != (session.Summary{}) {
		t.Errorf("Confirm() summary = %#v, want zero summary", got)
	}

	var intentStatus string
	var intentConfirmedAt *time.Time
	var intentLatestStatusChangeAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, confirmed_at, latest_status_change_at
		FROM upload_intents
		WHERE id = $1
	`, fixture.biometricIntentID).Scan(
		&intentStatus,
		&intentConfirmedAt,
		&intentLatestStatusChangeAt,
	); err != nil {
		t.Fatalf("read rolled-back Biometric Capture Upload Intent: %v", err)
	}
	if intentStatus != "pending" ||
		intentConfirmedAt != nil ||
		!intentLatestStatusChangeAt.Equal(fixture.biometricIntentCreatedAt) {
		t.Errorf(
			"rolled-back Upload Intent = status:%q confirmed:%v latest:%s",
			intentStatus,
			intentConfirmedAt,
			intentLatestStatusChangeAt,
		)
	}

	var biometricArtifactCount int
	var identityArtifactCount int
	if err := database.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE kind = 'biometric_capture'),
			count(*) FILTER (WHERE kind = 'identity_document')
		FROM verification_artifacts
		WHERE verification_session_id = $1
	`, fixture.sessionID).Scan(
		&biometricArtifactCount,
		&identityArtifactCount,
	); err != nil {
		t.Fatalf("count rolled-back Verification Artifacts: %v", err)
	}
	if biometricArtifactCount != 0 || identityArtifactCount != 1 {
		t.Errorf(
			"Verification Artifact counts = biometric:%d identity:%d, want 0/1",
			biometricArtifactCount,
			identityArtifactCount,
		)
	}

	var storedSessionStatus string
	var storedSessionUpdatedAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, updated_at
		FROM verification_sessions
		WHERE id = $1
	`, fixture.sessionID).Scan(
		&storedSessionStatus,
		&storedSessionUpdatedAt,
	); err != nil {
		t.Fatalf("read rolled-back Verification Session: %v", err)
	}
	if storedSessionStatus != session.StatusIdentityDocumentUploaded.String() ||
		!storedSessionUpdatedAt.Equal(fixture.identityConfirmedAt) {
		t.Errorf(
			"rolled-back Verification Session = status:%q updated:%s",
			storedSessionStatus,
			storedSessionUpdatedAt,
		)
	}

	var biometricEventCount int
	if err := database.QueryRow(ctx, `
		SELECT count(*)
		FROM session_events
		WHERE session_id = $1
		  AND event_type = 'confirm_biometric_capture'
	`, fixture.sessionID).Scan(&biometricEventCount); err != nil {
		t.Fatalf("count rolled-back Biometric Capture Session Events: %v", err)
	}
	if biometricEventCount != 0 {
		t.Errorf("Biometric Capture Session Event count = %d, want 0", biometricEventCount)
	}
}

func TestBiometricPostgresCreateSupersedesOnlyPendingBiometricIntent(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)

	presigner := &artifactPostgresPresigner{
		url: "https://uploads.example.test/biometric-postgres-signed",
	}
	postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
	service := artifact.NewUploadIntentService(
		postgresArtifacts,
		postgresArtifacts,
		presigner,
		session.NewProductionCryptoTokens(),
		fixedClock{now: now},
	)

	created, err := service.Create(
		ctx,
		fixture.sessionID,
		fixture.rawToken,
		"biometric_capture",
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
	expectedKey := fmt.Sprintf(
		"verification-sessions/%s/biometric_capture/%s",
		fixture.sessionID,
		created.ID,
	)
	if presigner.storageKey != expectedKey {
		t.Errorf("presigned storage key = %q, want %q", presigner.storageKey, expectedKey)
	}
	if presigner.ttl != 5*time.Minute {
		t.Errorf("presign TTL = %s, want 5m", presigner.ttl)
	}

	var oldBiometricStatus string
	var oldBiometricLatestStatusChangeAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, latest_status_change_at
		FROM upload_intents
		WHERE id = $1
	`, fixture.biometricIntentID).Scan(
		&oldBiometricStatus,
		&oldBiometricLatestStatusChangeAt,
	); err != nil {
		t.Fatalf("read superseded Biometric Capture Upload Intent: %v", err)
	}
	if oldBiometricStatus != "superseded" ||
		!oldBiometricLatestStatusChangeAt.Equal(now) {
		t.Errorf(
			"old Biometric Capture Upload Intent = status:%q latest:%s, want superseded/%s",
			oldBiometricStatus,
			oldBiometricLatestStatusChangeAt,
			now,
		)
	}

	var newBiometricStatus string
	var newBiometricKey string
	var newBiometricCreatedAt time.Time
	var newBiometricLatestStatusChangeAt time.Time
	var newBiometricExpiresAt time.Time
	if err := database.QueryRow(ctx, `
		SELECT status, storage_key, created_at, latest_status_change_at, expires_at
		FROM upload_intents
		WHERE id = $1
	`, created.ID).Scan(
		&newBiometricStatus,
		&newBiometricKey,
		&newBiometricCreatedAt,
		&newBiometricLatestStatusChangeAt,
		&newBiometricExpiresAt,
	); err != nil {
		t.Fatalf("read new Biometric Capture Upload Intent: %v", err)
	}
	if newBiometricStatus != "pending" ||
		newBiometricKey != expectedKey ||
		!newBiometricCreatedAt.Equal(now) ||
		!newBiometricLatestStatusChangeAt.Equal(now) ||
		!newBiometricExpiresAt.Equal(now.Add(5*time.Minute)) {
		t.Errorf(
			"new Biometric Capture Upload Intent = status:%q key:%q created:%s latest:%s expires:%s",
			newBiometricStatus,
			newBiometricKey,
			newBiometricCreatedAt,
			newBiometricLatestStatusChangeAt,
			newBiometricExpiresAt,
		)
	}

	var biometricTotalCount int
	var biometricPendingCount int
	if err := database.QueryRow(ctx, `
		SELECT
			count(*),
			count(*) FILTER (WHERE status = 'pending')
		FROM upload_intents
		WHERE verification_session_id = $1
		  AND kind = 'biometric_capture'
	`, fixture.sessionID).Scan(
		&biometricTotalCount,
		&biometricPendingCount,
	); err != nil {
		t.Fatalf("count Biometric Capture Upload Intents: %v", err)
	}
	if biometricTotalCount != 2 || biometricPendingCount != 1 {
		t.Errorf(
			"Biometric Capture Upload Intent counts = total:%d pending:%d, want 2/1",
			biometricTotalCount,
			biometricPendingCount,
		)
	}

	var identityIntentStatus string
	var identityArtifactCount int
	if err := database.QueryRow(ctx, `
		SELECT
			ui.status,
			count(va.id)
		FROM upload_intents ui
		LEFT JOIN verification_artifacts va ON va.upload_intent_id = ui.id
		WHERE ui.id = $1
		GROUP BY ui.status
	`, fixture.identityIntentID).Scan(
		&identityIntentStatus,
		&identityArtifactCount,
	); err != nil {
		t.Fatalf("read isolated Identity Document outcome: %v", err)
	}
	if identityIntentStatus != "confirmed" || identityArtifactCount != 1 {
		t.Errorf(
			"Identity Document outcome = status:%q artifacts:%d, want confirmed/1",
			identityIntentStatus,
			identityArtifactCount,
		)
	}
}

func TestBiometricPostgresConfirmRejectsStaleRereadWithoutPartialWrites(t *testing.T) {
	now := time.Date(2026, 8, 14, 13, 0, 0, 0, time.UTC)
	ctx, database := openArtifactDatabase(t)
	pool, err := pgxpool.New(ctx, database.Config().ConnString())
	if err != nil {
		t.Fatalf("create PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)

	tests := []struct {
		name              string
		mutate            func(context.Context, biometricPostgresFixture) error
		wantSessionStatus string
		wantIntentKind    string
		wantIntentKey     func(biometricPostgresFixture) string
	}{
		{
			name: "session state changes during HeadObject",
			mutate: func(ctx context.Context, fixture biometricPostgresFixture) error {
				_, err := database.Exec(ctx, `
					UPDATE verification_sessions
					SET status = 'personal_details_submitted'
					WHERE id = $1
				`, fixture.sessionID)
				return err
			},
			wantSessionStatus: session.StatusPersonalDetailsSubmitted.String(),
			wantIntentKind:    "biometric_capture",
			wantIntentKey: func(fixture biometricPostgresFixture) string {
				return fixture.biometricKey
			},
		},
		{
			name: "storage key changes during HeadObject",
			mutate: func(ctx context.Context, fixture biometricPostgresFixture) error {
				_, err := database.Exec(ctx, `
					UPDATE upload_intents
					SET storage_key = $2
					WHERE id = $1
				`, fixture.biometricIntentID, fixture.biometricKey+"-changed")
				return err
			},
			wantSessionStatus: session.StatusIdentityDocumentUploaded.String(),
			wantIntentKind:    "biometric_capture",
			wantIntentKey: func(fixture biometricPostgresFixture) string {
				return fixture.biometricKey + "-changed"
			},
		},
		{
			name: "kind changes during HeadObject",
			mutate: func(ctx context.Context, fixture biometricPostgresFixture) error {
				_, err := database.Exec(ctx, `
					UPDATE upload_intents
					SET kind = 'identity_document'
					WHERE id = $1
				`, fixture.biometricIntentID)
				return err
			},
			wantSessionStatus: session.StatusIdentityDocumentUploaded.String(),
			wantIntentKind:    "identity_document",
			wantIntentKey: func(fixture biometricPostgresFixture) string {
				return fixture.biometricKey
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := seedBiometricPostgresConfirmationState(t, ctx, database, now)
			objectStorage := &mutatingBiometricPostgresObjectStorage{
				t:       t,
				wantKey: fixture.biometricKey,
				mutate: func(ctx context.Context) error {
					return test.mutate(ctx, fixture)
				},
			}
			postgresArtifacts := postgresadapter.NewArtifactTransactions(database)
			service := artifact.NewService(
				postgresArtifacts,
				postgresadapter.NewArtifactConfirmCoordinator(
					newArtifactConfirmAcquireFunc(pool),
				),
				objectStorage,
				&biometricPostgresFailFastExtractor{t: t},
				session.NewProductionCryptoTokens(),
				fixedClock{now: now},
			)

			got, err := service.Confirm(
				ctx,
				fixture.sessionID,
				fixture.rawToken,
				fixture.biometricIntentID,
			)

			if got != (session.Summary{}) {
				t.Errorf("Confirm() summary = %#v, want zero summary", got)
			}
			var artifactError *artifact.Error
			if !errors.As(err, &artifactError) ||
				artifactError.Code != artifact.CodeConfirmationStale {
				t.Fatalf(
					"Confirm() error = %#v, want %s",
					err,
					artifact.CodeConfirmationStale,
				)
			}
			if objectStorage.calls != 1 {
				t.Errorf("HeadObject() calls = %d, want 1", objectStorage.calls)
			}

			var intentStatus string
			var intentKind string
			var intentKey string
			var intentConfirmedAt *time.Time
			var intentLatestStatusChangeAt time.Time
			if err := database.QueryRow(ctx, `
				SELECT status, kind, storage_key, confirmed_at, latest_status_change_at
				FROM upload_intents
				WHERE id = $1
			`, fixture.biometricIntentID).Scan(
				&intentStatus,
				&intentKind,
				&intentKey,
				&intentConfirmedAt,
				&intentLatestStatusChangeAt,
			); err != nil {
				t.Fatalf("read stale Biometric Capture Upload Intent: %v", err)
			}
			if intentStatus != "pending" ||
				intentKind != test.wantIntentKind ||
				intentKey != test.wantIntentKey(fixture) ||
				intentConfirmedAt != nil ||
				!intentLatestStatusChangeAt.Equal(fixture.biometricIntentCreatedAt) {
				t.Errorf(
					"stale Upload Intent = status:%q kind:%q key:%q confirmed:%v latest:%s",
					intentStatus,
					intentKind,
					intentKey,
					intentConfirmedAt,
					intentLatestStatusChangeAt,
				)
			}

			var sessionStatus string
			if err := database.QueryRow(ctx, `
				SELECT status
				FROM verification_sessions
				WHERE id = $1
			`, fixture.sessionID).Scan(&sessionStatus); err != nil {
				t.Fatalf("read stale Verification Session: %v", err)
			}
			if sessionStatus != test.wantSessionStatus {
				t.Errorf(
					"stale Verification Session status = %q, want %q",
					sessionStatus,
					test.wantSessionStatus,
				)
			}

			var artifactCount int
			var eventCount int
			if err := database.QueryRow(ctx, `
				SELECT count(*)
				FROM verification_artifacts
				WHERE upload_intent_id = $1
			`, fixture.biometricIntentID).Scan(&artifactCount); err != nil {
				t.Fatalf("count stale Biometric Verification Artifacts: %v", err)
			}
			if err := database.QueryRow(ctx, `
				SELECT count(*)
				FROM session_events
				WHERE session_id = $1
				  AND event_type = 'confirm_biometric_capture'
			`, fixture.sessionID).Scan(&eventCount); err != nil {
				t.Fatalf("count stale Biometric Session Events: %v", err)
			}
			if artifactCount != 0 || eventCount != 0 {
				t.Errorf(
					"stale partial writes = artifacts:%d events:%d, want 0/0",
					artifactCount,
					eventCount,
				)
			}
		})
	}
}

type mutatingBiometricPostgresObjectStorage struct {
	t       *testing.T
	wantKey string
	mutate  func(context.Context) error
	calls   int
}

func (s *mutatingBiometricPostgresObjectStorage) HeadObject(
	ctx context.Context,
	storageKey string,
) (artifact.ObjectMetadata, error) {
	s.calls++
	if storageKey != s.wantKey {
		s.t.Errorf("HeadObject() key = %q, want %q", storageKey, s.wantKey)
	}
	if err := s.mutate(ctx); err != nil {
		s.t.Fatalf("mutate PostgreSQL state during HeadObject: %v", err)
	}
	return artifact.ObjectMetadata{
		ContentType: "image/jpeg",
		SizeBytes:   4096,
		ETag:        "stale-biometric-etag",
	}, nil
}

type biometricPostgresFixture struct {
	sessionID                uuid.UUID
	identityIntentID         uuid.UUID
	identityArtifactID       uuid.UUID
	biometricIntentID        uuid.UUID
	rawToken                 string
	identityKey              string
	biometricKey             string
	identityConfirmedAt      time.Time
	biometricIntentCreatedAt time.Time
	biometricIntentExpiresAt time.Time
	sessionExpiresAt         time.Time
}

func seedBiometricPostgresConfirmationState(
	t *testing.T,
	ctx context.Context,
	database *pgx.Conn,
	now time.Time,
) biometricPostgresFixture {
	t.Helper()

	fixture := biometricPostgresFixture{
		sessionID:                uuid.New(),
		identityIntentID:         uuid.New(),
		identityArtifactID:       uuid.New(),
		biometricIntentID:        uuid.New(),
		rawToken:                 "biometric-postgres-fixture-" + uuid.NewString(),
		identityConfirmedAt:      now.Add(-5 * time.Minute),
		biometricIntentCreatedAt: now.Add(-time.Minute),
		biometricIntentExpiresAt: now.Add(5 * time.Minute),
		sessionExpiresAt:         now.Add(30 * time.Minute),
	}
	fixture.identityKey = fmt.Sprintf(
		"verification-sessions/%s/identity_document/%s",
		fixture.sessionID,
		fixture.identityIntentID,
	)
	fixture.biometricKey = fmt.Sprintf(
		"verification-sessions/%s/biometric_capture/%s",
		fixture.sessionID,
		fixture.biometricIntentID,
	)

	tokens := session.NewProductionCryptoTokens()
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
		fixture.sessionID,
		tokens.Hash(fixture.rawToken),
		now.Add(-10*time.Minute),
		fixture.identityConfirmedAt,
		fixture.sessionExpiresAt,
	); err != nil {
		t.Fatalf("insert fixture Verification Session: %v", err)
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
		VALUES ($1, 'Checkpoint Three', DATE '2000-01-01', '3173000000000008', '', $2)
	`, fixture.sessionID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("insert fixture Personal Details: %v", err)
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
		fixture.identityIntentID,
		fixture.sessionID,
		fixture.identityKey,
		now.Add(-10*time.Minute),
		fixture.identityConfirmedAt,
		now.Add(20*time.Minute),
	); err != nil {
		t.Fatalf("insert fixture confirmed Identity Document Upload Intent: %v", err)
	}

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
		VALUES ($1, $2, $3, 'identity_document', $4, 'image/jpeg', 2048, $5, $6)
	`,
		fixture.identityArtifactID,
		fixture.identityIntentID,
		fixture.sessionID,
		fixture.identityKey,
		"identity-etag-"+fixture.identityIntentID.String(),
		fixture.identityConfirmedAt,
	); err != nil {
		t.Fatalf("insert fixture accepted Identity Document Verification Artifact: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, occurred_at)
		VALUES ($1, 'submit_personal_details', $2)
	`, fixture.sessionID, now.Add(-10*time.Minute)); err != nil {
		t.Fatalf("insert fixture Personal Details Session Event: %v", err)
	}
	if _, err := database.Exec(ctx, `
		INSERT INTO session_events (session_id, event_type, metadata, occurred_at)
		VALUES ($1, 'confirm_identity_document', '{"outcome":"accepted"}', $2)
	`, fixture.sessionID, fixture.identityConfirmedAt); err != nil {
		t.Fatalf("insert fixture Identity Document Session Event: %v", err)
	}

	if _, err := database.Exec(ctx, `
		INSERT INTO upload_intents (
			id,
			verification_session_id,
			kind,
			storage_key,
			created_at,
			latest_status_change_at,
			expires_at
		)
		VALUES ($1, $2, 'biometric_capture', $3, $4, $4, $5)
	`,
		fixture.biometricIntentID,
		fixture.sessionID,
		fixture.biometricKey,
		fixture.biometricIntentCreatedAt,
		fixture.biometricIntentExpiresAt,
	); err != nil {
		t.Fatalf("insert fixture pending Biometric Capture Upload Intent: %v", err)
	}

	return fixture
}
