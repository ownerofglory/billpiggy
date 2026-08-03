package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
)

// GroupRepository is a PostgreSQL private-group projection.
type GroupRepository struct{ pool *pgxpool.Pool }

// NewGroupRepository creates a group adapter.
func NewGroupRepository(pool *pgxpool.Pool) *GroupRepository { return &GroupRepository{pool: pool} }

// CreateGroup stores a private group and its members atomically.
func (r *GroupRepository) CreateGroup(ctx context.Context, g domain.UserGroup) error {
	tx, e := r.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `insert into identity.groups(id,name,created_by,created_at) values($1,$2,$3,$4)`, g.ID, g.Name, g.CreatedBy, g.CreatedAt); e != nil {
		return e
	}
	for _, id := range g.MemberIDs {
		if _, e = tx.Exec(ctx, `insert into identity.group_members(group_id,user_id) values($1,$2)`, g.ID, id); e != nil {
			return e
		}
	}
	return tx.Commit(ctx)
}

// ListVisibleGroups returns groups owned by or shared with the viewer.
func (r *GroupRepository) ListVisibleGroups(ctx context.Context, viewer string, super bool) ([]domain.UserGroup, error) {
	q := `select distinct g.id::text,g.name,g.created_by::text,g.created_at from identity.groups g left join identity.group_members m on m.group_id=g.id where $1 or g.created_by=$2 or m.user_id=$2 order by g.created_at`
	rows, e := r.pool.Query(ctx, q, super, viewer)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []domain.UserGroup{}
	for rows.Next() {
		var g domain.UserGroup
		if e = rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt); e != nil {
			return nil, e
		}
		members, e := r.pool.Query(ctx, `select user_id::text from identity.group_members where group_id=$1`, g.ID)
		if e != nil {
			return nil, e
		}
		for members.Next() {
			var id string
			if e = members.Scan(&id); e != nil {
				members.Close()
				return nil, e
			}
			g.MemberIDs = append(g.MemberIDs, id)
		}
		members.Close()
		out = append(out, g)
	}
	return out, rows.Err()
}
