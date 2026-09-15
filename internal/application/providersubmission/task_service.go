package providersubmission

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/google/uuid"
)

var (
	ErrImmutableRecordsMissing = errors.New("provider submission records are incomplete")
	ErrTransientProvider       = errors.New("provider submission temporarily failed")
	ErrPermanentProvider       = errors.New("provider submission permanently failed")
	ErrInvalidTask             = errors.New("invalid provider submission task")
)

type ProviderSubmissionRequest struct {
	SessionID        uuid.UUID                    `json:"sessionId"`
	CallbackURL      string                       `json:"callbackUrl"`
	PersonalDetails  PersonalDetails              `json:"personalDetails"`
	IdentityDocument VerificationArtifactMetadata `json:"identityDocument"`
	BiometricCapture VerificationArtifactMetadata `json:"biometricCapture"`
}

type PersonalDetails struct {
	FullName       string `json:"fullName"`
	DateOfBirth    string `json:"dateOfBirth"`
	IdentityNumber string `json:"identityNumber"`
	Address        string `json:"address"`
}

type VerificationArtifactMetadata struct {
	Kind        string `json:"kind"`
	StorageKey  string `json:"storageKey"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
	ETag        string `json:"eTag"`
}

type PersonalDetailsAndArtifacts struct {
	PersonalDetails  PersonalDetails
	IdentityDocument VerificationArtifactMetadata
	BiometricCapture VerificationArtifactMetadata
}

type Reader interface {
	LoadPersonalDataAndArtifacts(ctx context.Context, sessionID uuid.UUID) (PersonalDetailsAndArtifacts, error)
}

type Provider interface {
	Send(ctx context.Context, request ProviderSubmissionRequest) error
}

type TaskService struct {
	provider    Provider
	reader      Reader
	callbackURL string
}

func NewTaskService(reader Reader, provider Provider, callbackURL string) (*TaskService, error) {
	parsedURL, err := url.ParseRequestURI(callbackURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("%w: callback URL", ErrPermanentProvider)
	}
	return &TaskService{
		reader:      reader,
		provider:    provider,
		callbackURL: callbackURL,
	}, nil
}

func (t *TaskService) SendToProvider(ctx context.Context, sessionID uuid.UUID) error {
	// manggil adapter 2 dan 3
	// manggil adapter 5
	if sessionID == uuid.Nil {
		return fmt.Errorf("missing session ID: %w", ErrInvalidTask)
	}
	detailArtifacts, err := t.reader.LoadPersonalDataAndArtifacts(ctx, sessionID)
	if err != nil {
		return err
	}

	providerReq := ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: t.callbackURL,
		PersonalDetails: PersonalDetails{
			FullName:       detailArtifacts.PersonalDetails.FullName,
			DateOfBirth:    detailArtifacts.PersonalDetails.DateOfBirth,
			IdentityNumber: detailArtifacts.PersonalDetails.IdentityNumber,
			Address:        detailArtifacts.PersonalDetails.Address,
		},
		IdentityDocument: VerificationArtifactMetadata{
			Kind:        detailArtifacts.IdentityDocument.Kind,
			StorageKey:  detailArtifacts.IdentityDocument.StorageKey,
			ContentType: detailArtifacts.IdentityDocument.ContentType,
			SizeBytes:   detailArtifacts.IdentityDocument.SizeBytes,
			ETag:        detailArtifacts.IdentityDocument.ETag,
		},
		BiometricCapture: VerificationArtifactMetadata{
			Kind:        detailArtifacts.BiometricCapture.Kind,
			StorageKey:  detailArtifacts.BiometricCapture.StorageKey,
			ContentType: detailArtifacts.BiometricCapture.ContentType,
			SizeBytes:   detailArtifacts.BiometricCapture.SizeBytes,
			ETag:        detailArtifacts.BiometricCapture.ETag,
		},
	}

	return t.provider.Send(ctx, providerReq)
}
