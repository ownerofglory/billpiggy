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
	minioadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/minio"
	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/email"
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
	identityRepository, expenseRepository, budgetRepository, groupRepository, taxonomyRepository, analyticsRepository, notificationRepository, eventStore, projectEvents, closeRepository := applicationStores(cfg, healthRegistry)
	defer closeRepository()
	objectStore, err := applicationObjectStore(cfg, healthRegistry)
	if err != nil {
		slog.Error("configure object storage", "error", err)
		os.Exit(1)
	}
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
	groupService, err := service.NewGroupService(groupRepository)
	if err != nil {
		slog.Error("configure groups", "error", err)
		os.Exit(1)
	}
	budgetService, err := service.NewBudgetService(budgetRepository, eventStore, groupRepository)
	if err != nil {
		slog.Error("configure budgets", "error", err)
		os.Exit(1)
	}
	analyticsService, err := service.NewAnalyticsService(analyticsRepository, budgetRepository)
	if err != nil {
		slog.Error("configure analytics", "error", err)
		os.Exit(1)
	}
	taxonomyService, err := service.NewTaxonomyService(taxonomyRepository)
	if err != nil {
		slog.Error("configure taxonomy", "error", err)
		os.Exit(1)
	}
	if cfg.SMTPAddress != "" {
		notifications, err := service.NewNotificationService(notificationRepository)
		if err != nil {
			slog.Error("configure notifications", "error", err)
			os.Exit(1)
		}
		sender, err := email.NewSMTPSender(cfg.SMTPAddress, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)
		if err != nil {
			slog.Error("configure smtp", "error", err)
			os.Exit(1)
		}
		go deliverNotifications(notifications, identityRepository, sender)
	}
	if projectEvents != nil {
		go runExpenseProjector(projectEvents)
	}

	// HTTP handler setup
	r.Get(handler.GetVersionPath, handler.HandleGetVersion)
	handler.RegisterAuthRoutes(r, authService, cfg.Environment == "production")
	handler.RegisterUserRoutes(r, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterUploadRoutes(r, authService, expenseService, objectStore, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseRoutes(r, expenseService, handler.NewAuthMiddleware(authService))
	handler.RegisterBudgetRoutes(r, budgetService, handler.NewAuthMiddleware(authService))
	handler.RegisterAnalyticsRoutes(r, analyticsService, handler.NewAuthMiddleware(authService))
	handler.RegisterTaxonomyRoutes(r, taxonomyService, handler.NewAuthMiddleware(authService))
	handler.RegisterGroupRoutes(r, groupService, handler.NewAuthMiddleware(authService))
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

func applicationObjectStore(cfg config.BillPiggyAppConfig, healthRegistry *health.Registry) (outbound.ObjectStore, error) {
	if cfg.MinIOEndpoint == "" {
		return memory.NewObjectStore(), nil
	}
	store, err := minioadapter.NewObjectStore(cfg.MinIOEndpoint, cfg.MinIOAccessKey, cfg.MinIOSecretKey, cfg.MinIOBucket, cfg.MinIOUseSSL)
	if err != nil {
		return nil, err
	}
	healthRegistry.Register("object_storage", store.Ping)
	return store, nil
}

func applicationStores(cfg config.BillPiggyAppConfig, healthRegistry *health.Registry) (outbound.IdentityRepository, outbound.ExpenseRepository, outbound.BudgetRepository, outbound.GroupRepository, outbound.TaxonomyRepository, outbound.AnalyticsRepository, outbound.NotificationRepository, outbound.EventStore, func(context.Context) (int, error), func()) {
	if cfg.DatabaseURL == "" {
		slog.Warn("using in-memory identity storage; set DATABASE_URL for persistent data")
		return memory.NewIdentityRepository(), memory.NewExpenseRepository(), memory.NewBudgetRepository(), memory.NewGroupRepository(), memory.NewTaxonomyRepository(), memory.NewAnalyticsRepository(), memory.NewNotificationRepository(), memory.NewEventStore(), nil, func() {}
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	identity := postgresadapter.NewIdentityRepository(pool)
	healthRegistry.Register("postgres", identity.Ping)
	projector := postgresadapter.NewExpenseProjector(pool)
	return identity, postgresadapter.NewExpenseRepository(pool), postgresadapter.NewBudgetRepository(pool), postgresadapter.NewGroupRepository(pool), postgresadapter.NewTaxonomyRepository(pool), postgresadapter.NewAnalyticsRepository(pool), postgresadapter.NewNotificationRepository(pool), postgresadapter.NewEventStore(pool), func(ctx context.Context) (int, error) { return projector.ProjectPending(ctx, 50) }, pool.Close
}

func runExpenseProjector(project func(context.Context) (int, error)) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if _, err := project(context.Background()); err != nil {
			slog.Error("project expense events", "error", err)
		}
		<-ticker.C
	}
}

func deliverNotifications(notifications *service.NotificationService, users outbound.IdentityRepository, sender service.EmailSender) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := notifications.DeliverPending(context.Background(), users, sender, 25); err != nil {
			slog.Error("deliver notifications", "error", err)
		}
		<-ticker.C
	}
}

func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
