package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/santosidauruk/lawang-go/internal/adapter/postgres/sqlc"
	"github.com/santosidauruk/lawang-go/internal/application/providersubmission"
)

type ProviderSubmissionReader struct {
	queries *sqlc.Queries
}

func NewProviderSubmissionReader(database sqlc.DBTX) *ProviderSubmissionReader {
	return &ProviderSubmissionReader{queries: sqlc.New(database)}
}

func (r *ProviderSubmissionReader) LoadPersonalDataAndArtifacts(ctx context.Context, sessionID uuid.UUID) (providersubmission.PersonalDetailsAndArtifacts, error) {
	details, err := r.queries.GetPersonalDetailsBySessionID(ctx, sessionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return providersubmission.PersonalDetailsAndArtifacts{}, providersubmission.ErrImmutableRecordsMissing
	}

	if err != nil {
		return providersubmission.PersonalDetailsAndArtifacts{}, err
	}

	artifacts, err := r.queries.GetVerificationArtifactsBySessionId(ctx, sessionID)
	if err != nil {
		return providersubmission.PersonalDetailsAndArtifacts{}, err
	}

	if len(artifacts) != 2 || artifacts[0].Kind != "identity_document" || artifacts[1].Kind != "biometric_capture" {
		return providersubmission.PersonalDetailsAndArtifacts{}, providersubmission.ErrImmutableRecordsMissing
	}

	mapArtifact := func(row sqlc.GetVerificationArtifactsBySessionIdRow) providersubmission.VerificationArtifactMetadata {
		return providersubmission.VerificationArtifactMetadata{
			Kind:        row.Kind,
			StorageKey:  row.StorageKey,
			ContentType: row.ContentType,
			SizeBytes:   row.SizeBytes,
			ETag:        row.Etag,
		}
	}

	return providersubmission.PersonalDetailsAndArtifacts{
		PersonalDetails: providersubmission.PersonalDetails{
			FullName:       details.FullName,
			IdentityNumber: details.IdentityNumber,
			Address:        details.Address,
			DateOfBirth:    details.DateOfBirth.Format("2006-01-02"),
		},
		IdentityDocument: mapArtifact(artifacts[0]),
		BiometricCapture: mapArtifact(artifacts[1]),
	}, nil

}
