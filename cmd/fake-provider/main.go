package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	"github.com/santosidauruk/lawang-go/internal/adapter/providerhttp"
	"github.com/santosidauruk/lawang-go/internal/application/fakeprovider"
	"github.com/santosidauruk/lawang-go/internal/platform/config"
	"github.com/santosidauruk/lawang-go/internal/platform/httpserver"
	"github.com/santosidauruk/lawang-go/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.LoadFake()
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
		return 1
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	listener, err := net.Listen("tcp", cfg.FakeHttpAddress)
	if err != nil {
		logger.Error("HTTP listener failed", "error", err)
		return 1
	}
	if err := serveFakeProvider(ctx, cfg, logger, listener); err != nil {
		logger.Error("fake provider stopped with error", "error", err)
		return 1
	}
	return 0
}

func serveFakeProvider(
	ctx context.Context,
	cfg config.FakeConfig,
	logger *slog.Logger,
	listener net.Listener,
) error {
	scenarioStore := providerhttp.NewScenarioStore()
	callbackSender := providerhttp.NewCallbackSender(cfg.ProviderWebhookSecret)
	service := fakeprovider.NewService(
		scenarioStore,
		callbackSender,
		cfg.CallbackTimeout,
		callbackFailureLogger{logger: logger},
		nil,
	)

	handler := httpapi.NewFakeProviderScenarioHandler(service, scenarioStore)
	server := httpserver.New(cfg.FakeHttpAddress, handler)
	logger.Info("fake provider listening", "address", listener.Addr().String())
	serveErr := httpserver.Run(ctx, server, listener, cfg.ShutdownTimeout)

	callbackShutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.CallbackTimeout)
	defer cancel()

	callbackErr := service.Shutdown(callbackShutdownCtx)
	if err := errors.Join(serveErr, callbackErr); err != nil {
		return err
	}

	logger.Info("fake provider stopped")
	return nil
}

type callbackFailureLogger struct {
	logger *slog.Logger
}

func (l callbackFailureLogger) ReportCallbackFailure(failure fakeprovider.CallbackFailure) {
	l.logger.Error(
		"fake provider callback failed",
		"event_id", failure.EventID,
		"session_id", failure.SessionID,
		"error_kind", callbackErrorKind(failure.Err),
	)
}

func callbackErrorKind(err error) string {
	if errors.Is(err, providerhttp.ErrCallbackNonSuccess) {
		return "non_2xx"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "timeout"
	}
	return "transport"
}
