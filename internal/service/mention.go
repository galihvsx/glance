package service

// @-mentions in comments (C8T1): parse @<name> tokens from comment bodies,
// resolve them against workspace members, and emit `mention` notifications
// on comment create and update.
//
// Resolution rules (contract-first, see cycle8-plan C8T1):
//   - Case-insensitive match against users.name; email local-part is the
//     fallback for users without a (matching) name.
//   - Multi-word display names match by longest-prefix scan at each @
//     ("@Galih Putro" resolves the member named "Galih Putro"; "@Galih"
//     alone does NOT — no partial-name guessing).
//   - Ambiguous match (two members, same normalized name or local-part) →
//     skipped; the comment posts fine, nobody is pinged.
//   - Self-mention → skipped (notifyTx also excludes the actor).
//   - Non-member name → skipped.
//   - Email addresses (user@domain) never parse as mentions: @ must not be
//     preceded by a word character.
//   - Mentions only ever resolve within the workspace the issue's project
//     belongs to — the member list is scoped by workspace_id, so a name
//     from another workspace can never resolve.
//
// Spam note: every comment create/update that mentions a user produces one
// notification per resolved user per comment. Mentioning one user in 50
// comments yields 50 notifications — each comment is a distinct event, so
// this is accepted behavior; users can disable the `mention` pref.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// MentionTarget is the identity surface of one workspace member used for
// @-mention resolution.
type MentionTarget struct {
	UserID string
	Name   string // users.name, may be ""
	Email  string // users.email
}

// mentionRune reports whether r may appear inside a @-token.
func mentionRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

// atBoundary reports whether the rune before an @ position may start a
// mention. Word characters (and dots) before @ mean the @ belongs to an
// email address or similar — not a mention.
func atBoundary(prev rune, hasPrev bool) bool {
	if !hasPrev {
		return true
	}
	return !unicode.IsLetter(prev) && !unicode.IsDigit(prev) && prev != '_' && prev != '.' && prev != '@'
}

// ParseMentionTokens scans plain text for @<token> candidates. A candidate
// starts at an @ that is not preceded by a word character (so email
// addresses like user@domain never produce tokens) and runs over
// [letters digits _ - .], with trailing dots/dashes trimmed ("@galih."
// at a sentence end → "galih"). It returns the raw token strings without
// the @; resolution against member names is ResolveMentions' job.
func ParseMentionTokens(text string) []string {
	var out []string
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '@' {
			continue
		}
		var prev rune
		hasPrev := i > 0
		if hasPrev {
			prev = rs[i-1]
		}
		if !atBoundary(prev, hasPrev) {
			continue
		}
		j := i + 1
		for j < len(rs) && mentionRune(rs[j]) {
			j++
		}
		token := strings.TrimRight(string(rs[i+1:j]), ".-")
		if token == "" {
			continue
		}
		out = append(out, token)
		i = j - 1
	}
	return out
}

// TipTapPlainText extracts readable text from a TipTap doc JSON, one block
// joined by spaces. Text inside codeBlock nodes is skipped best-effort so
// "@someone" in a code sample does not become a mention. Invalid JSON
// yields "".
func TipTapPlainText(doc json.RawMessage) string {
	trimmed := strings.TrimSpace(string(doc))
	if trimmed == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return ""
	}
	var parts []string
	var walk func(n any)
	walk = func(n any) {
		m, ok := n.(map[string]any)
		if !ok {
			if arr, ok := n.([]any); ok {
				for _, c := range arr {
					walk(c)
				}
			}
			return
		}
		if t, _ := m["type"].(string); t == "codeBlock" {
			return // best-effort: code fences never yield mentions
		}
		if t, _ := m["type"].(string); t == "text" {
			if s, _ := m["text"].(string); s != "" {
				parts = append(parts, s)
			}
			return
		}
		if c, ok := m["content"].([]any); ok {
			for _, ch := range c {
				walk(ch)
			}
		}
	}
	walk(v)
	return strings.Join(parts, " ")
}

// normalizeMentionKey folds a name or local-part for case-insensitive
// comparison.
func normalizeMentionKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// emailLocalPart returns the part of an email before the @, lowercased.
func emailLocalPart(email string) string {
	if i := strings.IndexByte(email, '@'); i >= 0 {
		return strings.ToLower(email[:i])
	}
	return strings.ToLower(email)
}

// mentionIndexes returns the rune offsets of every @ in text that may
// start a mention (boundary rule, see ParseMentionTokens).
func mentionIndexes(text string) []int {
	var out []int
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '@' {
			continue
		}
		var prev rune
		hasPrev := i > 0
		if hasPrev {
			prev = rs[i-1]
		}
		if atBoundary(prev, hasPrev) {
			out = append(out, i)
		}
	}
	return out
}

