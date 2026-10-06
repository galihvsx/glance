package service

// Task 28 (T26): SSRF guard tests for the webhook delivery path.

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWebhookTargetBlocked pins the blocklist: private, loopback,
// link-local (cloud metadata), multicast, and unspecified addresses must
// never receive webhook deliveries; public addresses pass.
func TestWebhookTargetBlocked(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"127.0.0.1", true},
		{"10.0.0.1", true},
		{"172.16.5.4", true},
		{"192.168.1.1", true},
		{"100.64.0.1", true},            // carrier-grade NAT
		{"169.254.169.254", true},       // cloud metadata endpoint
		{"0.0.0.0", true},               // unspecified
		{"::1", true},                   // v6 loopback
		{"fe80::1", true},               // v6 link-local
		{"fc00::1", true},               // v6 unique-local
		{"ff02::1", true},               // v6 multicast
		{"224.0.0.1", true},             // v4 multicast
		{"8.8.8.8", false},              // public v4
		{"93.184.216.34", false},        // public v4 (documentation)
		{"2606:4700:4700::1111", false}, // public v6
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("ParseIP(%q) = nil", tc.ip)
		}
		if got := webhookTargetBlocked(ip); got != tc.blocked {
			t.Errorf("webhookTargetBlocked(%s) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

// stubResolver returns canned DNS answers for guardTarget tests.
func stubResolver(t *testing.T, answers map[string][]net.IP) func(context.Context, string) ([]net.IP, error) {
	t.Helper()
	return func(_ context.Context, host string) ([]net.IP, error) {
		if host == "dnsfail.example" {
			return nil, errors.New("no such host")
		}
		ips, ok := answers[host]
		if !ok {
			t.Fatalf("unexpected DNS lookup for %q", host)
		}
		return ips, nil
	}
}

func testGuardDispatcher(t *testing.T) *WebhookDispatcher {
	t.Helper()
	d := NewWebhookDispatcher(nil)
	d.lookupIP = stubResolver(t, map[string][]net.IP{
		"public.example":  {net.ParseIP("93.184.216.34")},
		"private.example": {net.ParseIP("10.1.2.3")},
		"mixed.example":   {net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.1")},
		"v6link.example":  {net.ParseIP("fe80::1")},
	})
	return d
}

// TestGuardTarget pins delivery-time DNS resolution + rejection: literal
// private IPs, hostnames resolving to private IPs, mixed answers (fail
// closed), and IPv6 link-local are blocked; public targets pass with
// their validated IPs; DNS failures are errors (transient, retried).
func TestGuardTarget(t *testing.T) {
	ctx := context.Background()
	d := testGuardDispatcher(t)

	blocked := []string{
		"http://127.0.0.1:8080/hook",   // literal loopback
		"http://10.0.0.5/hook",         // literal private
		"http://169.254.169.254/",      // literal metadata endpoint
		"http://[::1]/hook",            // literal v6 loopback
		"http://[fc00::1]/hook",        // literal v6 ULA
		"https://private.example/hook", // hostname -> private
		"https://mixed.example/hook",   // mixed public+private: fail closed
		"https://v6link.example/hook",  // hostname -> link-local
		"not a url",                    // unparseable
		"",                             // empty
	}
	for _, raw := range blocked {
		_, err := d.guardTarget(ctx, raw)
		if !errors.Is(err, errWebhookTargetBlocked) {
			t.Errorf("guardTarget(%q): err = %v, want errWebhookTargetBlocked", raw, err)
		}
	}

	ips, err := d.guardTarget(ctx, "https://public.example/hook")
	if err != nil {
		t.Fatalf("guardTarget(public): unexpected err: %v", err)
	}
	if len(ips) != 1 || ips[0].String() != "93.184.216.34" {
		t.Errorf("guardTarget(public) = %v, want [93.184.216.34]", ips)
	}

	// DNS failure is an error but NOT the blocked-target error: the
	// caller retries it with backoff instead of failing permanently.
	if _, err := d.guardTarget(ctx, "https://dnsfail.example/hook"); err == nil || errors.Is(err, errWebhookTargetBlocked) {
		t.Errorf("guardTarget(dnsfail): err = %v, want transient DNS error", err)
	}
}

// TestDeliverRowBlocksPrivateTarget is the end-to-end SSRF pin: a webhook
// whose target resolves to loopback is failed permanently and never
// dialed. The httptest server must see ZERO requests.
func TestDeliverRowBlocksPrivateTarget(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	var dials int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// The webhook points at a live local server — without the guard this
	// WOULD be dialed. The stub resolver reports its host as loopback, so
	// the guard must reject before any dial and the server must see zero
	// requests.
	outboxID := enqueueOneWebhookRow(t, pool, ctx, wsSlug, actorID, srv.URL)

	d := NewWebhookDispatcher(pool)
	d.lookupIP = func(_ context.Context, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	}
	if err := d.Run(ctx); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var status, lastErr string
	if err := pool.QueryRow(ctx,
		`SELECT status, COALESCE(last_error, '') FROM outbox WHERE id = $1`, outboxID,
	).Scan(&status, &lastErr); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want failed (blocked target must not retry)", status)
	}
	if !strings.Contains(lastErr, "non-public IP") {
		t.Errorf("last_error = %q, want SSRF guard mention", lastErr)
	}
	if dials != 0 {
		t.Errorf("httptest server saw %d requests, want 0 (blocked target must never be dialed)", dials)
	}
}

// TestDeliverRowDNSFailureRetries pins the transient path: an unresolvable
// target goes back to pending with backoff (attempts bumped), not failed.
func TestDeliverRowDNSFailureRetries(t *testing.T) {
	pool := newTestPool(t)
	migrateTestDB(t, pool)
	ctx := context.Background()
	actorID, _, wsSlug, _, _ := createNotifyFixture(t, pool)

	outboxID := enqueueOneWebhookRow(t, pool, ctx, wsSlug, actorID, "https://dnsfail.example/hook")

	d := NewWebhookDispatcher(pool)
	d.lookupIP = stubResolver(t, map[string][]net.IP{})
	if err := d.Run(ctx); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	var status string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts FROM outbox WHERE id = $1`, outboxID,
	).Scan(&status, &attempts); err != nil {
		t.Fatalf("read outbox: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending (DNS failure is transient)", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
}

// TestDoPinnedDelivers exercises the pinned-dial path end-to-end: the
// request reaches the server when dialing the validated IPs directly,
// proving the guard's happy path can actually deliver.
func TestDoPinnedDelivers(t *testing.T) {
	ctx := context.Background()
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	d := NewWebhookDispatcher(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL, strings.NewReader(`{"ping":1}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := d.doPinned(ctx, req, []net.IP{net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("doPinned: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if string(gotBody) != `{"ping":1}` {
		t.Errorf("body = %q, want ping payload", gotBody)
	}
}
