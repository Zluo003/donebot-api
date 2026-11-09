package repository

import (
	"context"
	"encoding/json"
	"fmt"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type doneBotTemplateRepository struct{ client *dbent.Client }

func NewDoneBotTemplateRepository(client *dbent.Client) service.DoneBotTemplateRepository {
	return &doneBotTemplateRepository{client: client}
}

func (r *doneBotTemplateRepository) List(ctx context.Context, admin bool) ([]service.DoneBotTemplate, error) {
	query := `SELECT metadata,published,created_at FROM donebot_template_releases ORDER BY created_at DESC,template_id,release`
	if !admin {
		query = `SELECT DISTINCT ON (template_id) metadata,published,created_at FROM donebot_template_releases WHERE published ORDER BY template_id,split_part(release,'.',1)::integer DESC,split_part(release,'.',2)::integer DESC,split_part(release,'.',3)::integer DESC`
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := []service.DoneBotTemplate{}
	for rows.Next() {
		var raw []byte
		var entry service.DoneBotTemplate
		if err = rows.Scan(&raw, &entry.Published, &entry.CreatedAt); err != nil {
			return nil, err
		}
		published, created := entry.Published, entry.CreatedAt
		if err = json.Unmarshal(raw, &entry); err != nil {
			return nil, err
		}
		entry.Published, entry.CreatedAt = published, created
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
func (r *doneBotTemplateRepository) Get(ctx context.Context, id, version string, admin bool) (*service.DoneBotTemplateRelease, error) {
	query := `SELECT metadata,document,published,created_at FROM donebot_template_releases WHERE template_id=$1 AND release=$2`
	if !admin {
		query += ` AND published`
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, query, id, version)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrDoneBotTemplateMissing
	}
	var raw []byte
	entry := &service.DoneBotTemplateRelease{}
	if err = rows.Scan(&raw, &entry.Document, &entry.Published, &entry.CreatedAt); err != nil {
		return nil, err
	}
	published, created := entry.Published, entry.CreatedAt
	if err = json.Unmarshal(raw, &entry.DoneBotTemplate); err != nil {
		return nil, err
	}
	entry.Published, entry.CreatedAt = published, created
	return entry, nil
}
func (r *doneBotTemplateRepository) Create(ctx context.Context, entry *service.DoneBotTemplateRelease) error {
	raw, err := json.Marshal(entry.DoneBotTemplate)
	if err != nil {
		return err
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `INSERT INTO donebot_template_releases(template_id,release,metadata,document) VALUES($1,$2,$3::jsonb,$4) ON CONFLICT DO NOTHING RETURNING created_at`, entry.ID, entry.Release, string(raw), []byte(entry.Document))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return service.ErrDoneBotTemplateExists
	}
	return rows.Scan(&entry.CreatedAt)
}
func (r *doneBotTemplateRepository) SetPublished(ctx context.Context, id, version string, published bool) error {
	result, err := clientFromContext(ctx, r.client).ExecContext(ctx, `UPDATE donebot_template_releases SET published=$3 WHERE template_id=$1 AND release=$2`, id, version, published)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return service.ErrDoneBotTemplateMissing
	}
	if n != 1 {
		return fmt.Errorf("unexpected template update count")
	}
	return nil
}
