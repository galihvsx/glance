package auth

// Session cookie attributes, shared by every login path (OTP verify,
// OAuth callback) and by logout/session revocation. One place, so the
// cookie can never drift apart between login and logout.
//
// Spec §7: HttpOnly, Secure, SameSite=Lax; 30-day expiry matching the
// session TTL.
const (
	// SessionCookieName is the name of the session cookie.
	SessionCookieName = "glance_session"
	// SessionCookieMaxAge is the cookie lifetime in seconds (30 days).
	SessionCookieMaxAge = 30 * 24 * 3600 // 2592000
)
