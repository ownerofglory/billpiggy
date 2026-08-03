package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/config"
	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/health"
)

// @title			BillPiggy API
// @version		1.0
// @description	API for personal cost tracking.
// @BasePath		/
func main() {
	// Config parsing
	var cfg config.BillPiggyAppConfig
	err := env.Parse(&cfg)
	if err != nil {
		slog.Error("Failed to parse config", "error", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel(cfg.LogLevel)})))
	slog.Info("starting app")

	// Chi setup
	r := chi.NewRouter()
	healthRegistry := health.NewRegistry()
	identityRepository, expenseRepository, eventStore, closeRepository := applicationStores(cfg, healthRegistry)
	defer closeRepository()
	authService, err := service.NewAuthService(identityRepository, service.AuthConfig{
		JWTSecret: cfg.JWTSecret, BootstrapSuperAdminEmail: cfg.BootstrapSuperAdminEmail, BootstrapSuperAdminPassword: cfg.BootstrapSuperAdminPassword,
	})
	if err != nil {
		slog.Error("configure authentication", "error", err)
		os.Exit(1)
	}
	if err := authService.EnsureBootstrapSuperAdmin(context.Background()); err != nil {
		slog.Error("bootstrap authentication", "error", err)
		os.Exit(1)
	}
	expenseService, err := service.NewExpenseService(expenseRepository, eventStore)
	if err != nil {
		slog.Error("configure expenses", "error", err)
		os.Exit(1)
	}

	// HTTP handler setup
	r.Get(handler.GetVersionPath, handler.HandleGetVersion)
	handler.RegisterAuthRoutes(r, authService, cfg.Environment == "production")
	handler.RegisterExpenseRoutes(r, expenseService, handler.NewAuthMiddleware(authService))
	handler.RegisterAssistantRoutes(r, handler.NewAuthMiddleware(authService))
	r.Get("/livez", healthRegistry.Live)
	r.Get("/readyz", healthRegistry.Ready)
	r.Get("/startupz", healthRegistry.Startup)
	r.Get("/metrics", healthRegistry.Metrics)
	healthRegistry.MarkStarted()

	httpServer := http.Server{
		Addr:    cfg.ServerAddr,
		Handler: r,
	}

	go func() {
		slog.Info("Starting HTTP Server")

		err := httpServer.ListenAndServe()

		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Server shutdown unexpected", "err", err)
		}

		slog.Info("HTTP Server finished")
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error:", "err", err)
	}

	slog.Info("App finished")
}

func applicationStores(cfg config.BillPiggyAppConfig, healthRegistry *health.Registry) (outbound.IdentityRepository, outbound.ExpenseRepository, outbound.EventStore, func()) {
	if cfg.DatabaseURL == "" {
		slog.Warn("using in-memory identity storage; set DATABASE_URL for persistent data")
		return memory.NewIdentityRepository(), memory.NewExpenseRepository(), memory.NewEventStore(), func() {}
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	identity := postgresadapter.NewIdentityRepository(pool)
	healthRegistry.Register("postgres", identity.Ping)
	return identity, postgresadapter.NewExpenseRepository(pool), postgresadapter.NewEventStore(pool), pool.Close
}

func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
