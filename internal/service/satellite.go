package service

// Issue satellites (Task 18): comments (threaded), reactions, votes,
// subscribers, relations (single canonical row, reverse derived), versions
// (append-only snapshots), and history (issue_activities enriched).
//
// Conventions shared across every satellite:
//   - Tenancy: every op resolves workspace membership + project, then the
//     live issue, via resolveSatelliteIssue. A soft-deleted issue 404s
//     (ErrIssueNotFound) exactly like the issue itself does.
//   - Roles: any member (guest 5+) may read; mutations need member (15)+.
//   - Toggles are idempotent POST/DELETE pairs (never POST-toggles):
//     POST adds, DELETE removes, repeats are no-ops — never a 409.
//   - Versions are append-only: snapshotVersionTx records the
//     POST-mutation full row; restore applies an old snapshot as a NEW
//     version row, never rewriting history.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrCommentNotFound is returned for a missing (or soft-deleted) comment.
	ErrCommentNotFound = errors.New("service: comment not found")
	// ErrInvalidComment is returned for a bad comment payload or parent.
	ErrInvalidComment = errors.New("service: invalid comment")
	// ErrInvalidReaction is returned for a bad reaction emoji.
	ErrInvalidReaction = errors.New("service: invalid reaction")
	// ErrInvalidRelationType is returned for a relation type outside the
	// six canonical stored types (or an unknown reverse label on delete).
	ErrInvalidRelationType = errors.New("service: invalid relation type")
	// ErrInvalidRelation is returned for a self-relation.
	ErrInvalidRelation = errors.New("service: invalid relation")
	// ErrRelationNotFound is returned when the addressed relation row
	// does not exist.
	ErrRelationNotFound = errors.New("service: relation not found")
	// ErrVersionNotFound is returned for a missing version number.
	ErrVersionNotFound = errors.New("service: version not found")
)

// ActorRef is the denormalized actor embedded in satellite responses.
type ActorRef struct {
	ID    string  `json:"id"`
	Name  *string `json:"name,omitempty"`
	Email string  `json:"email"`
}

// resolveSatelliteIssue resolves (normalized identifier, projectID, role)
// and asserts the issue is live: soft-deleted (or missing) issues surface
// as ErrIssueNotFound, exactly like GetIssue. Every satellite op funnels
// through here.
//
// normalizeIssueID lowercases a UUID path parameter once at the service
// boundary. Postgres renders uuid::text in lowercase, so every string
// comparison against a DB-rendered ID (relation direction derivation,
// self-relation guards, symmetric normalization, parent-cycle guards)
// must use the lowercase form. SQL $N::uuid casts are case-insensitive,
// so normalizing first is behavior-neutral for the query paths and
// closes the mixed-case hole for the compare paths. Non-HTTP callers get
// the same canonical form.
func normalizeIssueID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}
func resolveSatelliteIssue(ctx context.Context, q queryRower, wsSlug, identifier, issueID, actorID string) (ident, projectID string, role int, err error) {
	ident, err = normalizeIdentifier(identifier)
	if err != nil {
		return "", "", 0, err
	}
	_, projectID, role, err = resolveIssueProject(ctx, q, wsSlug, ident, actorID)
	if err != nil {
		return "", "", 0, err
	}
	var id string
	err = q.QueryRow(ctx,
		`SELECT id::text FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
		issueID, projectID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return "", "", 0, ErrIssueNotFound
		}
		return "", "", 0, err
	}
	return ident, projectID, role, nil
}

// requireSatelliteWriter enforces member (15)+ for satellite mutations;
// guests may read but not write.
func requireSatelliteWriter(role int) error {
	if role < RoleMember {
		return ErrForbidden
	}
	return nil
}

// ---------- versions ----------

// IssueVersion is one immutable snapshot row.
type IssueVersion struct {
	VersionNo int             `json:"version_no"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedBy ActorRef        `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

// snapshotVersionTx appends an immutable version row carrying the
// POST-mutation full issue row as JSONB. Callers own the transaction; the
// issue row must already be locked (FOR UPDATE) or freshly inserted so
// version_no sequencing cannot interleave. There is deliberately no
// UPDATE path for versions anywhere.
func snapshotVersionTx(ctx context.Context, tx pgx.Tx, issueID, actorID string, iss *Issue) error {
	snap, err := json.Marshal(iss)
	if err != nil {
		return fmt.Errorf("service: marshal version snapshot: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO issue_versions (issue_id, version_no, snapshot, created_by)
		 VALUES ($1::uuid,
		         (SELECT COALESCE(MAX(version_no), 0) + 1 FROM issue_versions WHERE issue_id = $1::uuid),
		         $2::jsonb, $3::uuid)`,
		issueID, string(snap), actorID)
	return err
}

