package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/url-shortener/internal/model"
)

type LinkRepo struct {
	pool *pgxpool.Pool
}

func NewLinkRepo(pool *pgxpool.Pool) *LinkRepo {
	return &LinkRepo{pool: pool}
}

func (r *LinkRepo) Create(ctx context.Context, link *model.Link) error {
	query := `
		INSERT INTO links (user_id, short_code, original_url, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at
	`
	err := r.pool.QueryRow(
		ctx, query,
		link.UserID, link.ShortCode, link.OriginalURL, link.ExpiresAt,
	).Scan(&link.ID, &link.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return model.ErrAlreadyExists
		}
		return err
	}
	return nil
}

func (r *LinkRepo) GetByCode(ctx context.Context, code string) (*model.Link, error) {
	query := `
		SELECT id, user_id, short_code, original_url, clicks_count, expires_at, created_at
		FROM links
		WHERE short_code = $1
	`
	var l model.Link
	err := r.pool.QueryRow(ctx, query, code).Scan(
		&l.ID, &l.UserID, &l.ShortCode, &l.OriginalURL, &l.ClicksCount, &l.ExpiresAt, &l.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}
	return &l, nil
}

func (r *LinkRepo) ListByUser(ctx context.Context, userID int64) ([]model.Link, error) {
	query := `
		SELECT id, user_id, short_code, original_url, clicks_count, expires_at, created_at
		FROM links
		WHERE user_id = $1
		ORDER BY created_at DESC
	`
	rows, err := r.pool.Query(ctx, query, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []model.Link
	for rows.Next() {
		var l model.Link
		if err := rows.Scan(
			&l.ID, &l.UserID, &l.ShortCode, &l.OriginalURL, &l.ClicksCount, &l.ExpiresAt, &l.CreatedAt,
		); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func (r *LinkRepo) Delete(ctx context.Context, code string, userID int64) error {
	query := `DELETE FROM links WHERE short_code = $1 AND user_id = $2`
	tag, err := r.pool.Exec(ctx, query, code, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return model.ErrNotFound
	}
	return nil
}

func (r *LinkRepo) SaveClickBatch(ctx context.Context, events []model.ClickEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.CopyFrom(
		ctx,
		pgx.Identifier{"click_events"},
		[]string{"short_code", "ip", "user_agent", "referer", "clicked_at"},
		pgx.CopyFromSlice(len(events), func(i int) ([]any, error) {
			e := events[i]
			return []any{e.ShortCode, e.IP, e.UserAgent, e.Referer, e.ClickedAt}, nil
		}),
	)
	if err != nil {
		return err
	}

	counts := make(map[string]int64)
	for _, e := range events {
		counts[e.ShortCode]++
	}

	batch := &pgx.Batch{}
	for code, count := range counts {
		batch.Queue(
			`UPDATE links SET clicks_count = clicks_count + $1 WHERE short_code = $2`,
			count, code,
		)
	}

	br := tx.SendBatch(ctx, batch)
	for range counts {
		if _, err := br.Exec(); err != nil {
			br.Close()
			return err
		}
	}
	if err := br.Close(); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *LinkRepo) GetRecentClicks(ctx context.Context, code string, limit int) ([]model.ClickEvent, error) {
	query := `
		SELECT short_code, ip, user_agent, referer, clicked_at
		FROM click_events
		WHERE short_code = $1
		ORDER BY clicked_at DESC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, query, code, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []model.ClickEvent
	for rows.Next() {
		var e model.ClickEvent
		if err := rows.Scan(&e.ShortCode, &e.IP, &e.UserAgent, &e.Referer, &e.ClickedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