// isNameBoundary reports whether the text right after a matched name ends
// the mention: end of string or a non-word rune.
func isNameBoundary(rest string) bool {
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
}

// ResolveMentions maps @-mentions in text to workspace member user IDs.
// At each @ it first tries the longest multi-word display-name prefix
// match (case-insensitive, word boundary required after the name), then
// falls back to the single @token matched against member names and email
// local-parts. Ambiguous matches and self-mentions are skipped; the
// result is deduplicated, in first-mention order.
func ResolveMentions(text, actorID string, members []MentionTarget) []string {
	if len(members) == 0 {
		return nil
	}
	// Index unambiguous normalized names and local-parts.
	nameToID := map[string]string{}
	localToID := map[string]string{}
	ambiguousName := map[string]bool{}
	ambiguousLocal := map[string]bool{}
	type nameKey struct {
		key string
		id  string
	}
	var names []nameKey
	for _, m := range members {
		if key := normalizeMentionKey(m.Name); key != "" {
			if _, dup := nameToID[key]; dup {
				ambiguousName[key] = true
				delete(nameToID, key)
			} else if !ambiguousName[key] {
				nameToID[key] = m.UserID
			}
			names = append(names, nameKey{key: key, id: m.UserID})
		}
		if lp := emailLocalPart(m.Email); lp != "" {
			if _, dup := localToID[lp]; dup {
				ambiguousLocal[lp] = true
				delete(localToID, lp)
			} else if !ambiguousLocal[lp] {
				localToID[lp] = m.UserID
			}
		}
	}

	rs := []rune(text)
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if id == "" || strings.EqualFold(id, actorID) || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	for _, at := range mentionIndexes(text) {
		rest := strings.ToLower(string(rs[at+1:]))
		// Longest multi-word display-name prefix match.
		var bestID, bestKey string
		for _, nk := range names {
			if ambiguousName[nk.key] {
				continue
			}
			if !strings.HasPrefix(rest, nk.key) {
				continue
			}
			after := rest[len(nk.key):]
			if !isNameBoundary(after) {
				continue
			}
			if len(nk.key) > len(bestKey) {
				bestKey = nk.key
				bestID = nk.id
			}
		}
		if bestID != "" {
			add(bestID)
			continue
		}
		// Single-token fallback: name or email local-part.
		j := at + 1
		for j < len(rs) && mentionRune(rs[j]) {
			j++
		}
		token := normalizeMentionKey(strings.TrimRight(string(rs[at+1:j]), ".-"))
		if token == "" {
			continue
		}
		if id, ok := nameToID[token]; ok {
			add(id)
			continue
		}
		if id, ok := localToID[token]; ok {
			add(id)
		}
	}
	return out
}

// mentionTargetsTx lists the workspace's active members with their
// identity surface for @-mention resolution.
func mentionTargetsTx(ctx context.Context, tx pgx.Tx, wsID string) ([]MentionTarget, error) {
	rows, err := tx.Query(ctx,
		`SELECT u.id::text, COALESCE(u.name, ''), u.email::text
		   FROM workspace_members m
		   JOIN users u ON u.id = m.user_id
		  WHERE m.workspace_id = $1::uuid AND u.is_active`,
		wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MentionTarget
	for rows.Next() {
		var t MentionTarget
		if err := rows.Scan(&t.UserID, &t.Name, &t.Email); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// resolveCommentMentions parses @-mentions from a comment's TipTap content
// and resolves them against the workspace's members. Best-effort parse:
// unresolvable or ambiguous tokens are skipped, never an error.
func resolveCommentMentions(ctx context.Context, tx pgx.Tx, wsID, actorID string, content json.RawMessage) ([]string, error) {
	text := TipTapPlainText(content)
	if !strings.Contains(text, "@") {
		return nil, nil
	}
	members, err := mentionTargetsTx(ctx, tx, wsID)
	if err != nil {
		return nil, err
	}
	return ResolveMentions(text, actorID, members), nil
}

// notifyMentionsTx emits `mention` notifications for the resolved user IDs
// via notifyTx (which honors notification_prefs and excludes the actor).
// Returns the created rows for post-commit WS broadcast — call
// announceNotifications AFTER the tx commits.
func notifyMentionsTx(ctx context.Context, tx pgx.Tx, actorName, displayID, issueName string, payload map[string]any, actorID string, mentionedIDs []string) ([]*Notification, error) {
	if len(mentionedIDs) == 0 {
		return nil, nil
	}
	return notifyTx(ctx, tx, NotifyMention,
		fmt.Sprintf("%s mentioned you in %s", actorName, displayID),
		fmt.Sprintf("Issue: %s", issueName),
		payload,
		actorID, mentionedIDs)
}