// ListVersions returns the issue's versions oldest-first. Any member may read.
func ListVersions(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*IssueVersion, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT v.version_no, v.snapshot, u.id::text, u.name, u.email, v.created_at
		 FROM issue_versions v JOIN users u ON u.id = v.created_by
		 WHERE v.issue_id = $1::uuid
		 ORDER BY v.version_no ASC`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*IssueVersion{}
	for rows.Next() {
		var ver IssueVersion
		var snap []byte
		if err := rows.Scan(&ver.VersionNo, &snap,
			&ver.CreatedBy.ID, &ver.CreatedBy.Name, &ver.CreatedBy.Email,
			&ver.CreatedAt); err != nil {
			return nil, err
		}
		ver.Snapshot = json.RawMessage(snap)
		out = append(out, &ver)
	}
	return out, rows.Err()
}

// GetVersion returns one version by number. Any member may read.
func GetVersion(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, n int) (*IssueVersion, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	var ver IssueVersion
	var snap []byte
	err := pool.QueryRow(ctx,
		`SELECT v.version_no, v.snapshot, u.id::text, u.name, u.email, v.created_at
		 FROM issue_versions v JOIN users u ON u.id = v.created_by
		 WHERE v.issue_id = $1::uuid AND v.version_no = $2`,
		issueID, n).Scan(&ver.VersionNo, &snap,
		&ver.CreatedBy.ID, &ver.CreatedBy.Name, &ver.CreatedBy.Email,
		&ver.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}
	ver.Snapshot = json.RawMessage(snap)
	return &ver, nil
}

// diffIssuePatch builds the IssuePatch that transforms cur into snap: only
// fields that actually differ are set, so updateIssueTx writes activity
// rows and a version snapshot for the real changes.
func diffIssuePatch(cur, snap *Issue) IssuePatch {
	var p IssuePatch
	if cur.Name != snap.Name {
		name := snap.Name
		p.Name = &name
	}
	if !bytes.Equal(cur.Description, snap.Description) {
		pf := PatchField[json.RawMessage]{Set: true}
		if snap.Description != nil {
			cp := append(json.RawMessage(nil), snap.Description...)
			pf.Value = &cp
		}
		p.Description = pf
	}
	if cur.Priority != snap.Priority {
		pri := snap.Priority
		p.Priority = &pri
	}
	if cur.StateID != snap.StateID {
		sid := snap.StateID
		p.StateID = &sid
	}
	if !strPtrEq(cur.ParentID, snap.ParentID) {
		pf := PatchField[string]{Set: true}
		if snap.ParentID != nil {
			cp := *snap.ParentID
			pf.Value = &cp
		}
		p.ParentID = pf
	}
	if cur.SortOrder != snap.SortOrder {
		so := snap.SortOrder
		p.SortOrder = &so
	}
	if !datePtrEq(cur.StartDate, snap.StartDate) {
		pf := PatchField[time.Time]{Set: true}
		if snap.StartDate != nil {
			cp := *snap.StartDate
			pf.Value = &cp
		}
		p.StartDate = pf
	}
	if !datePtrEq(cur.TargetDate, snap.TargetDate) {
		pf := PatchField[time.Time]{Set: true}
		if snap.TargetDate != nil {
			cp := *snap.TargetDate
			pf.Value = &cp
		}
		p.TargetDate = pf
	}
	if !strPtrEq(cur.EstimatePointID, snap.EstimatePointID) {
		pf := PatchField[string]{Set: true}
		if snap.EstimatePointID != nil {
			cp := *snap.EstimatePointID
			pf.Value = &cp
		}
		p.EstimatePointID = pf
	}
	if cur.IsDraft != snap.IsDraft {
		d := snap.IsDraft
		p.IsDraft = &d
	}
	return p
}

// isIssueDescendant walks the parent_id chain of candidateID upward and
// reports whether ancestorID appears — i.e. whether assigning candidateID
// as ancestorID's parent would close a cycle. Bounded at 100 hops as a
// defensive cap against pre-existing corrupt chains.
func isIssueDescendant(ctx context.Context, q queryRower, projectID, ancestorID, candidateID string) (bool, error) {
	cur := candidateID
	for i := 0; i < 100; i++ {
		var parent *string
		err := q.QueryRow(ctx,
			`SELECT parent_id::text FROM issues
			 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL`,
			cur, projectID).Scan(&parent)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if parent == nil {
			return false, nil
		}
		if *parent == ancestorID {
			return true, nil
		}
		cur = *parent
	}
	return false, nil
}

// RestoreIssueVersion applies version n's snapshot as the issue's new
// state. History is never rewritten: the restore lands as a NEW version
// row (max+1), per-field activity rows capture the actual changes, and a
// _restored marker records which version was restored. References in the
// snapshot are re-validated against the live project (a state deleted
// since the snapshot was taken rejects the restore); a parent that would
// now close a cycle is rejected too. Member (15)+.
func RestoreIssueVersion(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, n int) (*Issue, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, err
	}
	// Canonical lowercase BEFORE the self-parent (`pid == issueID`) and
	// cycle (`isIssueDescendant`) guards: both compare against
	// DB-rendered lowercase IDs, and an uppercase URL UUID would weaken
	// them.
	issueID = normalizeIssueID(issueID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	_, projectID, role, err := resolveIssueProject(ctx, tx, wsSlug, ident, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}

	// Lock the live row: concurrent restores/patches serialize here, so
	// the version_no sequencing below cannot interleave.
	cur, err := scanIssue(tx.QueryRow(ctx,
		`SELECT `+issueColumns+` FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL
		 FOR UPDATE`,
		issueID, projectID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrIssueNotFound
		}
		return nil, err
	}

	var snapJSON []byte
	if err := tx.QueryRow(ctx,
		`SELECT snapshot FROM issue_versions WHERE issue_id = $1::uuid AND version_no = $2`,
		issueID, n).Scan(&snapJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrVersionNotFound
		}
		return nil, err
	}
	var snap Issue
	if err := json.Unmarshal(snapJSON, &snap); err != nil {
		return nil, fmt.Errorf("service: decode version snapshot: %w", err)
	}

	// Re-validate the snapshot's references against the live project: a
	// restore must not resurrect a deleted state, a deleted parent, or a
	// parent that now closes a cycle.
	if _, err := checkStateInProject(ctx, tx, projectID, snap.StateID); err != nil {
		return nil, err
	}
	if snap.ParentID != nil {
		pid := *snap.ParentID
		if pid == issueID {
			return nil, ErrInvalidParent
		}
		if err := checkParentIssue(ctx, tx, projectID, pid); err != nil {
			return nil, err
		}
		isDesc, err := isIssueDescendant(ctx, tx, projectID, issueID, pid)
		if err != nil {
			return nil, err
		}
		if isDesc {
			return nil, ErrInvalidParent
		}
	}
	if snap.EstimatePointID != nil {
		if _, err := checkEstimatePoint(ctx, tx, projectID, *snap.EstimatePointID); err != nil {
			return nil, err
		}
	}

	patch := diffIssuePatch(cur, &snap)
	var updated *Issue
	if patch.hasFields() {
		// Reuses the PATCH machinery: per-field activity rows plus the
		// new version snapshot, all inside this tx.
		updated, err = updateIssueTx(ctx, tx, projectID, ident, issueID, actorID, patch)
		if err != nil {
			return nil, err
		}
	} else {
		// Nothing differs, but the restore was an explicit user action:
		// record it as a new version anyway.
		updated = cur
		updated.DisplayID = ident + "-" + strconv.Itoa(updated.SequenceID)
		if err := snapshotVersionTx(ctx, tx, issueID, actorID, updated); err != nil {
			return nil, err
		}
	}

	marker, _ := json.Marshal(map[string]any{"version_no": n})
	if _, err := tx.Exec(ctx,
		`INSERT INTO issue_activities (issue_id, actor_id, field, new_value)
		 VALUES ($1::uuid, $2::uuid, '_restored', $3::jsonb)`,
		issueID, actorID, string(marker)); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return updated, nil
}

// ---------- comments ----------

// Comment is one comment row with its actor denormalized.
type Comment struct {
	ID        string          `json:"id"`
	IssueID   string          `json:"issue_id"`
	ParentID  *string         `json:"parent_id,omitempty"`
	Actor     ActorRef        `json:"actor"`
	Content   json.RawMessage `json:"content"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CommentNode is a comment with its nested replies.
type CommentNode struct {
	Comment
	Replies []*CommentNode `json:"replies"`
}

const commentColumns = `c.id::text, c.issue_id::text, c.parent_id::text,
	c.actor_id::text, u.name, u.email, c.content, c.created_at, c.updated_at`

func scanComment(row pgx.Row) (*Comment, error) {
	var c Comment
	var content []byte
	err := row.Scan(&c.ID, &c.IssueID, &c.ParentID,
		&c.Actor.ID, &c.Actor.Name, &c.Actor.Email,
		&content, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	c.Content = json.RawMessage(content)
	return &c, nil
}

func getComment(ctx context.Context, q queryRower, issueID, commentID string) (*Comment, error) {
	c, err := scanComment(q.QueryRow(ctx,
		`SELECT `+commentColumns+`
		 FROM comments c JOIN users u ON u.id = c.actor_id
		 WHERE c.id = $1::uuid AND c.issue_id = $2::uuid AND c.deleted_at IS NULL`,
		commentID, issueID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrCommentNotFound
		}
		return nil, err
	}
	return c, nil
}

// CreateComment posts a comment; parentID nests it as a reply. The parent
// must belong to the same issue and not be deleted. Member (15)+.
func CreateComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string, content json.RawMessage, parentID *string) (*Comment, error) {
	ident, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return nil, err
	}
	content = bytes.TrimSpace(content)
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return nil, ErrInvalidComment
	}
	var parent *string
	if parentID != nil {
		pid := strings.ToLower(strings.TrimSpace(*parentID))
		if pid == "" {
			return nil, ErrInvalidComment
		}
		var ok bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM comments
			 WHERE id = $1::uuid AND issue_id = $2::uuid AND deleted_at IS NULL)`,
			pid, issueID).Scan(&ok)
		if err != nil {
			if isInvalidUUID(err) {
				return nil, ErrInvalidComment
			}
			return nil, err
		}
		if !ok {
			return nil, ErrInvalidComment
		}
		parent = &pid
	}

	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO comments (issue_id, parent_id, actor_id, content)
		 VALUES ($1::uuid, $2::uuid, $3::uuid, $4::jsonb)
		 RETURNING id::text`,
		issueID, parent, actorID, string(content)).Scan(&id)
	if err != nil {
		return nil, err
	}
	c, err := getComment(ctx, pool, issueID, id)
	if err != nil {
		return nil, err
	}
	announce(
		[]string{issueChannel(issueID), projectChannel(wsSlug, ident), workspaceChannel(wsSlug)},
		EventCommentCreated,
		map[string]string{"id": c.ID, "issue_id": issueID},
	)
	return c, nil
}

