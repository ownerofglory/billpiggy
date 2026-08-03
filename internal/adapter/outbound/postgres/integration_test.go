//go:build integration

// Package postgres_test exercises the PostgreSQL adapters against a real
// database. The tests are behind the `integration` build tag and skip unless
// TEST_DATABASE_URL points at a disposable database, because they truncate
// every table between cases.
//
//	docker compose up -d postgres
//	TEST_DATABASE_URL="postgres://billpiggy:billpiggy@localhost:5432/billpiggy?sslmode=disable" \
//	    go test -tags=integration ./internal/adapter/outbound/postgres/...
package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	postgresadapter "github.com/ownerofglory/billpiggy/internal/adapter/outbound/postgres"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/internal/core/port/outbound"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// migrated tracks whether this process has already applied the schema.
var migrated bool

// newPool connects to the test database, applying migrations once per run.
func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping PostgreSQL integration tests")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if !migrated {
		applyMigrations(t, pool)
		migrated = true
	}
	truncateAll(t, pool)
	return pool
}

// applyMigrations runs every up migration in order, exactly as the deployed
// migration job does.
func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "migrations"))
	if err != nil {
		t.Fatalf("resolve migrations directory: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(root, "*.up.sql"))
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no migrations found under %s", root)
	}
	// Start from nothing, so every run exercises the migrations exactly as a
	// fresh deployment does rather than assuming a pre-existing schema.
	if _, err := pool.Exec(context.Background(), `drop schema if exists events, identity, expenses, budgets, analytics, reports, notifications, audit, files cascade`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	for _, file := range files {
		applyMigration(t, pool, filepath.Base(file))
	}
}

// applyMigration runs one migration file by name.
func applyMigration(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "migrations", name))
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	statements, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if _, err := pool.Exec(context.Background(), string(statements)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
}

