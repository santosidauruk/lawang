package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
	"github.com/santosidauruk/lawang-go/internal/platform/config"
)

func TestCallbackFailureLoggerDoesNotExposeSensitiveError(t *testing.T) {
	const sensitive = "https://secret.example.test/callback?token=secret"
	eventID := uuid.MustParse("10907121-d6c4-442b-b5bf-1f62a14c8dbc")
	sessionID := uuid.MustParse("d1fb6175-9b87-44ba-9a2a-dbd60073f20d")
	var output bytes.Buffer
	reporter := callbackFailureLogger{
		logger: slog.New(slog.NewJSONHandler(&output, nil)),
	}

	reporter.ReportCallbackFailure(fakeprovider.CallbackFailure{
		EventID:   eventID,
		SessionID: sessionID,
		Err:       errors.New("POST " + sensitive + ": callback body rejected"),
	})

	logLine := output.String()
	if strings.Contains(logLine, sensitive) || strings.Contains(logLine, "callback body rejected") {
		t.Fatalf("callback failure log exposes sensitive error: %s", logLine)
	}
	for _, want := range []string{
		`"msg":"fake provider callback failed"`,
		`"event_id":"` + eventID.String() + `"`,
		`"session_id":"` + sessionID.String() + `"`,
		`"error_kind":"transport"`,
	} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("callback failure log = %s, want field %s", logLine, want)
		}
	}
}

func TestFakeProviderProcessDefaultVerifiedSubmissionOnActualListener(t *testing.T) {
	const webhookSecret = "process-level-fixed-webhook-secret"
	sessionID := uuid.MustParse("893a970d-822c-49c6-86ba-141a967ab367")
	type recordedCallback struct {
		header http.Header
		body   []byte
	}
	callbacks := make(chan recordedCallback, 1)
	callbackServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(response, "read callback", http.StatusBadRequest)
			return
		}
		callbacks <- recordedCallback{header: request.Header.Clone(), body: body}
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(callbackServer.Close)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for fake-provider process: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveResults := make(chan error, 1)
	go func() {
		serveResults <- serveFakeProvider(
			ctx,
			config.FakeConfig{
				FakeHttpAddress:       listener.Addr().String(),
				CallbackTimeout:       time.Second,
				ShutdownTimeout:       time.Second,
				LogLevel:              slog.LevelInfo,
				ProviderWebhookSecret: webhookSecret,
			},
			slog.New(slog.NewJSONHandler(io.Discard, nil)),
			listener,
		)
	}()

	submission := fakeprovider.ProviderSubmissionRequest{
		SessionID:   sessionID,
		CallbackURL: callbackServer.URL + "/webhooks/verification",
		PersonalDetails: fakeprovider.PersonalDetails{
			FullName:       "Process Tracer",
			DateOfBirth:    "2000-01-01",
			IdentityNumber: "3173000000000016",
			Address:        "jalan listener",
		},
		IdentityDocument: fakeprovider.VerificationArtifactMetadata{
			Kind: "identity_document", StorageKey: "process/identity", ContentType: "image/jpeg", SizeBytes: 1, ETag: "identity-etag",
		},
		BiometricCapture: fakeprovider.VerificationArtifactMetadata{
			Kind: "biometric_capture", StorageKey: "process/biometric", ContentType: "image/png", SizeBytes: 1, ETag: "biometric-etag",
		},
	}
	body, err := json.Marshal(submission)
	if err != nil {
		t.Fatalf("marshal Provider Submission: %v", err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+listener.Addr().String()+"/",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("create Provider Submission request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", sessionID.String())
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("submit to fake-provider process: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("submission status = %d, want 2xx", response.StatusCode)
	}

	var callback recordedCallback
	select {
	case callback = <-callbacks:
	case <-time.After(2 * time.Second):
		t.Fatal("fake-provider process did not send callback")
	}
	var event struct {
		EventID   uuid.UUID `json:"eventId"`
		SessionID uuid.UUID `json:"sessionId"`
		Verdict   string    `json:"verdict"`
	}
	decoder := json.NewDecoder(bytes.NewReader(callback.body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		t.Fatalf("decode exact callback: %v", err)
	}
	if event.EventID == uuid.Nil || event.SessionID != sessionID || event.Verdict != "verified" {
		t.Fatalf("callback event = %#v", event)
	}
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	_, _ = mac.Write(callback.body)
	wantSignature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if callback.header.Get("x-signature") != wantSignature {
		t.Fatal("callback signature does not authenticate exact transmitted body")
	}

	cancel()
	select {
	case err := <-serveResults:
		if err != nil {
			t.Fatalf("fake-provider graceful shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("fake-provider process did not shut down")
	}
}