// ListComments returns the issue's comments as a nested tree, oldest
// first. Replies whose parent was soft-deleted surface as roots. Any
// member may read.
func ListComments(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*CommentNode, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT `+commentColumns+`
		 FROM comments c JOIN users u ON u.id = c.actor_id
		 WHERE c.issue_id = $1::uuid AND c.deleted_at IS NULL
		 ORDER BY c.created_at ASC, c.id ASC`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byID := map[string]*CommentNode{}
	order := []string{}
	for rows.Next() {
		c, err := scanComment(rows)
		if err != nil {
			return nil, err
		}
		byID[c.ID] = &CommentNode{Comment: *c, Replies: []*CommentNode{}}
		order = append(order, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	roots := []*CommentNode{}
	for _, id := range order {
		node := byID[id]
		if node.ParentID != nil {
			if parent, ok := byID[*node.ParentID]; ok {
				parent.Replies = append(parent.Replies, node)
				continue
			}
		}
		roots = append(roots, node)
	}
	return roots, nil
}

// UpdateComment edits a comment's content. The author or a workspace admin
// may edit; everyone else gets ErrForbidden.
func UpdateComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, commentID, actorID string, content json.RawMessage) (*Comment, error) {
	_, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	content = bytes.TrimSpace(content)
	if len(content) == 0 || bytes.Equal(content, []byte("null")) {
		return nil, ErrInvalidComment
	}
	c, err := getComment(ctx, pool, issueID, commentID)
	if err != nil {
		return nil, err
	}
	if c.Actor.ID != actorID && role < RoleAdmin {
		return nil, ErrForbidden
	}
	var id string
	err = pool.QueryRow(ctx,
		`UPDATE comments SET content = $1::jsonb, updated_at = now()
		 WHERE id = $2::uuid AND issue_id = $3::uuid AND deleted_at IS NULL
		 RETURNING id::text`,
		string(content), commentID, issueID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || isInvalidUUID(err) {
			return nil, ErrCommentNotFound
		}
		return nil, err
	}
	return getComment(ctx, pool, issueID, id)
}

// DeleteComment soft-deletes a comment. Author or admin; a second delete
// is ErrCommentNotFound.
func DeleteComment(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, commentID, actorID string) error {
	_, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	c, err := getComment(ctx, pool, issueID, commentID)
	if err != nil {
		return err
	}
	if c.Actor.ID != actorID && role < RoleAdmin {
		return ErrForbidden
	}
	tag, err := pool.Exec(ctx,
		`UPDATE comments SET deleted_at = now(), updated_at = now()
		 WHERE id = $1::uuid AND issue_id = $2::uuid AND deleted_at IS NULL`,
		commentID, issueID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCommentNotFound
	}
	return nil
}

// ---------- reactions ----------

// ReactionGroup aggregates one emoji on an issue: total count plus
// whether the calling user reacted.
type ReactionGroup struct {
	Emoji   string `json:"emoji"`
	Count   int    `json:"count"`
	Reacted bool   `json:"reacted"`
}

// AddIssueReaction adds the caller's emoji reaction; a repeat is a no-op.
// Member (15)+.
func AddIssueReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, emoji string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || len([]rune(emoji)) > 32 {
		return ErrInvalidReaction
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO issue_reactions (issue_id, user_id, emoji)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		issueID, actorID, emoji)
	return err
}

// RemoveIssueReaction removes the caller's emoji reaction; absent is a
// no-op. Member (15)+.
func RemoveIssueReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, emoji string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM issue_reactions
		 WHERE issue_id = $1::uuid AND user_id = $2::uuid AND emoji = $3`,
		issueID, actorID, emoji)
	return err
}