// truncateAll resets every table so each test starts from a known state.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		truncate table
			events.outbox, events.projector_checkpoints, events.subscriptions, events.events,
			analytics.tag_expense_rollups, analytics.expense_rollups, analytics.expense_contributions,
			budgets.budget_usage, budgets.expense_contributions, budgets.budgets,
			expenses.expense_items, expenses.expense_tags, expenses.expenses, expenses.tags,
			audit.entries, notifications.deliveries, files.object_references,
			identity.group_members, identity.groups, identity.refresh_tokens, identity.invitations, identity.users
		restart identity cascade`)
	if err != nil {
		t.Fatalf("truncate test database: %v", err)
	}
	// TRUNCATE ... RESTART IDENTITY only resets identity columns, not the
	// standalone sequence backing global_seq, so tests that assert on it would
	// otherwise see values carried over from earlier cases.
	if _, err := pool.Exec(context.Background(), `alter sequence events.event_global_seq restart with 1`); err != nil {
		t.Fatalf("reset global sequence: %v", err)
	}
	// CASCADE reaches expenses.categories through its owner_id foreign key, so
	// the default categories go with it. Re-running their seed migration puts
	// them back; it is written to be idempotent.
	applyMigration(t, pool, "000008_seed_default_categories.up.sql")
}

// seedUser inserts a user the foreign keys require and returns its id.
func seedUser(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `insert into identity.users (id, email, password_hash, display_name) values ($1, $2, 'x', $2)`, id, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// defaultCategoryID returns a seeded default category, proving migration 000008
// actually gave production users something to categorise against.
func defaultCategoryID(t *testing.T, pool *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `select id::text from expenses.categories where owner_id is null and is_default and name = $1`, name).Scan(&id); err != nil {
		t.Fatalf("read default category %q: %v", name, err)
	}
	return id
}

func seedTag(t *testing.T, pool *pgxpool.Pool, ownerID, name string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `insert into expenses.tags (id, owner_id, name) values ($1, $2, $3)`, id, ownerID, name); err != nil {
		t.Fatalf("seed tag: %v", err)
	}
	return id
}

func expenseRecord(ownerID, categoryID string, amountMinor int64, occurredAt time.Time, tagIDs ...string) domain.ExpenseRecord {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return domain.ExpenseRecord{
		ID: uuid.NewString(), OwnerID: ownerID, Title: "Cinema", AmountMinor: amountMinor, Currency: "EUR",
		OccurredAt: occurredAt.UTC(), CategoryID: categoryID, TagIDs: tagIDs, Status: domain.ExpenseConfirmed,
		Items:     []domain.ExpenseItem{{Title: "Ticket", Quantity: "2", AmountMinor: amountMinor}},
		CreatedAt: now, UpdatedAt: now,
	}
}

func TestDefaultCategoriesAreSeeded(t *testing.T) {
	pool := newPool(t)
	var count int
	if err := pool.QueryRow(context.Background(), `select count(*) from expenses.categories where owner_id is null and is_default`).Scan(&count); err != nil {
		t.Fatalf("count default categories: %v", err)
	}
	if count != len(domain.DefaultCategories()) {
		t.Fatalf("seeded %d default categories, want %d", count, len(domain.DefaultCategories()))
	}
	// The identifiers must match the in-memory adapter's, or an expense created
	// in development would reference a category that does not exist in production.
	for _, category := range domain.DefaultCategories() {
		var name string
		if err := pool.QueryRow(context.Background(), `select name from expenses.categories where id = $1`, category.ID).Scan(&name); err != nil {
			t.Fatalf("default category %s (%s) missing from PostgreSQL: %v", category.Name, category.ID, err)
		}
		if name != category.Name {
			t.Fatalf("category %s is named %q in PostgreSQL", category.ID, name)
		}
	}
}

func TestExpenseRepositoryRoundTrip(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewExpenseRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	category := defaultCategoryID(t, pool, "Food")
	other := defaultCategoryID(t, pool, "Transport")
	tagA, tagB := seedTag(t, pool, owner, "cinema"), seedTag(t, pool, owner, "family")

	expense := expenseRecord(owner, category, 25_00, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC), tagA, tagB)
	if err := repository.CreateExpense(context.Background(), expense); err != nil {
		t.Fatalf("create expense: %v", err)
	}
	stored, err := repository.GetExpense(context.Background(), owner, expense.ID)
	if err != nil {
		t.Fatalf("get expense: %v", err)
	}
	if stored.Title != "Cinema" || stored.AmountMinor != 25_00 || stored.CategoryName != "Food" {
		t.Fatalf("unexpected expense %#v", stored)
	}
	if len(stored.TagIDs) != 2 || len(stored.Items) != 1 || stored.Items[0].Title != "Ticket" {
		t.Fatalf("relations not loaded: %#v", stored)
	}

	// The update path previously sent two semicolon-separated statements through
	// one parameterised Exec, which pgx rejects outright.
	expense.Title, expense.AmountMinor, expense.CategoryID = "Museum", 40_00, other
	expense.TagIDs = []string{tagB}
	expense.Items = []domain.ExpenseItem{{Title: "Entry", Quantity: "1", AmountMinor: 40_00}, {Title: "Guide", Quantity: "1", AmountMinor: 5_00}}
	expense.UpdatedAt = time.Now().UTC()
	if err := repository.UpdateExpense(context.Background(), expense); err != nil {
		t.Fatalf("update expense: %v", err)
	}
	stored, err = repository.GetExpense(context.Background(), owner, expense.ID)
	if err != nil {
		t.Fatalf("get updated expense: %v", err)
	}
	if stored.Title != "Museum" || stored.AmountMinor != 40_00 || stored.CategoryName != "Transport" {
		t.Fatalf("update not applied: %#v", stored)
	}
	if len(stored.TagIDs) != 1 || stored.TagIDs[0] != tagB || len(stored.Items) != 2 {
		t.Fatalf("relations not replaced: %#v", stored)
	}

	if err := repository.DeleteExpense(context.Background(), owner, expense.ID); err != nil {
		t.Fatalf("delete expense: %v", err)
	}
	if _, err := repository.GetExpense(context.Background(), owner, expense.ID); err == nil {
		t.Fatal("soft-deleted expense is still readable")
	}
}

func TestExpenseRepositoryFiltersTagsBeforePaging(t *testing.T) {
	pool := newPool(t)
	repository := postgresadapter.NewExpenseRepository(pool)
	owner := seedUser(t, pool, "owner@example.test")
	category := defaultCategoryID(t, pool, "Food")
	wanted := seedTag(t, pool, owner, "wanted")
	noise := seedTag(t, pool, owner, "noise")

	// Interleave tagged and untagged expenses. Filtering in Go after SQL's
	// LIMIT would drop most of the matches from the first page.
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		tag := noise
		if i%2 == 0 {
			tag = wanted
		}
		expense := expenseRecord(owner, category, int64(i+1)*100, base.AddDate(0, 0, i), tag)
		if err := repository.CreateExpense(context.Background(), expense); err != nil {
			t.Fatalf("create expense %d: %v", i, err)
		}
	}

	matches, err := repository.ListExpenses(context.Background(), outbound.ExpenseListFilter{
		OwnerID: owner, TagIDs: []string{wanted}, Limit: 5,
	})
	if err != nil {
		t.Fatalf("list expenses: %v", err)
	}
	if len(matches) != 5 {
		t.Fatalf("got %d matches on a page of 5, want a full page", len(matches))
	}
	for _, expense := range matches {
		if len(expense.TagIDs) != 1 || expense.TagIDs[0] != wanted {
			t.Fatalf("unexpected expense in a filtered page: %#v", expense)
		}
	}
}

func TestEventStoreCommitsWithTheCallersUnitOfWork(t *testing.T) {
	pool := newPool(t)
	runner := pgxtx.NewRunner(pool)
	events := postgresadapter.NewEventStore(pool)
	expenses := postgresadapter.NewExpenseRepository(pool)
	outboxStore := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")
	category := defaultCategoryID(t, pool, "Food")

	if err := outboxStore.EnsureSubscription(context.Background(), "analytics_rollups"); err != nil {
		t.Fatalf("register subscription: %v", err)
	}

	expense := expenseRecord(owner, category, 25_00, time.Now().UTC())
	failure := "projection rejected"
	err := runner.Within(context.Background(), func(ctx context.Context) error {
		if err := events.Append(ctx, outbound.DomainEvent{
			ID: uuid.NewString(), AggregateType: "expense", AggregateID: expense.ID,
			EventType: "expense_added", Payload: domain.ExpenseAdded{Expense: expense},
			OccurredAt: time.Now().UnixMilli(), ActorID: owner,
		}); err != nil {
			return err
		}
		if err := expenses.CreateExpense(ctx, expense); err != nil {
			return err
		}
		return errRollback(failure)
	})
	if err == nil {
		t.Fatal("expected the unit of work to fail")
	}

	// Nothing may survive: this is the atomicity the two-transaction write path
	// could not provide.
	var events_, outboxRows, expenseRows int
	if err := pool.QueryRow(context.Background(), `select
			(select count(*) from events.events),
			(select count(*) from events.outbox),
			(select count(*) from expenses.expenses)`).Scan(&events_, &outboxRows, &expenseRows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if events_ != 0 || outboxRows != 0 || expenseRows != 0 {
		t.Fatalf("rolled-back unit left events=%d outbox=%d expenses=%d", events_, outboxRows, expenseRows)
	}
}

// errRollback is a sentinel used to abort a unit of work deliberately.
type errRollback string

func (e errRollback) Error() string { return string(e) }

func TestEventStoreFansOutToEverySubscription(t *testing.T) {
	pool := newPool(t)
	runner := pgxtx.NewRunner(pool)
	events := postgresadapter.NewEventStore(pool)
	outboxStore := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")

	for _, subscription := range []string{"analytics_rollups", "budget_usage", "audit_trail"} {
		if err := outboxStore.EnsureSubscription(context.Background(), subscription); err != nil {
			t.Fatalf("register %s: %v", subscription, err)
		}
	}
	aggregateID := uuid.NewString()
	if err := runner.Within(context.Background(), func(ctx context.Context) error {
		return events.Append(ctx, outbound.DomainEvent{
			ID: uuid.NewString(), AggregateType: "expense", AggregateID: aggregateID,
			EventType: "expense_added", Payload: map[string]string{"expense_id": aggregateID},
			OccurredAt: time.Now().UnixMilli(), ActorID: owner,
		})
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	var rows int
	if err := pool.QueryRow(context.Background(), `select count(*) from events.outbox`).Scan(&rows); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	if rows != 3 {
		t.Fatalf("fan-out produced %d outbox rows, want one per subscription", rows)
	}
	for _, subscription := range []string{"analytics_rollups", "budget_usage", "audit_trail"} {
		lag, err := outboxStore.Lag(context.Background(), subscription)
		if err != nil {
			t.Fatalf("lag for %s: %v", subscription, err)
		}
		if lag != 1 {
			t.Fatalf("lag for %s = %d, want 1", subscription, lag)
		}
	}
}

func TestEnsureSubscriptionBackfillsHistory(t *testing.T) {
	pool := newPool(t)
	runner := pgxtx.NewRunner(pool)
	events := postgresadapter.NewEventStore(pool)
	outboxStore := postgresadapter.NewOutboxStore(pool)
	owner := seedUser(t, pool, "owner@example.test")

	// Append before any subscription exists, then register one. A projection
	// added later must receive the history it missed.
	aggregateID := uuid.NewString()
	for _, eventType := range []string{"expense_added", "expense_updated"} {
		if err := runner.Within(context.Background(), func(ctx context.Context) error {
			return events.Append(ctx, outbound.DomainEvent{
				ID: uuid.NewString(), AggregateType: "expense", AggregateID: aggregateID,
				EventType: eventType, Payload: map[string]string{"expense_id": aggregateID},
				OccurredAt: time.Now().UnixMilli(), ActorID: owner,
			})
		}); err != nil {
			t.Fatalf("append %s: %v", eventType, err)
		}
	}
	if err := outboxStore.EnsureSubscription(context.Background(), "late_joiner"); err != nil {
		t.Fatalf("register subscription: %v", err)
	}
	lag, err := outboxStore.Lag(context.Background(), "late_joiner")
	if err != nil {
		t.Fatalf("lag: %v", err)
	}
	if lag != 2 {
		t.Fatalf("backfill queued %d messages, want 2", lag)
	}

	// Registering again is idempotent and must not duplicate the backlog.
	if err := outboxStore.EnsureSubscription(context.Background(), "late_joiner"); err != nil {
		t.Fatalf("re-register subscription: %v", err)
	}
	if lag, _ := outboxStore.Lag(context.Background(), "late_joiner"); lag != 2 {
		t.Fatalf("re-registering changed the backlog to %d", lag)
	}

	var replay bool
	if err := pool.QueryRow(context.Background(), `select bool_and(replay) from events.outbox where subscription = 'late_joiner'`).Scan(&replay); err != nil {
		t.Fatalf("read replay flag: %v", err)
	}
	if !replay {
		t.Fatal("backfilled messages must be marked as replays so handlers suppress side effects")
	}
}
