package repository

import (
	"context"
	"fmt"

	entsql "entgo.io/ent/dialect/sql"
)

// This predicate is deliberately limited to management listings. Gateway
// authentication and invalidation still see every installation API key.
func excludeDoneBotInstallKeys(s *entsql.Selector) {
	installs := entsql.Table("donebot_installs")
	s.Where(entsql.NotExists(entsql.Select(installs.C("api_key_id")).From(installs).Where(entsql.ColumnsEQ(installs.C("api_key_id"), s.C("id")))))
}
func (r *apiKeyRepository) IsDoneBotInstallKey(ctx context.Context, id int64) (bool, error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM donebot_installs WHERE api_key_id=$1)`, id)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, fmt.Errorf("installation key lookup returned no row")
	}
	var managed bool
	err = rows.Scan(&managed)
	return managed, err
}