// ListIssueReactions groups the issue's reactions by emoji. Any member may read.
func ListIssueReactions(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*ReactionGroup, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT emoji, count(*)::int, bool_or(user_id = $2::uuid)
		 FROM issue_reactions
		 WHERE issue_id = $1::uuid
		 GROUP BY emoji
		 ORDER BY count(*) DESC, emoji ASC`,
		issueID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ReactionGroup{}
	for rows.Next() {
		var g ReactionGroup
		if err := rows.Scan(&g.Emoji, &g.Count, &g.Reacted); err != nil {
			return nil, err
		}
		out = append(out, &g)
	}
	return out, rows.Err()
}

// AddCommentReaction adds the caller's emoji reaction to a comment;
// idempotent. Member (15)+.
func AddCommentReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, commentID, actorID, emoji string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	if _, err := getComment(ctx, pool, issueID, commentID); err != nil {
		return err
	}
	emoji = strings.TrimSpace(emoji)
	if emoji == "" || len([]rune(emoji)) > 32 {
		return ErrInvalidReaction
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO comment_reactions (comment_id, user_id, emoji)
		 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
		commentID, actorID, emoji)
	return err
}

// RemoveCommentReaction removes the caller's emoji reaction from a
// comment; absent is a no-op. Member (15)+.
func RemoveCommentReaction(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, commentID, actorID, emoji string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	if _, err := getComment(ctx, pool, issueID, commentID); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM comment_reactions
		 WHERE comment_id = $1::uuid AND user_id = $2::uuid AND emoji = $3`,
		commentID, actorID, emoji)
	return err
}

