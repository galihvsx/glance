package service

// Calendar feed data (C15T3): the dated-issue working set behind the
// calendar.ics subscription feeds. Membership enforcement is the same as
// the issue list — resolveIssueProject (any workspace member, guests
// included); the cycle feed additionally requires the cycle to belong to
// the project (via GetCycle, which enforces both).
//
// Only issues with a due date (target_date) appear in a feed, archived
// issues never do, drafts of the subscriber are included (the feed is
// per-user: it mirrors what the issue list shows that user). Rows are
// bounded by MaxFeedRows — calendar subscriptions are polled snapshots,
// not data exports, so an over-cap set answers 400 honestly instead of
// silently truncating.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MaxFeedRows caps the issues a single calendar.ics response may carry.
// A calendar subscription holds the whole set in memory on both ends;
// var (not const) so tests can exercise the over-cap path without
// seeding 10k issues. Production code must not mutate it.
var MaxFeedRows = 10000

// ErrFeedTooLarge is returned before a single VEVENT is emitted when the
// feed scope matches more than MaxFeedRows issues. The error text names
// the matched count so the handler can answer honestly (400 with the
// count — never a silently truncated calendar).
var ErrFeedTooLarge = errors.New("service: calendar feed row limit exceeded")

// FeedIssue is one feed row: the display id for the SUMMARY, the raw
// description doc (plain-text extraction happens at render time), and
// the dates the VEVENT is built from.
type FeedIssue struct {
	ID          string
	DisplayID   string
	Name        string
	Description json.RawMessage
	StartDate   *string // YYYY-MM-DD
	TargetDate  string  // YYYY-MM-DD
}

// FeedScope identifies which dated issues the feed carries: a whole
// project, or one cycle within it.
type FeedScope struct {
	// CalName is the calendar display name (project or cycle name).
	CalName string
	// CycleID is "" for a project feed.
	CycleID string
}

// FeedIssues returns the feed's dated issues: project-scoped membership
// resolution, then (for a cycle feed) cycle validation, then a single
// count + select. CycleID "" selects the project feed.
func FeedIssues(ctx context.Context, pool *pgxpool.Pool, wsSlug, identifier, actorID, cycleID string) ([]*FeedIssue, *FeedScope, error) {
	ident, err := normalizeIdentifier(identifier)
	if err != nil {
		return nil, nil, err
	}
	_, projectID, _, err := resolveIssueProject(ctx, pool, wsSlug, ident, actorID)
	if err != nil {
		return nil, nil, err
	}

	scope := &FeedScope{}
	var cycleFilter string
	var cycleArg any
	if cycleID != "" {
		cycle, err := GetCycle(ctx, pool, wsSlug, ident, actorID, cycleID)
		if err != nil {
			return nil, nil, err
		}
		scope.CalName = cycle.Name
		scope.CycleID = cycle.ID
		cycleFilter = `AND EXISTS (SELECT 1 FROM cycle_issues ci WHERE ci.cycle_id = $2::uuid AND ci.issue_id = i.id)`
		cycleArg = cycle.ID
	} else {
		var projectName string
		if err := pool.QueryRow(ctx,
			`SELECT name FROM projects WHERE id = $1::uuid`, projectID).Scan(&projectName); err != nil {
			return nil, nil, err
		}
		scope.CalName = ident + " " + projectName
	}

	var n int64
	countQuery := `SELECT COUNT(*) FROM issues i
		WHERE i.project_id = $1::uuid
		  AND i.archived_at IS NULL
		  AND i.target_date IS NOT NULL ` + cycleFilter
	countArgs := []any{projectID}
	if cycleArg != nil {
		countArgs = append(countArgs, cycleArg)
	}
	if err := pool.QueryRow(ctx, countQuery, countArgs...).Scan(&n); err != nil {
		return nil, nil, fmt.Errorf("service: calendar feed count: %w", err)
	}
	if n > int64(MaxFeedRows) {
		return nil, nil, fmt.Errorf("%w: %d issues have due dates, cap is %d",
			ErrFeedTooLarge, n, MaxFeedRows)
	}

	query := `SELECT i.id::text, i.sequence_id, i.name, i.description,
		to_char(i.start_date, 'YYYY-MM-DD'), to_char(i.target_date, 'YYYY-MM-DD')
		FROM issues i
		WHERE i.project_id = $1::uuid
		  AND i.archived_at IS NULL
		  AND i.target_date IS NOT NULL ` + cycleFilter + `
		ORDER BY i.target_date ASC, i.sequence_id ASC, i.id ASC`
	rows, err := pool.Query(ctx, query, countArgs...)
	if err != nil {
		return nil, nil, fmt.Errorf("service: calendar feed select: %w", err)
	}
	defer rows.Close()

	var issues []*FeedIssue
	for rows.Next() {
		var item FeedIssue
		var seq int
		var desc []byte
		var startDate *string
		if err := rows.Scan(&item.ID, &seq, &item.Name, &desc, &startDate, &item.TargetDate); err != nil {
			return nil, nil, fmt.Errorf("service: calendar feed scan: %w", err)
		}
		if desc != nil {
			item.Description = json.RawMessage(desc)
		}
		item.StartDate = startDate
		item.DisplayID = ident + "-" + strconv.Itoa(seq)
		issues = append(issues, &item)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("service: calendar feed rows: %w", err)
	}
	return issues, scope, nil
}

// TiptapPlainText extracts readable text from a Tiptap JSON document (the
// shape issue descriptions are stored in): the "text" of every text
// node, paragraphs joined by newlines. Unknown shapes yield "" — the
// feed never fails on a description it cannot parse.
func TiptapPlainText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var doc struct {
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	var lines []string
	for _, block := range doc.Content {
		var sb strings.Builder
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
		for _, inline := range block.Content {
			if inline.Type == "text" {
				sb.WriteString(inline.Text)
			}
		}
		if s := strings.TrimSpace(sb.String()); s != "" {
			lines = append(lines, s)
		}
	}
	return strings.Join(lines, "\n")
}
