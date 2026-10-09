package service

// "My work" view (C6T3): the caller's issues across a workspace's projects,
// filterable by relationship (assigned / created / watched). One query,
// no N+1. Any workspace member (guest 5+) may list their own — it is
// inherently scoped to the caller's user id.

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidMyWorkFilter is returned for unknown ?filter= values.
var ErrInvalidMyWorkFilter = errors.New("service: invalid my-work filter")

// MyWorkFilter is the relationship filter for ListMyWork.
type MyWorkFilter string

const (
	MyWorkAssigned MyWorkFilter = "assigned"
	MyWorkCreated  MyWorkFilter = "created"
	MyWorkWatched  MyWorkFilter = "watched"
)

// MyWorkItem is a compact issue summary for the "My work" view.
type MyWorkItem struct {
	ID                string    `json:"id"`
	DisplayID         string    `json:"display_id"`
	Name              string    `json:"name"`
	Priority          int       `json:"priority"`
	StateID           string    `json:"state_id"`
	StateName         string    `json:"state_name"`
	StateGroup        string    `json:"state_group"`
	ProjectID         string    `json:"project_id"`
	ProjectIdentifier string    `json:"project_identifier"`
	ProjectName       string    `json:"project_name"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ListMyWork returns the caller's issues in the workspace, newest first.
// filter defaults to assigned; limit is clamped to [1,200] (default 100).
func ListMyWork(ctx context.Context, pool *pgxpool.Pool, wsSlug, actorID string, filter MyWorkFilter, limit int) ([]MyWorkItem, error) {
	if filter == "" {
		filter = MyWorkAssigned
	}
	var join string
	switch filter {
	case MyWorkAssigned:
		join = `JOIN issue_assignees ia ON ia.issue_id = i.id AND ia.user_id = $2::uuid`
	case MyWorkCreated:
		join = `` // predicate goes in WHERE
	case MyWorkWatched:
		join = `JOIN issue_subscribers sub ON sub.issue_id = i.id AND sub.user_id = $2::uuid`
	default:
		return nil, ErrInvalidMyWorkFilter
	}
	if limit < 1 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	wsID, _, err := workspaceIDForActor(pool.QueryRow(ctx,
		`SELECT w.id::text, m.role
		   FROM workspaces w
		   JOIN workspace_members m ON m.workspace_id = w.id
		  WHERE w.slug = $1 AND m.user_id = $2::uuid`,
		wsSlug, actorID))
	if err != nil {
		return nil, err
	}
	where := `p.workspace_id = $1::uuid AND i.deleted_at IS NULL`
	if filter == MyWorkCreated {
		where += ` AND i.created_by = $2::uuid`
	}
	rows, err := pool.Query(ctx, `
		SELECT i.id::text, p.identifier, i.sequence_id, i.name, i.priority,
		       i.state_id::text, s.name, s."group",
		       p.id::text, p.identifier, p.name, i.updated_at
		  FROM issues i
		  JOIN projects p ON p.id = i.project_id
		  JOIN states s ON s.id = i.state_id
		  `+join+`
		 WHERE `+where+`
		 ORDER BY i.updated_at DESC
		 LIMIT $3`,
		wsID, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []MyWorkItem{}
	for rows.Next() {
		var it MyWorkItem
		var projIdent string
		var seq int
		if err := rows.Scan(
			&it.ID, &projIdent, &seq, &it.Name, &it.Priority,
			&it.StateID, &it.StateName, &it.StateGroup,
			&it.ProjectID, &it.ProjectIdentifier, &it.ProjectName, &it.UpdatedAt,
		); err != nil {
			return nil, err
		}
		it.DisplayID = projIdent + "-" + strconv.Itoa(seq)
		items = append(items, it)
	}
	return items, rows.Err()
}
