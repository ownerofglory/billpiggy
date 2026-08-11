package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ownerofglory/billpiggy/config"
	"github.com/ownerofglory/billpiggy/internal/adapter/inbound/http/v1/handler"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/cached"
	"github.com/ownerofglory/billpiggy/internal/adapter/outbound/memory"
	minioadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/minio"
	openaiadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/openai"
	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/inbound"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/internal/core/service"
	sharedauth "github.com/ownerofglory/billpiggy/pkg/auth"
	"github.com/ownerofglory/billpiggy/pkg/cors"
	"github.com/ownerofglory/billpiggy/pkg/email"
	"github.com/ownerofglory/billpiggy/pkg/health"
	"github.com/ownerofglory/billpiggy/pkg/metrics"
	"github.com/ownerofglory/billpiggy/pkg/outbox"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
	"github.com/ownerofglory/billpiggy/pkg/ratelimit"
)

// latestMigrationVersion is the newest migration this build expects to find
// applied. It backs the "migrations applied" readiness check and must be
// bumped alongside every new migrations/NNNNNN_*.up.sql file.
const latestMigrationVersion = "000015_notification_preferences"

// cacheTTL bounds how stale a cached user, category, tag, or group list may
// be before the next read re-fetches it. Short enough that an admin change
// (blocking a user, renaming a category) is visible well within a session.
const cacheTTL = 30 * time.Second

// assistantRateLimit is how many assistant questions one user may ask per
// window. It is deliberately generous for a family-sized deployment but still
// bounded, since every question is a paid provider call.
const (
	assistantRateLimit    = 10
	assistantRateInterval = time.Minute
)

// mailerSendQuotaWindow is the rolling window MailerSendMonthlyLimit is
// enforced over. A rolling 30 days rather than a calendar month, since the
// limiter has no notion of calendar boundaries — close enough to keep the
// account within its plan without needing calendar-aware bookkeeping.
const mailerSendQuotaWindow = 30 * 24 * time.Hour

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
	reports       outbound.ReportRepository
	payments      outbound.ScheduledPaymentRepository
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

