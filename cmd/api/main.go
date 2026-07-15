package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santosidauruk/lawang-go/internal/adapter/httpapi"
	postgresadapter "github.com/santosidauruk/lawang-go/internal/adapter/postgres"
	"github.com/santosidauruk/lawang-go/internal/application/session"
	"github.com/santosidauruk/lawang-go/internal/platform/config"
	"github.com/santosidauruk/lawang-go/internal/platform/httpserver"
	"github.com/santosidauruk/lawang-go/internal/platform/logging"
)

func main() {
	os.Exit(run())
}

func run() int {
	bootstrapLogger := logging.New(os.Stderr, slog.LevelInfo)
	cfg, err := config.Load()
	if err != nil {
		bootstrapLogger.Error("invalid configuration", "error", err)
		return 1
	}

	logger := logging.New(os.Stdout, cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	database, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database configuration failed")
		return 1
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		logger.Error("database unavailable")
		return 1
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddress)
	if err != nil {
		logger.Error("HTTP listener failed", "error", err)
		return 1
	}

	store := postgresadapter.NewSessionStore(database)
	sessions := session.NewService(store, session.NewProductionCryptoTokens(), systemClock{})
	server := httpserver.New(cfg.HTTPAddress, httpapi.NewHandler(sessions))
	logger.Info("API listening", "address", listener.Addr().String())
	if err := httpserver.Run(ctx, server, listener, cfg.ShutdownTimeout); err != nil {
		logger.Error("API stopped with error", "error", err)
		return 1
	}
	logger.Info("API stopped")
	return 0
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
