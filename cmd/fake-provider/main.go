package main

import (
	"context"
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

	scenarioStore := providerhttp.NewScenarioStore()
	callbackSender := providerhttp.NewCallbackSender(cfg.ProviderWebhookSecret)
	service := fakeprovider.NewService(scenarioStore, callbackSender, cfg.CallbackTimeout)

	handler := httpapi.NewFakeProviderScenarioHandler(service, scenarioStore)
	server := httpserver.New(cfg.FakeHttpAddress, handler)
	logger.Info("API listening", "address", listener.Addr().String())
	if err := httpserver.Run(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		logger.Error("API stopped with error", "error", err)
		return 1
	}
	logger.Info("API stopped")
	return 0
}
