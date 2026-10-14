package observations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

type Postgres struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Postgres, error) {
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("DATABASE_URL is invalid")
	}
	config.MaxConns = 4
	config.MinConns = 0
	config.ConnConfig.ConnectTimeout = 3 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, errors.New("could not configure PostgreSQL")
	}
	store := &Postgres{pool}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, errors.New("PostgreSQL is unavailable; start it with bash scripts/db.sh up")
	}
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		return nil, errors.New("could not apply observation database migrations")
	}
	return store, nil
}
func (p *Postgres) Close() { p.pool.Close() }
func (p *Postgres) Migrate(ctx context.Context) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(2026101401)`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS farewise_schema_migrations (version integer PRIMARY KEY,checksum bytea NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	sql, err := migrations.ReadFile("migrations/001_observations.sql")
	if err != nil {
		return err
	}
	checksum := sha256.Sum256(sql)
	var existing []byte
	err = tx.QueryRow(ctx, `SELECT checksum FROM farewise_schema_migrations WHERE version=1`).Scan(&existing)
	if err == nil {
		if !bytes.Equal(existing, checksum[:]) {
			return errors.New("migration checksum changed")
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err = tx.Exec(ctx, string(sql)); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO farewise_schema_migrations(version,checksum) VALUES(1,$1)`, checksum[:]); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (p *Postgres) FindSession(ctx context.Context, id string) (Snapshot, []byte, error) {
	var snapshot Snapshot
	var raw, hash []byte
	err := p.pool.QueryRow(ctx, `SELECT snapshot,request_hash FROM observation_sessions WHERE id=$1`, id).Scan(&raw, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return snapshot, nil, ErrNotFound
	}
	if err != nil {
		return snapshot, nil, err
	}
	err = json.Unmarshal(raw, &snapshot)
	return snapshot, hash, err
}
func (p *Postgres) PutSession(ctx context.Context, snapshot Snapshot, hash [32]byte) (Snapshot, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO observation_sessions(id,captured_at,request_hash,snapshot) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO NOTHING`, snapshot.ID, snapshot.CapturedAt, hash[:], raw)
	if err != nil {
		return Snapshot{}, err
	}
	saved, savedHash, err := p.FindSession(ctx, snapshot.ID)
	if err == nil && !bytes.Equal(savedHash, hash[:]) {
		return Snapshot{}, ErrConflict
	}
	return saved, err
}

const columns = `o.id::text,o.session_id::text,o.provider,o.product,o.price_cents,o.fare_kind,o.quoted_at,o.weather,o.promotion,o.included_fees_cents,o.notes,o.revision,o.created_at,o.updated_at,s.snapshot`

func scanRecord(row pgx.Row) (Record, error) {
	var r Record
	var raw []byte
	err := row.Scan(&r.ID, &r.SessionID, &r.Provider, &r.Product, &r.PriceCents, &r.FareKind, &r.QuotedAt, &r.Weather, &r.Promotion, &r.IncludedFeesCents, &r.Notes, &r.Revision, &r.CreatedAt, &r.UpdatedAt, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, ErrNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(raw, &r.Snapshot); err != nil {
		return r, err
	}
	r.Derive()
	return r, nil
}
func (p *Postgres) record(ctx context.Context, id string) (Record, error) {
	return scanRecord(p.pool.QueryRow(ctx, `SELECT `+columns+` FROM fare_observations o JOIN observation_sessions s ON s.id=o.session_id WHERE o.id=$1`, id))
}
func (p *Postgres) Save(ctx context.Context, request SaveRequest) (Record, bool, error) {
	if _, _, err := p.FindSession(ctx, request.SessionID); err != nil {
		return Record{}, false, err
	}
	hash := Fingerprint(request)
	tag, err := p.pool.Exec(ctx, `INSERT INTO fare_observations(id,session_id,request_hash,provider,product,price_cents,fare_kind,quoted_at,weather,promotion,included_fees_cents,notes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(id) DO NOTHING`, request.ID, request.SessionID, hash[:], request.Provider, request.Product, request.PriceCents, request.FareKind, request.QuotedAt, request.Weather, request.Promotion, request.IncludedFeesCents, request.Notes)
	if err != nil {
		return Record{}, false, err
	}
	if tag.RowsAffected() == 0 {
		var existing []byte
		if err = p.pool.QueryRow(ctx, `SELECT request_hash FROM fare_observations WHERE id=$1`, request.ID).Scan(&existing); err != nil {
			return Record{}, false, err
		}
		if !bytes.Equal(existing, hash[:]) {
			return Record{}, false, ErrConflict
		}
	}
	saved, err := p.record(ctx, request.ID)
	return saved, tag.RowsAffected() == 1, err
}
func (p *Postgres) Edit(ctx context.Context, id string, request EditRequest) (Record, error) {
	// Immutable session linkage and snapshots are never replaced by a correction.
	tag, err := p.pool.Exec(ctx, `UPDATE fare_observations SET provider=$2,product=$3,price_cents=$4,fare_kind=$5,quoted_at=$6,weather=$7,promotion=$8,included_fees_cents=$9,notes=$10,revision=revision+1,updated_at=now() WHERE id=$1 AND revision=$11`, id, request.Provider, request.Product, request.PriceCents, request.FareKind, request.QuotedAt, request.Weather, request.Promotion, request.IncludedFeesCents, request.Notes, request.Revision)
	if err != nil {
		return Record{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err = p.record(ctx, id); err != nil {
			return Record{}, err
		}
		return Record{}, ErrConflict
	}
	return p.record(ctx, id)
}

const where = `($1='' OR o.provider=$1) AND ($2='' OR concat_ws(' ',o.product,o.notes,s.snapshot->'trip'->'pickup'->>'name',s.snapshot->'trip'->'dropoff'->>'name') ILIKE '%' || $2 || '%')`

func (p *Postgres) List(ctx context.Context, filter Filter) (Page, error) {
	page := Page{Records: []Record{}, Stats: Stats{ByProvider: []Group{}, ByTimeSlot: []Group{}, ByWeekday: []Group{}}}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM fare_observations o JOIN observation_sessions s ON s.id=o.session_id WHERE `+where, filter.Provider, filter.Search).Scan(&page.Total); err != nil {
		return page, err
	}
	rows, err := tx.Query(ctx, `SELECT `+columns+` FROM fare_observations o JOIN observation_sessions s ON s.id=o.session_id WHERE `+where+` ORDER BY o.quoted_at DESC,o.id DESC LIMIT $3 OFFSET $4`, filter.Provider, filter.Search, filter.Limit, filter.Offset)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			rows.Close()
			return page, err
		}
		page.Records = append(page.Records, record)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	if err = tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE fare_kind='quoted'),count(*) FILTER(WHERE fare_kind='paid'),count(DISTINCT (s.snapshot->'trip'->'pickup'->>'latitude',s.snapshot->'trip'->'pickup'->>'longitude',s.snapshot->'trip'->'dropoff'->>'latitude',s.snapshot->'trip'->'dropoff'->>'longitude',s.snapshot->'trip'->>'passengers',s.snapshot->'trip'->>'rideType')) FROM fare_observations o JOIN observation_sessions s ON s.id=o.session_id`).Scan(&page.Stats.Total, &page.Stats.Quotes, &page.Stats.Paid, &page.Stats.Routes); err != nil {
		return page, err
	}
	for _, group := range []struct {
		expression string
		target     *[]Group
	}{{"provider", &page.Stats.ByProvider}, {`extract(isodow FROM quoted_at AT TIME ZONE 'Asia/Singapore')::int::text`, &page.Stats.ByWeekday}, {`CASE WHEN extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')>=6 AND extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')<10 THEN 'morning' WHEN extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')>=10 AND extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')<16 THEN 'midday' WHEN extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')>=16 AND extract(hour FROM quoted_at AT TIME ZONE 'Asia/Singapore')<20 THEN 'evening' ELSE 'night' END`, &page.Stats.ByTimeSlot}} {
		rows, err := tx.Query(ctx, fmt.Sprintf(`SELECT %s,count(*) FROM fare_observations GROUP BY 1 ORDER BY 1`, group.expression))
		if err != nil {
			return page, err
		}
		for rows.Next() {
			var value Group
			if err = rows.Scan(&value.Label, &value.Count); err != nil {
				rows.Close()
				return page, err
			}
			*group.target = append(*group.target, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return page, err
		}
	}
	return page, tx.Commit(ctx)
}
