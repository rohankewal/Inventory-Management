package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohankewalramani/inventory-sys/internal/core"
	"github.com/rohankewalramani/inventory-sys/internal/storage"
)

type settingsRepo struct{ s *Store }

func (r *settingsRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.s.read().QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", mapError(err, fmt.Sprintf("read setting %q", key))
	}
	return value, nil
}

func (r *settingsRepo) Set(ctx context.Context, key, value string) error {
	if strings.TrimSpace(key) == "" {
		return fmt.Errorf("write setting: %w: key cannot be blank", core.ErrInvalid)
	}

	_, err := r.s.write().ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, fmtTime(time.Now().UTC()))
	if err != nil {
		return mapError(err, fmt.Sprintf("write setting %q", key))
	}
	return nil
}

func (r *settingsRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.s.read().QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, mapError(err, "read settings")
	}
	defer func() { _ = rows.Close() }()

	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("read settings: %w", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read settings: %w", err)
	}
	return out, nil
}

var _ storage.SettingsRepo = (*settingsRepo)(nil)