// ---------- votes ----------

// IssueVotes is the vote count plus whether the caller voted.
type IssueVotes struct {
	Count int  `json:"count"`
	Voted bool `json:"voted"`
}

// VoteIssue records the caller's vote; one per user, repeats are no-ops.
// Member (15)+.
func VoteIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO issue_votes (issue_id, user_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		issueID, actorID)
	return err
}

// UnvoteIssue removes the caller's vote; absent is a no-op. Member (15)+.
func UnvoteIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM issue_votes WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issueID, actorID)
	return err
}

// GetIssueVotes returns the vote count and the caller's vote state. Any
// member may read.
func GetIssueVotes(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) (*IssueVotes, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	var v IssueVotes
	err := pool.QueryRow(ctx,
		`SELECT count(*)::int, COALESCE(bool_or(user_id = $2::uuid), false)
		 FROM issue_votes WHERE issue_id = $1::uuid`,
		issueID, actorID).Scan(&v.Count, &v.Voted)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// ---------- subscribers ----------

// Subscriber is one subscribed user.
type Subscriber struct {
	UserID string  `json:"user_id"`
	Name   *string `json:"name,omitempty"`
	Email  string  `json:"email"`
}

// SubscribeIssue subscribes the caller; repeats are no-ops. Member (15)+.
func SubscribeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO issue_subscribers (issue_id, user_id)
		 VALUES ($1::uuid, $2::uuid) ON CONFLICT DO NOTHING`,
		issueID, actorID)
	return err
}

// UnsubscribeIssue removes the caller's subscription; absent is a no-op.
// Member (15)+.
func UnsubscribeIssue(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) error {
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	_, err := pool.Exec(ctx,
		`DELETE FROM issue_subscribers WHERE issue_id = $1::uuid AND user_id = $2::uuid`,
		issueID, actorID)
	return err
}

// ListSubscribers returns the issue's subscribers. Any member may read.
func ListSubscribers(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*Subscriber, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT u.id::text, u.name, u.email
		 FROM issue_subscribers s JOIN users u ON u.id = s.user_id
		 WHERE s.issue_id = $1::uuid
		 ORDER BY s.created_at ASC`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Subscriber{}
	for rows.Next() {
		var s Subscriber
		if err := rows.Scan(&s.UserID, &s.Name, &s.Email); err != nil {
			return nil, err
		}
		out = append(out, &s)
	}
	return out, rows.Err()
}

