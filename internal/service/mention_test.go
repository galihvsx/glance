package service

// @-mentions in comments (C8T1): parser edge cases, resolution rules, and
// the notify-on-create/update path with prefs honored. All tests run
// against the real test database — no skips. Harness from
// workspace_test.go / issue_test.go / notify_test.go.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------- pure parser tests ----------

func TestParseMentionTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple", "hi @galih", []string{"galih"}},
		{"at start", "@galih please review", []string{"galih"}},
		{"at end", "please review @galih", []string{"galih"}},
		{"paren wrapped", "(@galih) looks good", []string{"galih"}},
		{"trailing dot trimmed", "thanks @galih.", []string{"galih"}},
		{"trailing comma", "hi @galih, @budi!", []string{"galih", "budi"}},
		{"hyphenated", "@anne-marie ok", []string{"anne-marie"}},
		{"dotted", "@john.doe ok", []string{"john.doe"}},
		{"underscore", "@_lead ok", []string{"_lead"}},
		{"digits", "@dev2 ok", []string{"dev2"}},
		{"unicode", "halo @BudiSantoso", []string{"BudiSantoso"}},
		{"lone at", "email me @ or call", nil},
		{"email not a mention", "write to galih@example.com today", nil},
		{"email after text", "cc galih@example.com and @budi", []string{"budi"}},
		{"double at not a mention", "@@galih", nil},
		{"newline separated", "line one\n@galih line two", []string{"galih"}},
		{"empty", "", nil},
		{"no mentions", "just a comment", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMentionTokens(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ParseMentionTokens(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestTipTapPlainTextSkipsCodeBlocks(t *testing.T) {
	doc := json.RawMessage(`{"type":"doc","content":[
		{"type":"paragraph","content":[{"type":"text","text":"ping @galih"}]},
		{"type":"codeBlock","content":[{"type":"text","text":"@notamention = 1"}]},
		{"type":"paragraph","content":[{"type":"text","text":"thanks @budi"}]}
	]}`)
	got := TipTapPlainText(doc)
	if got != "ping @galih thanks @budi" {
		t.Fatalf("TipTapPlainText = %q", got)
	}
	if toks := ParseMentionTokens(got); !reflect.DeepEqual(toks, []string{"galih", "budi"}) {
		t.Fatalf("tokens from extracted text = %v", toks)
	}
}

func TestTipTapPlainTextInvalid(t *testing.T) {
	if got := TipTapPlainText(json.RawMessage(`{oops`)); got != "" {
		t.Fatalf("invalid JSON = %q, want empty", got)
	}
	if got := TipTapPlainText(json.RawMessage(`null`)); got != "" {
		t.Fatalf("null doc = %q, want empty", got)
	}
}

// ---------- resolution tests ----------

func mentionFixtureMembers() []MentionTarget {
	return []MentionTarget{
		{UserID: "u-galih", Name: "Galih Putro", Email: "galih.putro@example.com"},
		{UserID: "u-budi", Name: "budi", Email: "budi.santoso@example.com"},
		{UserID: "u-anne", Name: "Anne-Marie", Email: "anne@example.com"},
		{UserID: "u-noname", Name: "", Email: "ops.bot@example.com"},
	}
}

func TestResolveMentions(t *testing.T) {
	members := mentionFixtureMembers()
	cases := []struct {
		name  string
		text  string
		actor string
		want  []string
	}{
		{"multi-word name", "hey @Galih Putro review this", "u-x", []string{"u-galih"}},
		{"multi-word name case-insensitive", "hey @galih putro review", "u-x", []string{"u-galih"}},
		{"name boundary required", "@Galih Putrosian", "u-x", nil},
		{"single-word name case-insensitive", "hi @BUDI", "u-x", []string{"u-budi"}},
		{"local-part fallback", "hi @galih.putro", "u-x", []string{"u-galih"}},
		{"local-part for nameless user", "hi @ops.bot", "u-x", []string{"u-noname"}},
		{"hyphenated name token", "cc @anne-marie", "u-x", []string{"u-anne"}},
		{"self mention skipped", "note to self @budi", "u-budi", nil},
		{"unknown skipped", "hi @stranger", "u-x", nil},
		{"email not a mention", "write galih@example.com", "u-x", nil},
		{"deduped", "@budi and @BUDI again", "u-x", []string{"u-budi"}},
		{"multiple", "@galih putro meet @budi", "u-x", []string{"u-galih", "u-budi"}},
		{"first name only does not match multi-word", "@Galih review", "u-x", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveMentions(tc.text, tc.actor, members)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ResolveMentions(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestResolveMentionsAmbiguousSkipped(t *testing.T) {
	members := []MentionTarget{
		{UserID: "u-1", Name: "Budi", Email: "budi1@example.com"},
		{UserID: "u-2", Name: "budi", Email: "budi2@example.com"},
		{UserID: "u-3", Name: "Sari", Email: "shared@example.com"},
		{UserID: "u-4", Name: "Dewi", Email: "shared@example.com"},
	}
	if got := ResolveMentions("hi @budi", "u-x", members); len(got) != 0 {
		t.Fatalf("ambiguous name resolved = %v, want skipped", got)
	}
	if got := ResolveMentions("hi @shared", "u-x", members); len(got) != 0 {
		t.Fatalf("ambiguous local-part resolved = %v, want skipped", got)
	}
	if got := ResolveMentions("hi @sari", "u-x", members); !reflect.DeepEqual(got, []string{"u-3"}) {
		t.Fatalf("unambiguous @sari = %v, want [u-3]", got)
	}
}

// ---------- integration tests ----------

// createMentionFixture builds a workspace with three members (actor, a
// watcher, and a mentionee) plus one issue. memberB has a name; watcher is
// subscribed to the issue so comment.created would reach them.
func createMentionFixture(t *testing.T, pool *pgxpool.Pool) (actorID, watcherID, mentioneeID, wsSlug, ident, issueID string) {
	t.Helper()
	ctx := context.Background()
	actorID = createTestUser(t, pool, uniqueTestEmail("mention-actor"))
	watcherID = createTestUser(t, pool, uniqueTestEmail("mention-watcher"))
	mentioneeID = createTestUser(t, pool, uniqueTestEmail("mention-target"))
	for id, name := range map[string]string{
		actorID:     "Actor Andy",
		watcherID:   "Watcher Wulan",
		mentioneeID: "Mentee Galih",
	} {
		if _, err := pool.Exec(ctx, `UPDATE users SET name = $2 WHERE id = $1::uuid`, id, name); err != nil {
			t.Fatalf("set name: %v", err)
		}
	}
	ws := createTestWorkspace(t, pool, "Acme", uniqueTestSlug("acme-mention"), actorID)
	for _, id := range []string{watcherID, mentioneeID} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO workspace_members (workspace_id, user_id, role)
			 VALUES ($1::uuid, $2::uuid, $3) ON CONFLICT DO NOTHING`,
			ws.ID, id, RoleMember); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	p := createTestProject(t, pool, ws.Slug, actorID, "Engineering", uniqueTestIdentifier())
	iss := createTestIssue(t, pool, ws.Slug, p.Identifier, actorID, "Mention me")
	if err := SubscribeIssue(ctx, pool, ws.Slug, p.Identifier, iss.ID, watcherID); err != nil {
		t.Fatalf("subscribe watcher: %v", err)
	}
	return actorID, watcherID, mentioneeID, ws.Slug, p.Identifier, iss.ID
}

func tiptapComment(text string) json.RawMessage {
	doc := map[string]any{
		"type": "doc",
		"content": []any{
			map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": text}},
			},
		},
	}
	raw, _ := json.Marshal(doc)
	return raw
}

func notificationTypes(t *testing.T, pool *pgxpool.Pool, userID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT type FROM notifications WHERE user_id = $1::uuid ORDER BY created_at`, userID)
	if err != nil {
		t.Fatalf("query notifications: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

// TestCreateCommentMentionNotifies: a mentioned member gets exactly one
// `mention` notification; the mentioned watcher does NOT also get
// comment.created; the unmentioned watcher gets comment.created; the actor
// gets nothing.
func TestCreateCommentMentionNotifies(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, watcherID, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	_, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Mentee Galih take a look"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}

	if got := notificationTypes(t, pool, mentioneeID); !reflect.DeepEqual(got, []string{NotifyMention}) {
		t.Fatalf("mentionee notifications = %v, want [mention]", got)
	}
	if got := notificationTypes(t, pool, watcherID); !reflect.DeepEqual(got, []string{NotifyCommentCreated}) {
		t.Fatalf("watcher notifications = %v, want [comment.created]", got)
	}
	if n := countNotifications(t, pool, actorID); n != 0 {
		t.Fatalf("actor notifications = %d, want 0", n)
	}
	var title string
	if err := pool.QueryRow(ctx,
		`SELECT title FROM notifications WHERE user_id = $1::uuid`, mentioneeID).Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	if title == "" {
		t.Fatal("mention notification title is empty")
	}
}

// TestCreateCommentMentionPrefRespected: a user who disabled the `mention`
// pref gets no notification.
func TestCreateCommentMentionPrefRespected(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	if _, err := SetNotificationPref(ctx, pool, mentioneeID, NotifyMention, false, false); err != nil {
		t.Fatalf("SetNotificationPref: %v", err)
	}
	_, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Mentee Galih take a look"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if n := countNotifications(t, pool, mentioneeID); n != 0 {
		t.Fatalf("mentionee notifications = %d, want 0 (pref off)", n)
	}
}

// TestCreateCommentMentionPrefListed: the `mention` event appears in the
// pref list with defaults (in_app on, email off).
func TestCreateCommentMentionPrefListed(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	userID := createTestUser(t, pool, uniqueTestEmail("mention-preflist"))
	prefs, err := ListNotificationPrefs(ctx, pool, userID)
	if err != nil {
		t.Fatalf("ListNotificationPrefs: %v", err)
	}
	for _, p := range prefs {
		if p.Event == NotifyMention {
			if !p.InApp || p.Email {
				t.Fatalf("mention pref defaults = in_app %v email %v, want true/false", p.InApp, p.Email)
			}
			return
		}
	}
	t.Fatal("mention event missing from ListNotificationPrefs")
}

// TestCreateCommentMentionedWatcherSinglePing: a watcher who is also
// mentioned gets exactly one notification — the `mention` — not both
// comment.created and mention.
func TestCreateCommentMentionedWatcherSinglePing(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, watcherID, _, wsSlug, ident, issueID := createMentionFixture(t, pool)

	_, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Watcher Wulan, your call"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got := notificationTypes(t, pool, watcherID); !reflect.DeepEqual(got, []string{NotifyMention}) {
		t.Fatalf("watcher notifications = %v, want [mention] only", got)
	}
}

// TestCreateCommentNoMentionNoExtraNoise: a comment without @-tokens only
// produces comment.created for the watcher — no mention rows.
func TestCreateCommentNoMentionNoExtraNoise(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, watcherID, _, wsSlug, ident, issueID := createMentionFixture(t, pool)

	_, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("plain comment, no pings"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if got := notificationTypes(t, pool, watcherID); !reflect.DeepEqual(got, []string{NotifyCommentCreated}) {
		t.Fatalf("watcher notifications = %v, want [comment.created]", got)
	}
}

// TestUpdateCommentMentionNotifies: editing a comment to add a mention
// notifies the mentioned member.
func TestUpdateCommentMentionNotifies(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	c, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("first draft"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if _, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, actorID,
		tiptapComment("edited: hey @Mentee Galih take a look")); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if got := notificationTypes(t, pool, mentioneeID); !reflect.DeepEqual(got, []string{NotifyMention}) {
		t.Fatalf("mentionee notifications = %v, want [mention]", got)
	}
}

// TestUpdateCommentSelfMentionSkipped: mentioning yourself in an edit
// produces no notification.
func TestUpdateCommentSelfMentionSkipped(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, _, wsSlug, ident, issueID := createMentionFixture(t, pool)

	c, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("first draft"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	if _, err := UpdateComment(ctx, pool, wsSlug, ident, issueID, c.ID, actorID,
		tiptapComment("note to self @Actor Andy")); err != nil {
		t.Fatalf("UpdateComment: %v", err)
	}
	if n := countNotifications(t, pool, actorID); n != 0 {
		t.Fatalf("actor notifications = %d, want 0", n)
	}
}

// TestCreateCommentMentionNonMemberSkipped: a name that matches nobody in
// the workspace creates no notification and does not fail the comment.
func TestCreateCommentMentionNonMemberSkipped(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, watcherID, mentioneeID, wsSlug, ident, issueID := createMentionFixture(t, pool)

	_, err := CreateComment(ctx, pool, wsSlug, ident, issueID, actorID,
		tiptapComment("hey @Nobody Here, thoughts?"), nil)
	if err != nil {
		t.Fatalf("CreateComment: %v", err)
	}
	// Only the subscribed watcher gets comment.created; nobody gets a
	// mention row.
	if got := notificationTypes(t, pool, watcherID); !reflect.DeepEqual(got, []string{NotifyCommentCreated}) {
		t.Fatalf("watcher notifications = %v", got)
	}
	if n := countNotifications(t, pool, mentioneeID); n != 0 {
		t.Fatalf("mentionee notifications = %d, want 0", n)
	}
}
