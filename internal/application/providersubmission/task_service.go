package providersubmission

import (
	"context"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/session"
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
	LoadPersonalDataAndArtifacts(sessionID uuid.UUID) (PersonalDetailsAndArtifacts, error)
}

type Provider interface {
	Send(request ProviderSubmissionRequest) error
}

type TaskService struct {
	provider Provider
	reader   Reader
	clock    session.Clock
}

func NewTaskService(reader Reader, provider Provider, clock session.Clock) *TaskService {
	return &TaskService{
		reader:   reader,
		provider: provider,
		clock:    clock,
	}
}

func (t *TaskService) SendToProvider(ctx context.Context, sessionID uuid.UUID, callbackURL string) error {
	// manggil adapter 2 dan 3
	// manggil adapter 5
	detailArtifacts, err := t.reader.LoadPersonalDataAndArtifacts(sessionID)
	if err != nil {
		return err
	}

	providerReq := ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: callbackURL,
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

	err = t.provider.Send(providerReq)
	if err != nil {
		return err
	}

	return nil

}