// ---------- relations ----------

// relationReverseType maps each canonical stored type to the label seen
// from the related issue's side. relates_to and duplicate are symmetric.
var relationReverseType = map[string]string{
	"blocked_by":     "blocking",
	"relates_to":     "relates_to",
	"duplicate":      "duplicate",
	"start_before":   "start_after",
	"finish_before":  "finish_after",
	"implemented_by": "implements",
}

// relationCanonicalType is the inverse: any label a client may address
// (canonical or reverse) back to the stored canonical type.
var relationCanonicalType = func() map[string]string {
	m := map[string]string{}
	for canon, rev := range relationReverseType {
		m[canon] = canon
		m[rev] = canon
	}
	return m
}()

// Relation is one relation as seen from the queried issue.
type Relation struct {
	IssueID   string `json:"issue_id"`
	DisplayID string `json:"display_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`      // as seen from this issue's side
	Direction string `json:"direction"` // outgoing | incoming
}

// isSymmetricRelationType reports whether the canonical type reads the
// same from both sides — these are stored normalized (lexicographically
// smaller issue first) so the two creation directions collapse to one row.
func isSymmetricRelationType(t string) bool {
	return t == "relates_to" || t == "duplicate"
}

// CreateRelation links issueID to relatedID with a canonical type. Exactly
// one canonical row exists per (issue, related, type); repeats are
// no-ops. The related issue must be live and in the same project.
// Member (15)+.
func CreateRelation(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, relatedID, relType string) error {
	issueID = normalizeIssueID(issueID)
	_, projectID, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return err
	}
	if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	if _, ok := relationReverseType[relType]; !ok {
		return ErrInvalidRelationType
	}
	rel := strings.ToLower(strings.TrimSpace(relatedID))
	if rel == "" || rel == issueID {
		return ErrInvalidRelation
	}
	var ok bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM issues
		 WHERE id = $1::uuid AND project_id = $2::uuid AND deleted_at IS NULL)`,
		rel, projectID).Scan(&ok)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrIssueNotFound
		}
		return err
	}
	if !ok {
		return ErrIssueNotFound
	}
	fromID, toID := issueID, rel
	if isSymmetricRelationType(relType) && toID < fromID {
		fromID, toID = toID, fromID
	}
	_, err = pool.Exec(ctx,
		`INSERT INTO issue_relations (issue_id, related_issue_id, type, created_by)
		 VALUES ($1::uuid, $2::uuid, $3, $4::uuid) ON CONFLICT DO NOTHING`,
		fromID, toID, relType, actorID)
	return err
}

// DeleteRelation removes the relation between issueID and otherID.
// typeAsSeen is the label from issueID's side: a canonical type addresses
// the outgoing row, a reverse label (e.g. "blocking") resolves to the
// canonical row stored from the other side. Member (15)+.
func DeleteRelation(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID, otherID, typeAsSeen string) error {
	issueID = normalizeIssueID(issueID)
	if _, _, role, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return err
	} else if err := requireSatelliteWriter(role); err != nil {
		return err
	}
	canon, known := relationCanonicalType[typeAsSeen]
	if !known {
		return ErrInvalidRelationType
	}
	other := strings.ToLower(strings.TrimSpace(otherID))
	// Resolve which canonical row the client addressed. Symmetric types
	// are stored normalized; a canonical label addresses the outgoing
	// row; a reverse-only label (blocking, start_after, …) addresses the
	// row stored from the other side.
	fromID, toID := issueID, other
	_, typeAsSeenIsCanon := relationReverseType[typeAsSeen]
	switch {
	case isSymmetricRelationType(canon):
		if toID < fromID {
			fromID, toID = toID, fromID
		}
	case typeAsSeenIsCanon:
		// Canonical label → outgoing row (fromID, toID as initialized).
	default:
		// Reverse-only label → the canonical row stored from the other side.
		fromID, toID = other, issueID
	}
	tag, err := pool.Exec(ctx,
		`DELETE FROM issue_relations
		 WHERE issue_id = $1::uuid AND related_issue_id = $2::uuid AND type = $3`,
		fromID, toID, canon)
	if err != nil {
		if isInvalidUUID(err) {
			return ErrRelationNotFound
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRelationNotFound
	}
	return nil
}

// ListRelations returns every relation touching the issue: canonical rows
// stored from this issue (outgoing) plus rows stored from the other side
// with the reverse label derived (incoming). Any member may read.
func ListRelations(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*Relation, error) {
	issueID = normalizeIssueID(issueID)
	ident, projectID, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT r.issue_id::text, r.related_issue_id::text, r.type,
		        o.id::text, o.sequence_id, o.name
		 FROM issue_relations r
		 JOIN issues o ON o.id = CASE WHEN r.issue_id = $1::uuid
		                              THEN r.related_issue_id ELSE r.issue_id END
		 WHERE (r.issue_id = $1::uuid OR r.related_issue_id = $1::uuid)
		   AND o.deleted_at IS NULL AND o.project_id = $2::uuid
		 ORDER BY o.sequence_id ASC`,
		issueID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Relation{}
	for rows.Next() {
		var fromID, toID, storedType, otherID, otherName string
		var otherSeq int
		if err := rows.Scan(&fromID, &toID, &storedType, &otherID, &otherSeq, &otherName); err != nil {
			return nil, err
		}
		rel := &Relation{
			IssueID:   otherID,
			DisplayID: ident + "-" + strconv.Itoa(otherSeq),
			Name:      otherName,
		}
		// Symmetric types are stored normalized, so "direction" there is
		// just which side sorts first — the type itself is what matters.
		if fromID == issueID {
			rel.Type = storedType
			rel.Direction = "outgoing"
		} else {
			rel.Type = relationReverseType[storedType]
			rel.Direction = "incoming"
		}
		out = append(out, rel)
	}
	return out, rows.Err()
}

