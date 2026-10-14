package observations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func database(t *testing.T) *Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL or run bash scripts/test-db.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal("test PostgreSQL configuration failed")
	}
	schema := fmt.Sprintf("farewise_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal("could not create isolated test schema")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	store := &Postgres{pool}
	t.Cleanup(func() {
		pool.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("migration failed", err)
	}
	return store
}
func TestPostgresIdempotencyCorrectionsAndPersistence(t *testing.T) {
	store := database(t)
	ctx := context.Background()
	snapshot := testSnapshot()
	hash := Fingerprint("fixture")
	saved, err := store.PutSession(ctx, snapshot, hash)
	if err != nil {
		t.Fatal(err)
	}
	mutated := snapshot
	mutated.Trip.DistanceKM = 999
	replay, err := store.PutSession(ctx, mutated, hash)
	if err != nil || replay.Trip.DistanceKM != 20 {
		t.Fatal("snapshot was overwritten")
	}
	if _, err = store.PutSession(ctx, snapshot, Fingerprint("different")); !errors.Is(err, ErrConflict) {
		t.Fatal("capture ID collision ignored")
	}
	input := SaveRequest{ID: testObservationID, SessionID: saved.ID, Fields: testFields()}
	if err = input.Validate(time.Now()); err != nil {
		t.Fatal(err)
	}
	record, created, err := store.Save(ctx, input)
	if err != nil || !created {
		t.Fatal(err)
	}
	record, recreated, err := store.Save(ctx, input)
	if err != nil || recreated || record.Revision != 1 {
		t.Fatal("duplicate save created another record")
	}
	different := input
	different.Product = "GrabCar"
	if _, _, err = store.Save(ctx, different); !errors.Is(err, ErrConflict) {
		t.Fatal("save ID collision ignored")
	}
	changed := record.Fields
	price := 6800
	changed.PriceCents = &price
	corrected, err := store.Edit(ctx, record.ID, EditRequest{Revision: 1, Fields: changed})
	if err != nil || corrected.Revision != 2 || *corrected.PriceCents != 6800 || corrected.Snapshot.Trip.DistanceKM != 20 {
		t.Fatal("correction modified snapshot or failed", err)
	}
	if _, err = store.Edit(ctx, record.ID, EditRequest{Revision: 1, Fields: changed}); !errors.Is(err, ErrConflict) {
		t.Fatal("stale correction overwrote a newer revision")
	}
	if err = store.Migrate(ctx); err != nil {
		t.Fatal("migration was not idempotent")
	}
	// A new pool has no process-memory records and must read the persisted row.
	config := store.pool.Config()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	reconnected := &Postgres{pool}
	read, err := reconnected.record(ctx, record.ID)
	if err != nil || *read.PriceCents != 6800 {
		t.Fatal("record did not survive pool restart")
	}
	page, err := reconnected.List(ctx, Filter{Provider: "grab", Search: "JustGrab", Limit: 20})
	if err != nil || page.Total != 1 || page.Stats.Total != 1 || page.Stats.Routes != 1 {
		t.Fatal("search/stats query failed", err)
	}
	page, err = reconnected.List(ctx, Filter{Search: "' OR 1=1 --", Limit: 20})
	if err != nil || page.Total != 0 {
		t.Fatal("search was not parameterized", err)
	}
}
func TestPostgresConcurrentDuplicateSaves(t *testing.T) {
	store := database(t)
	ctx := context.Background()
	if _, err := store.PutSession(ctx, testSnapshot(), Fingerprint("fixture")); err != nil {
		t.Fatal(err)
	}
	request := SaveRequest{ID: testObservationID, SessionID: testSessionID, Fields: testFields()}
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, _, err := store.Save(ctx, request); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	page, err := store.List(ctx, Filter{Limit: 20})
	if err != nil || page.Total != 1 {
		t.Fatal("concurrent retries duplicated data")
	}
}
