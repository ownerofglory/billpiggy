package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ownerofglory/billpiggy/internal/core/domain"
	"github.com/ownerofglory/billpiggy/pkg/pgxtx"
)

// GroupRepository is a PostgreSQL private-group projection.
type GroupRepository struct{ pool *pgxpool.Pool }

// NewGroupRepository creates a group adapter.
func NewGroupRepository(pool *pgxpool.Pool) *GroupRepository { return &GroupRepository{pool: pool} }

// CreateGroup stores a private group and its members atomically.
func (r *GroupRepository) CreateGroup(ctx context.Context, g domain.UserGroup) error {
	return pgxtx.Atomic(ctx, r.pool, func(ctx context.Context, querier pgxtx.Querier) error {
		if _, err := querier.Exec(ctx, `insert into identity.groups(id,name,created_by,created_at) values($1,$2,$3,$4)`, g.ID, g.Name, g.CreatedBy, g.CreatedAt); err != nil {
			return err
		}
		for _, id := range g.MemberIDs {
			if _, err := querier.Exec(ctx, `insert into identity.group_members(group_id,user_id) values($1,$2)`, g.ID, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ListVisibleGroups returns groups owned by or shared with the viewer.
func (r *GroupRepository) ListVisibleGroups(ctx context.Context, viewer string, super bool) ([]domain.UserGroup, error) {
	querier := pgxtx.From(ctx, r.pool)
	rows, err := querier.Query(ctx, `select distinct g.id::text,g.name,g.created_by::text,g.created_at from identity.groups g left join identity.group_members m on m.group_id=g.id where $1 or g.created_by=$2 or m.user_id=$2 order by g.created_at`, super, viewer)
	if err != nil {
		return nil, err
	}
	groups := []domain.UserGroup{}
	for rows.Next() {
		var g domain.UserGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedBy, &g.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Members load in one query after the cursor closes, rather than one query
	// per group issued while it is still open.
	if err := loadGroupMembers(ctx, querier, groups); err != nil {
		return nil, err
	}
	return groups, nil
}

// loadGroupMembers attaches member identifiers to already-scanned groups.
func loadGroupMembers(ctx context.Context, querier pgxtx.Querier, groups []domain.UserGroup) error {
	if len(groups) == 0 {
		return nil
	}
	ids := make([]string, 0, len(groups))
	index := make(map[string]*domain.UserGroup, len(groups))
	for i := range groups {
		ids = append(ids, groups[i].ID)
		index[groups[i].ID] = &groups[i]
	}
	rows, err := querier.Query(ctx, `select group_id::text,user_id::text from identity.group_members where group_id = any($1::uuid[]) order by group_id, added_at`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var groupID, userID string
		if err := rows.Scan(&groupID, &userID); err != nil {
			return err
		}
		if group, ok := index[groupID]; ok {
			group.MemberIDs = append(group.MemberIDs, userID)
		}
	}
	return rows.Err()
}
