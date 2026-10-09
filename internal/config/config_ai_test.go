package config

import "testing"

// TestLoadAIDefaults pins the C5T2 fail-open rule: AI assist is optional
// at boot. Without GLANCE_AI_* env vars, Load succeeds (non-fatal) and
// AI reports unconfigured; the endpoints answer 503 instead of failing
// the whole server. BaseURL/Model fall back to OpenAI-compatible
// defaults so a custom provider needs only its base URL set.
func TestLoadAIDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
	t.Setenv("GLANCE_AI_BASE_URL", "")
	t.Setenv("GLANCE_AI_MODEL", "")
	t.Setenv("GLANCE_AI_API_KEY", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.AI.Configured() {
		t.Error("AI.Configured() = true without GLANCE_AI_API_KEY, want false")
	}
	if cfg.AI.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("AI.BaseURL = %q, want default https://api.openai.com/v1", cfg.AI.BaseURL)
	}
	if cfg.AI.Model != "gpt-4o-mini" {
		t.Errorf("AI.Model = %q, want default gpt-4o-mini", cfg.AI.Model)
	}
}

// TestLoadAIFromEnv pins that the three GLANCE_AI_* vars land on the
// config verbatim (the key must be stored exactly as given — it is sent
// as the Bearer <redacted> header).
func TestLoadAIFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/glance?sslmode=disable")
	t.Setenv("OTP_PEPPER", "test-pepper")
	t.Setenv("ALLOW_INSECURE_OTP_PEPPER", "")
	t.Setenv("GLANCE_AI_BASE_URL", "https://llm.example.com/v1")
	t.Setenv("GLANCE_AI_MODEL", "some-compatible-model")
	t.Setenv("GLANCE_AI_API_KEY", "sk-test-key-do-not-use")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if !cfg.AI.Configured() {
		t.Error("AI.Configured() = false with GLANCE_AI_API_KEY set, want true")
	}
	if cfg.AI.BaseURL != "https://llm.example.com/v1" {
		t.Errorf("AI.BaseURL = %q, want https://llm.example.com/v1", cfg.AI.BaseURL)
	}
	if cfg.AI.Model != "some-compatible-model" {
		t.Errorf("AI.Model = %q, want some-compatible-model", cfg.AI.Model)
	}
	if cfg.AI.APIKey != "sk-test-key-do-not-use" {
		t.Error("AI.APIKey was not stored verbatim")
	}
}