// @title						BillPiggy API
// @version					1.0
// @description				API for personal cost tracking.
// @BasePath					/
// @securityDefinitions.apikey	ApiKeyAuth
// @in							header
// @name						Authorization
// @description				Type "Bearer" followed by a space and the access token from POST /auth/login or POST /auth/refresh.
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
	appMetrics := metrics.NewRegistry()
	healthRegistry.WithMetrics(appMetrics)
	httpRequests := appMetrics.NewCounterVec("billpiggy_http_requests_total", "Total HTTP requests.", "route", "method", "status")
	httpLatency := appMetrics.NewHistogramVec("billpiggy_http_request_duration_seconds", "HTTP request latency in seconds.", metrics.DefaultLatencyBuckets, "route", "method", "status")
	aiCalls := appMetrics.NewCounterVec("billpiggy_ai_requests_total", "Total AI provider calls.", "workload", "outcome")
	aiTokens := appMetrics.NewCounterVec("billpiggy_ai_tokens_total", "Total AI token usage.", "workload", "direction")
	notificationOutcomes := appMetrics.NewCounterVec("billpiggy_notifications_total", "Total resolved notification deliveries.", "kind", "outcome")
	// Registered first so it wraps everything else: a preflight OPTIONS
	// request must get a CORS response before it ever reaches auth or route
	// handling, not a 405 from falling through unmatched.
	r.Use(cors.Middleware(cors.ParseOrigins(cfg.CORSAllowedOrigins)))
	r.Use(metrics.HTTPMiddleware(httpRequests, httpLatency, routePattern))

	adapters := applicationStores(cfg, healthRegistry)
	defer adapters.close()
	healthRegistry.RegisterGauge("billpiggy_active_users", "Non-blocked user accounts.", activeUsersGauge(adapters.identity))

	objectStore, err := applicationObjectStore(cfg, healthRegistry)
	if err != nil {
		slog.Error("configure object storage", "error", err)
		os.Exit(1)
	}
	authService, err := service.NewAuthService(adapters.identity, service.AuthConfig{
		JWTSecret: cfg.JWTSecret, BootstrapSuperAdminEmail: cfg.BootstrapSuperAdminEmail, BootstrapSuperAdminPassword: cfg.BootstrapSuperAdminPassword,
		PublicBaseURL: cfg.PublicBaseURL,
	})
	if err != nil {
		slog.Error("configure authentication", "error", err)
		os.Exit(1)
	}
	authService = authService.WithObjectReferences(adapters.objectRefs).WithNotifications(adapters.notifications)
	if err := authService.EnsureBootstrapSuperAdmin(ctx); err != nil {
		slog.Error("bootstrap authentication", "error", err)
		os.Exit(1)
	}
	expenseService, err := service.NewExpenseService(adapters.expenses, adapters.events, adapters.unit)
	if err != nil {
		slog.Error("configure expenses", "error", err)
		os.Exit(1)
	}
	expenseService = expenseService.WithObjectReferences(adapters.objectRefs).WithGroups(adapters.groups).WithTaxonomy(adapters.taxonomy)
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
	analyticsService, err := service.NewAnalyticsService(adapters.analytics, adapters.budgets, adapters.expenses)
	if err != nil {
		slog.Error("configure analytics", "error", err)
		os.Exit(1)
	}
	taxonomyService, err := service.NewTaxonomyService(adapters.taxonomy)
	if err != nil {
		slog.Error("configure taxonomy", "error", err)
		os.Exit(1)
	}
	auditService, err := service.NewAuditService(adapters.audit)
	if err != nil {
		slog.Error("configure audit", "error", err)
		os.Exit(1)
	}
	adminUsageService, err := service.NewAdminUsageService(adapters.identity, adapters.aiRequests, adapters.notifications, adapters.audit)
	if err != nil {
		slog.Error("configure admin usage", "error", err)
		os.Exit(1)
	}
	if err := startProjections(ctx, adapters, healthRegistry); err != nil {
		slog.Error("configure projections", "error", err)
		os.Exit(1)
	}
	switch {
	case cfg.MailerSendAPIKey != "" && cfg.MailerSendFromEmail != "":
		notifications, err := service.NewNotificationService(adapters.notifications)
		if err != nil {
			slog.Error("configure notifications", "error", err)
			os.Exit(1)
		}
		notifications = notifications.WithMetrics(notificationOutcomes)
		// Both required fields were just checked above, so this can only
		// fail on a future validation this constructor grows; keep it a hard
		// failure rather than silently swallowing a real misconfiguration.
		sender, err := email.NewMailerSendSender(cfg.MailerSendAPIKey, cfg.MailerSendFromEmail, cfg.MailerSendFromName)
		if err != nil {
			slog.Error("configure mailersend", "error", err)
			os.Exit(1)
		}
		limitedSender := email.WithSendLimit(sender, adapters.newLimiter(cfg.MailerSendMonthlyLimit, mailerSendQuotaWindow))
		var lastRun atomic.Int64
		healthRegistry.Register("notification_worker", notificationWorkerHealth(&lastRun))
		go deliverNotifications(ctx, notifications, adapters.identity, limitedSender, &lastRun)
	case cfg.MailerSendAPIKey != "":
		// A key with no sender address can never actually send: MailerSend
		// requires a from address on every request, and there's no
		// account-level default it falls back to. Disabling cleanly here,
		// rather than letting email.NewMailerSendSender's validation error
		// reach the code path below that exits the whole process, is what
		// keeps a single missing var from crashing the entire app on
		// startup over an optional feature.
		slog.Warn("email notifications disabled; MAILERSEND_API_KEY is set but MAILERSEND_FROM_EMAIL is missing")
	default:
		slog.Warn("email notifications disabled; set MAILERSEND_API_KEY to enable delivery")
	}
	retentionService, err := service.NewRetentionService(adapters.objectRefs, objectStore)
	if err != nil {
		slog.Error("configure retention", "error", err)
		os.Exit(1)
	}
	go sweepOrphanedObjects(ctx, retentionService)
	reportService, err := service.NewReportService(adapters.reports, adapters.expenses, adapters.taxonomy, adapters.identity, objectStore, adapters.notifications)
	if err != nil {
		slog.Error("configure reports", "error", err)
		os.Exit(1)
	}
	go generateReports(ctx, reportService)
	scheduledPaymentService, err := service.NewScheduledPaymentService(adapters.payments, adapters.events, adapters.groups, adapters.unit)
	if err != nil {
		slog.Error("configure scheduled payments", "error", err)
		os.Exit(1)
	}
	scheduledPaymentService = scheduledPaymentService.
		WithExpensePosting(adapters.expenses).
		WithNotifications(adapters.notifications).
		WithTaxonomy(adapters.taxonomy)
	go postDuePayments(ctx, scheduledPaymentService)
	if adapters.pool != nil {
		go cleanupRateLimitWindows(ctx, postgresadapter.NewRateLimiter(adapters.pool, 0, 0))
	}
	// assistantService and intakeService are declared as their inbound
	// interfaces, not concrete pointers: the handlers check `h.service != nil`
	// to report "not configured" when no OpenAI key is set, and assigning a
	// nil *service.AssistantService into an interface variable would produce
	// a non-nil interface holding a nil pointer, breaking that check. Each
	// concrete value is built in full locally and only assigned to the
	// interface variable once construction has succeeded.
	var assistantService inbound.AssistantService
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
		auditedProvider = auditedProvider.WithMetrics(aiCalls, aiTokens)
		concreteAssistant, err := service.NewAssistantService(auditedProvider, adapters.expenses, adapters.budgets)
		if err != nil {
			slog.Error("configure assistant", "error", err)
			os.Exit(1)
		}
		assistantService = concreteAssistant.
			WithModel(cfg.OpenAIAssistantModel).
			WithLimiter(adapters.newLimiter(assistantRateLimit, assistantRateInterval))
	}
	var intakeService inbound.ExpenseIntakeService
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
		auditedProvider = auditedProvider.WithMetrics(aiCalls, aiTokens)
		auditedTranscriber, err := service.NewAuditedAudioTranscriber(client, adapters.aiRequests)
		if err != nil {
			slog.Error("configure transcription auditing", "error", err)
			os.Exit(1)
		}
		auditedTranscriber = auditedTranscriber.WithMetrics(aiCalls, aiTokens)
		concreteIntake, err := service.NewExpenseIntakeService(auditedProvider, auditedTranscriber)
		if err != nil {
			slog.Error("configure expense intake", "error", err)
			os.Exit(1)
		}
		intakeService = concreteIntake.WithLimiter(adapters.newLimiter(service.IntakeRateLimit, service.IntakeRateInterval))
	}

	// HTTP handler setup
	r.Get(handler.GetVersionPath, handler.HandleGetVersion)
	handler.RegisterAuthRoutes(r, authService, cfg.Environment == "production")
	handler.RegisterUserRoutes(r, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterUploadRoutes(r, authService, expenseService, objectStore, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseRoutes(r, expenseService, handler.NewAuthMiddleware(authService))
	handler.RegisterExpenseIntakeRoutes(r, intakeService, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterBudgetRoutes(r, budgetService, handler.NewAuthMiddleware(authService))
	handler.RegisterScheduledPaymentRoutes(r, scheduledPaymentService, handler.NewAuthMiddleware(authService))
	handler.RegisterAnalyticsRoutes(r, analyticsService, handler.NewAuthMiddleware(authService))
	handler.RegisterTaxonomyRoutes(r, taxonomyService, handler.NewAuthMiddleware(authService))
	handler.RegisterGroupRoutes(r, groupService, handler.NewAuthMiddleware(authService))
	handler.RegisterAssistantRoutes(r, assistantService, authService, handler.NewAuthMiddleware(authService))
	handler.RegisterReportRoutes(r, reportService, objectStore, handler.NewAuthMiddleware(authService))
	handler.RegisterAuditRoutes(r, auditService, handler.NewAuthMiddleware(authService))
	handler.RegisterAdminUsageRoutes(r, adminUsageService, handler.NewAuthMiddleware(authService))
	r.Get("/livez", healthRegistry.Live)
	r.Get("/readyz", healthRegistry.Ready)
	r.Get("/startupz", healthRegistry.Startup)
	// Gated by a static token, not the JWT middleware every other endpoint
	// uses: Prometheus scrapes this and cannot do an interactive login.
	// Metrics leak user counts, request volumes, and route names, so this
	// must never be reachable without it.
	r.With(sharedauth.RequireBearerToken(cfg.MetricsToken)).Get("/metrics", healthRegistry.Metrics)
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
		healthRegistry.RegisterGauge(fmt.Sprintf(`billpiggy_outbox_lag{subscription=%q}`, projection.Name()), "Pending events for a subscription.", func() float64 { return float64(engine.Stats().Lag) })
		healthRegistry.RegisterGauge(fmt.Sprintf(`billpiggy_outbox_dead_lettered{subscription=%q}`, projection.Name()), "Events abandoned after exhausting retry attempts.", func() float64 { return float64(engine.Stats().DeadLettered) })
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
	// pgxpool.New never dials on its own — without this, a bad DSN, DNS
	// failure, or unreachable host stays silent until the first real query
	// blocks indefinitely and is eventually cut short by SIGTERM, surfacing
	// only as an opaque "context canceled" with no indication of the actual
	// cause. Pinging here with a bounded timeout fails fast with the real error.
	pingCtx, cancelPing := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		slog.Error("ping postgres", "error", err)
		os.Exit(1)
	}
	identity := postgresadapter.NewIdentityRepository(pool)
	healthRegistry.Register("postgres", identity.Ping)
	healthRegistry.Register("migrations", migrationsAppliedCheck(pool))
	outboxStore := postgresadapter.NewOutboxStore(pool)
	return stores{
		unit: pgxtx.NewRunner(pool),
		// Identity, taxonomy, and group reads are cached: GetUserByID runs on
		// every authenticated request, and categories/tags/groups change far
		// less often than expense and budget listing reads them back.
		identity:      cached.NewIdentityRepository(identity, cacheTTL),
		expenses:      postgresadapter.NewExpenseRepository(pool),
		budgets:       postgresadapter.NewBudgetRepository(pool),
		groups:        cached.NewGroupRepository(postgresadapter.NewGroupRepository(pool), cacheTTL),
		taxonomy:      cached.NewTaxonomyRepository(postgresadapter.NewTaxonomyRepository(pool), cacheTTL),
		analytics:     postgresadapter.NewAnalyticsRepository(pool),
		budgetUsage:   postgresadapter.NewBudgetUsageRepository(pool),
		audit:         postgresadapter.NewAuditRepository(pool),
		notifications: postgresadapter.NewNotificationRepository(pool),
		objectRefs:    postgresadapter.NewObjectReferenceRepository(pool),
		aiRequests:    postgresadapter.NewAIRequestRepository(pool),
		reports:       postgresadapter.NewReportRepository(pool),
		payments:      postgresadapter.NewScheduledPaymentRepository(pool),
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
	reports := memory.NewReportRepository()
	payments := memory.NewScheduledPaymentRepository()
	events := memory.NewEventStore()
	unit := memory.NewUnitOfWork(expenses, budgets, analytics, budgetUsage, audit, notifications, objectRefs, taxonomy, reports, payments, events)
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
		reports:       reports,
		payments:      payments,
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

// generateReports periodically generates every weekly/monthly/yearly report
// that has become due since the last tick.
//
// An hourly tick is far more frequent than any report period, so this does
// not race to produce the same report the moment it becomes due: it is a
// deliberately relaxed cadence for a background job, and GenerateDue's
// existence check against already-generated reports means a redundant tick
// costs one query per user and no rendering work.
func generateReports(ctx context.Context, reports *service.ReportService) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if generated, err := reports.GenerateDue(ctx, time.Now()); err != nil {
			slog.Error("generate due reports", "error", err)
		} else if generated > 0 {
			slog.Info("generated due reports", "count", generated)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// postDuePayments periodically runs every scheduled payment that has come
// due and sends the advance notices for those about to.
//
// The tick is finer than the report scheduler's because a payment reminder is
// time-sensitive in a way a monthly report is not: a "rent due tomorrow"
// notice that lands eleven hours late has lost most of its value. Redundant
// ticks are cheap — PostDue's ledger check means an occurrence already handled
// costs one insert that conflicts, and no expense or email.
func postDuePayments(ctx context.Context, payments *service.ScheduledPaymentService) {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for {
		if posted, err := payments.PostDue(ctx, time.Now()); err != nil {
			slog.Error("post due scheduled payments", "error", err)
		} else if posted > 0 {
			slog.Info("posted due scheduled payments", "count", posted)
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

func deliverNotifications(ctx context.Context, notifications *service.NotificationService, users outbound.IdentityRepository, sender service.EmailSender, lastRun *atomic.Int64) {
	workerID := notificationWorkerID()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := notifications.DeliverPending(ctx, users, sender, workerID, 25); err != nil {
			slog.Error("deliver notifications", "error", err)
		}
		lastRun.Store(time.Now().UnixNano())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// notificationWorkerHealth fails readiness once the delivery loop has gone
// far longer than its one-minute tick without completing a pass, which means
// it has hung or its goroutine has died.
func notificationWorkerHealth(lastRun *atomic.Int64) health.Check {
	const staleness = 5 * time.Minute
	return func(context.Context) error {
		last := lastRun.Load()
		if last == 0 {
			return nil // hasn't ticked yet; startupz/readyz allow a grace period before this check matters
		}
		if age := time.Since(time.Unix(0, last)); age > staleness {
			return fmt.Errorf("notification worker has not completed a pass in %s", age.Round(time.Second))
		}
		return nil
	}
}

// activeUsersGauge returns a pull-based gauge counting non-blocked user
// accounts. Called at scrape time, so it always reflects the current count
// rather than a value that must be kept in sync by every mutation path.
func activeUsersGauge(identity outbound.IdentityRepository) func() float64 {
	return func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		users, err := identity.ListUsers(ctx)
		if err != nil {
			return 0
		}
		count := 0
		for _, user := range users {
			if !user.AccessBlocked {
				count++
			}
		}
		return float64(count)
	}
}

// migrationsAppliedCheck fails readiness until the newest migration this
// build expects has been recorded, so traffic never reaches a binary whose
// schema assumptions the database doesn't satisfy yet.
func migrationsAppliedCheck(pool *pgxpool.Pool) health.Check {
	return func(ctx context.Context) error {
		var applied bool
		if err := pool.QueryRow(ctx, `select exists(select 1 from public.schema_migrations where version = $1)`, latestMigrationVersion).Scan(&applied); err != nil {
			return fmt.Errorf("check migrations applied: %w", err)
		}
		if !applied {
			return fmt.Errorf("migration %s has not been applied", latestMigrationVersion)
		}
		return nil
	}
}

// routePattern reads chi's matched route pattern after routing completes, so
// HTTP metrics label by pattern (e.g. "/expenses/{expenseID}") rather than
// the literal path, which would give every distinct ID its own label series.
func routePattern(r *http.Request) string {
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		return routeContext.RoutePattern()
	}
	return ""
}

// notificationWorkerID identifies this replica's lease holder, so a stuck
// delivery's locked_by names the process that abandoned it.
func notificationWorkerID() string {
	host, err := os.Hostname()
	if err != nil {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func logLevel(value string) slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		return slog.LevelInfo
	}
	return level
}
