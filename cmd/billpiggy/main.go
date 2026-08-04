package main

import (
	"context"
	"errors"
	"fmt"
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
	openaiadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/openai"
	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	"github.com/ownerofglory/billpiggy/pkg/email"
	"github.com/ownerofglory/billpiggy/pkg/health"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// assistantRateLimit is how many assistant questions one user may ask per
// window. It is deliberately generous for a family-sized deployment but still
// bounded, since every question is a paid provider call.
const (
	assistantRateLimit    = 10
	assistantRateInterval = time.Minute
)

// subscriptionRegistrar registers a durable outbox subscription, backfilling it
// with existing events the first time it is seen.
type subscriptionRegistrar interface {
	EnsureSubscription(ctx context.Context, name string) error
}

// stores holds every outbound adapter the application is wired with, so the
// PostgreSQL and in-memory configurations differ in one place only.
type stores struct {
	unit          outbound.UnitOfWork
	identity      outbound.IdentityRepository
	expenses      outbound.ExpenseRepository
	budgets       outbound.BudgetRepository
	groups        outbound.GroupRepository
	taxonomy      outbound.TaxonomyRepository
	analytics     outbound.AnalyticsRepository
	budgetUsage   outbound.BudgetUsageRepository
	audit         outbound.AuditRepository
	notifications outbound.NotificationRepository
	objectRefs    outbound.ObjectReferenceRepository
	aiRequests    outbound.AIRequestRepository
	events        outbound.EventStore
	outboxStore   outbox.Store
	subscriptions subscriptionRegistrar
	// pool is non-nil only in the PostgreSQL configuration. It backs the
	// durable rate limiter and its cleanup; nothing else should reach for it
	// directly, since every other outbound need already has a port above.
	pool  *pgxpool.Pool
	close func()
}

// newLimiter builds a fixed-window limiter for one AI workload. It is durable
// and shared across replicas when PostgreSQL is configured, and falls back to
// an in-memory, process-local limiter otherwise.
func (s stores) newLimiter(limit int, interval time.Duration) ratelimit.Limiter {
	if s.pool == nil {
		return ratelimit.NewFixedWindow(limit, interval)
	}
	return postgresadapter.NewRateLimiter(s.pool, limit, interval)
}

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

	ctx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	r := chi.NewRouter()
	healthRegistry := health.NewRegistry()
	adapters := applicationStores(cfg, healthRegistry)
	defer adapters.close()

	objectStore, err := applicationObjectStore(cfg, healthRegistry)
	if err != nil {
		slog.Error("configure object storage", "error", err)
		os.Exit(1)
	}
	authService, err := service.NewAuthService(adapters.identity, service.AuthConfig{
		JWTSecret: cfg.JWTSecret, BootstrapSuperAdminEmail: cfg.BootstrapSuperAdminEmail, BootstrapSuperAdminPassword: cfg.BootstrapSuperAdminPassword,
	})
	if err != nil {
		slog.Error("configure authentication", "error", err)
		os.Exit(1)
	}
	authService = authService.WithObjectReferences(adapters.objectRefs)
	if err := authService.EnsureBootstrapSuperAdmin(ctx); err != nil {
		slog.Error("bootstrap authentication", "error", err)
		os.Exit(1)
	}
	expenseService, err := service.NewExpenseService(adapters.expenses, adapters.events, adapters.unit)
	if err != nil {
		slog.Error("configure expenses", "error", err)
		os.Exit(1)
	}
	expenseService = expenseService.WithObjectReferences(adapters.objectRefs)
	groupService, err := service.NewGroupService(adapters.groups)
	if err != nil {
		slog.Error("configure groups", "error", err)
		os.Exit(1)
	}
	budgetService, err := service.NewBudgetService(adapters.budgets, adapters.events, adapters.groups, adapters.unit)
	if err != nil {
		slog.Error("configure budgets", "error", err)
		os.Exit(1)
	}
	analyticsService, err := service.NewAnalyticsService(adapters.analytics, adapters.budgets)
	if err != nil {
		slog.Error("configure analytics", "error", err)
		os.Exit(1)
	}
	taxonomyService, err := service.NewTaxonomyService(adapters.taxonomy)
	if err != nil {
		slog.Error("configure taxonomy", "error", err)
		os.Exit(1)
	}
	if err := startProjections(ctx, adapters, healthRegistry); err != nil {
		slog.Error("configure projections", "error", err)
		os.Exit(1)
	}
	if cfg.SMTPAddress != "" {
		notifications, err := service.NewNotificationService(adapters.notifications)
		if err != nil {
			slog.Error("configure notifications", "error", err)
			os.Exit(1)
		}
		sender, err := email.NewSMTPSender(cfg.SMTPAddress, cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPFrom)
		if err != nil {
			slog.Error("configure smtp", "error", err)
			os.Exit(1)
		}
		go deliverNotifications(ctx, notifications, adapters.identity, sender)
	}
	retentionService, err := service.NewRetentionService(adapters.objectRefs, objectStore)
	if err != nil {
		slog.Error("configure retention", "error", err)
		os.Exit(1)
	}
	go sweepOrphanedObjects(ctx, retentionService)
	if adapters.pool != nil {
		go cleanupRateLimitWindows(ctx, postgresadapter.NewRateLimiter(adapters.pool, 0, 0))
	}
	var assistantService *service.AssistantService
	if cfg.OpenAIAPIKey != "" {
		client, err := openaiadapter.NewClient(cfg.OpenAIAPIKey,
			openaiadapter.WithModel(cfg.OpenAIAssistantModel),
			openaiadapter.WithBaseURL(cfg.OpenAIBaseURL),
			openaiadapter.WithLogger(slog.Default()))
		if err != nil {
			slog.Error("configure OpenAI client", "error", err)
			os.Exit(1)
		}
		auditedProvider, err := service.NewAuditedAIProvider(client, adapters.aiRequests, domain.AIWorkloadAssistant)
		if err != nil {
			slog.Error("configure AI request auditing", "error", err)
			os.Exit(1)
		}
		assistantService, err = service.NewAssistantService(auditedProvider, adapters.expenses, adapters.budgets)
		if err != nil {
			slog.Error("configure assistant", "error", err)
			os.Exit(1)
		}
		assistantService = assistantService.
			WithModel(cfg.OpenAIAssistantModel).
			WithLimiter(adapters.newLimiter(assistantRateLimit, assistantRateInterval))
	}
	var intakeService *service.ExpenseIntakeService
	if cfg.OpenAIAPIKey != "" {
		client, err := openaiadapter.NewClient(cfg.OpenAIAPIKey, openaiadapter.WithBaseURL(cfg.OpenAIBaseURL), openaiadapter.WithLogger(slog.Default()))
		if err != nil {
			slog.Error("configure OpenAI client for expense intake", "error", err)
			os.Exit(1)
		}
		auditedProvider, err := service.NewAuditedAIProvider(client, adapters.aiRequests, domain.AIWorkloadReceiptExtraction)
		if err != nil {
			slog.Error("configure AI request auditing", "error", err)
			os.Exit(1)
		}
		auditedTranscriber, err := service.NewAuditedAudioTranscriber(client, adapters.aiRequests)
		if err != nil {
			slog.Error("configure transcription auditing", "error", err)
			os.Exit(1)
		}
		intakeService, err = service.NewExpenseIntakeService(auditedProvider, auditedTranscriber)
		if err != nil {
			slog.Error("configure expense intake", "error", err)
			os.Exit(1)
		}
		intakeService = intakeService.WithLimiter(adapters.newLimiter(service.IntakeRateLimit, service.IntakeRateInterval))
	}

	// HTTP handler setup
	r.Get(handler.GetVersionPath, handler.HandleGetVersion)
	handler.RegisterAuthRoutes(r, authService, cfg.Environment == "production")
	handler.RegisterUserRoutes(r, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterUploadRoutes(r, authService, expenseService, objectStore, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseRoutes(r, expenseService, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseIntakeRoutes(r, intakeService, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterBudgetRoutes(r, budgetService, handler.NewAuthMiddleware(authService))
	handler.RegisterAnalyticsRoutes(r, analyticsService, handler.NewAuthMiddleware(authService))
	handler.RegisterTaxonomyRoutes(r, taxonomyService, handler.NewAuthMiddleware(authService))
	handler.RegisterGroupRoutes(r, groupService, handler.NewAuthMiddleware(authService))
	handler.RegisterAssistantRoutes(r, assistantService, authService, handler.NewAuthMiddleware(authService))
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

	<-ctx.Done()

	shutdownCtx, shutdownRelease := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownRelease()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP shutdown error:", "err", err)
	}

	slog.Info("App finished")
}

// startProjections registers every outbox subscription and runs an engine for
// each, exposing its progress as a readiness check.
//
// A subscription that has never run backfills automatically: registering it
// enqueues the existing event history, which then drains through exactly the
// same engine and handler as live traffic.
func startProjections(ctx context.Context, adapters stores, healthRegistry *health.Registry) error {
	analyticsProjection, err := service.NewAnalyticsProjection(adapters.analytics)
	if err != nil {
		return fmt.Errorf("analytics projection: %w", err)
	}
	budgetUsageProjection, err := service.NewBudgetUsageProjection(adapters.budgetUsage, adapters.notifications)
	if err != nil {
		return fmt.Errorf("budget usage projection: %w", err)
	}
	auditProjection, err := service.NewAuditProjection(adapters.audit)
	if err != nil {
		return fmt.Errorf("audit projection: %w", err)
	}
	for _, projection := range []outbox.Handler{analyticsProjection, budgetUsageProjection, auditProjection} {
		if err := adapters.subscriptions.EnsureSubscription(ctx, projection.Name()); err != nil {
			return fmt.Errorf("register subscription %s: %w", projection.Name(), err)
		}
		engine, err := outbox.NewEngine(adapters.outboxStore, projection, outbox.Options{
			Policy:       outbox.DefaultPolicy(),
			IdleInterval: 2 * time.Second,
			Logger:       slog.Default(),
		})
		if err != nil {
			return fmt.Errorf("build engine %s: %w", projection.Name(), err)
		}
		healthRegistry.Register("projector_"+projection.Name(), engine.Health(2*time.Minute))
		go func(engine *outbox.Engine) {
			if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("projection engine stopped", "subscription", engine.Name(), "error", err)
			}
		}(engine)
	}
	return nil
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

// applicationStores builds the outbound adapters, choosing PostgreSQL when a
// database URL is configured and in-memory equivalents otherwise.
func applicationStores(cfg config.BillPiggyAppConfig, healthRegistry *health.Registry) stores {
	if cfg.DatabaseURL == "" {
		slog.Warn("using in-memory storage; set DATABASE_URL for persistent data")
		return memoryStores()
	}
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect postgres", "error", err)
		os.Exit(1)
	}
	identity := postgresadapter.NewIdentityRepository(pool)
	healthRegistry.Register("postgres", identity.Ping)
	outboxStore := postgresadapter.NewOutboxStore(pool)
	return stores{
		unit:          pgxtx.NewRunner(pool),
		identity:      identity,
		expenses:      postgresadapter.NewExpenseRepository(pool),
		budgets:       postgresadapter.NewBudgetRepository(pool),
		groups:        postgresadapter.NewGroupRepository(pool),
		taxonomy:      postgresadapter.NewTaxonomyRepository(pool),
		analytics:     postgresadapter.NewAnalyticsRepository(pool),
		budgetUsage:   postgresadapter.NewBudgetUsageRepository(pool),
		audit:         postgresadapter.NewAuditRepository(pool),
		notifications: postgresadapter.NewNotificationRepository(pool),
		objectRefs:    postgresadapter.NewObjectReferenceRepository(pool),
		aiRequests:    postgresadapter.NewAIRequestRepository(pool),
		events:        postgresadapter.NewEventStore(pool),
		outboxStore:   outboxStore,
		subscriptions: outboxStore,
		pool:          pool,
		close:         pool.Close,
	}
}

// memoryStores wires the in-memory adapters, including a unit of work that
// gives them the same all-or-nothing semantics PostgreSQL provides.
func memoryStores() stores {
	identity := memory.NewIdentityRepository()
	expenses := memory.NewExpenseRepository()
	budgets := memory.NewBudgetRepository()
	groups := memory.NewGroupRepository()
	taxonomy := memory.NewTaxonomyRepository()
	analytics := memory.NewAnalyticsRepository()
	budgetUsage := memory.NewBudgetUsageRepository(budgets)
	audit := memory.NewAuditRepository()
	notifications := memory.NewNotificationRepository()
	objectRefs := memory.NewObjectReferenceRepository()
	aiRequests := memory.NewAIRequestRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenses, budgets, analytics, budgetUsage, audit, notifications, objectRefs, taxonomy, events)
	events.WithUnitOfWork(unit)
	return stores{
		unit:          unit,
		identity:      identity,
		expenses:      expenses,
		budgets:       budgets,
		groups:        groups,
		taxonomy:      taxonomy,
		analytics:     analytics,
		budgetUsage:   budgetUsage,
		audit:         audit,
		notifications: notifications,
		objectRefs:    objectRefs,
		aiRequests:    aiRequests,
		events:        events,
		outboxStore:   events,
		subscriptions: events,
		close:         func() {},
	}
}

// sweepOrphanedObjects periodically reclaims objects that AttachReceipt,
// UpdateProfileImage or a resource deletion has orphaned.
func sweepOrphanedObjects(ctx context.Context, retention *service.RetentionService) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		if swept, err := retention.SweepOrphans(ctx, 50); err != nil {
			slog.Error("sweep orphaned objects", "error", err)
		} else if swept > 0 {
			slog.Info("swept orphaned objects", "count", swept)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// cleanupRateLimitWindows periodically deletes closed rate-limit windows so
// ratelimit.windows does not grow without bound. The retain period must stay
// well above every limiter's interval configured against this table; a day
// comfortably covers the per-minute and per-day AI limits in use.
func cleanupRateLimitWindows(ctx context.Context, limiter *postgresadapter.RateLimiter) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if removed, err := limiter.CleanupExpired(ctx, 24*time.Hour); err != nil {
			slog.Error("clean up rate limit windows", "error", err)
		} else if removed > 0 {
			slog.Info("cleaned up rate limit windows", "count", removed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func deliverNotifications(ctx context.Context, notifications *service.NotificationService, users outbound.IdentityRepository, sender service.EmailSender) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := notifications.DeliverPending(ctx, users, sender, 25); err != nil {
			slog.Error("deliver notifications", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