// ---------- history ----------

// HistoryEntry is one issue_activities row with its actor denormalized,
// oldest first.
type HistoryEntry struct {
	ID        string          `json:"id"`
	Field     string          `json:"field"`
	OldValue  json.RawMessage `json:"old_value,omitempty"`
	NewValue  json.RawMessage `json:"new_value,omitempty"`
	Actor     ActorRef        `json:"actor"`
	CreatedAt time.Time       `json:"created_at"`
}

// GetIssueHistory returns the issue's activity rows in chronological
// order, enriched with actor info. Any member may read.
func GetIssueHistory(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, issueID, actorID string) ([]*HistoryEntry, error) {
	if _, _, _, err := resolveSatelliteIssue(ctx, pool, wsSlug, identifier, issueID, actorID); err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx,
		`SELECT a.id::text, a.field, a.old_value, a.new_value, a.created_at,
		        u.id::text, u.name, u.email
		 FROM issue_activities a JOIN users u ON u.id = a.actor_id
		 WHERE a.issue_id = $1::uuid
		 ORDER BY a.created_at ASC, a.id ASC`,
		issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*HistoryEntry{}
	for rows.Next() {
		var h HistoryEntry
		var oldVal, newVal []byte
		if err := rows.Scan(&h.ID, &h.Field, &oldVal, &newVal, &h.CreatedAt,
			&h.Actor.ID, &h.Actor.Name, &h.Actor.Email); err != nil {
			return nil, err
		}
		if oldVal != nil {
			h.OldValue = json.RawMessage(oldVal)
		}
		if newVal != nil {
			h.NewValue = json.RawMessage(newVal)
		}
		out = append(out, &h)
	}
	return out, rows.Err()
}
