package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
)

const toolMarkerSecretEnv = "DS2API_TOOL_MARKER_SECRET"

// ToolMarkerSecret returns the server-side secret used to derive per-API-key
// tool-call markers. The DS2API_TOOL_MARKER_SECRET environment variable takes
// precedence over the persisted config value so ephemeral/read-only deployments
// can pin a stable secret.
func (s *Store) ToolMarkerSecret() string {
	if s == nil {
		return strings.TrimSpace(os.Getenv(toolMarkerSecretEnv))
	}
	if v := strings.TrimSpace(os.Getenv(toolMarkerSecretEnv)); v != "" {
		return v
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return strings.TrimSpace(s.cfg.ToolMarkerSecret)
}

// EnsureToolMarkerSecret returns the configured marker secret, generating and
// persisting a random one the first time it is needed. Persistence is
// best-effort: when the config is read-only (env-backed or Vercel) the secret
// simply lives in memory for the process lifetime, so operators should set
// DS2API_TOOL_MARKER_SECRET in those deployments to keep markers stable across
// cold starts.
func (s *Store) EnsureToolMarkerSecret() string {
	if v := strings.TrimSpace(os.Getenv(toolMarkerSecretEnv)); v != "" {
		return v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := strings.TrimSpace(s.cfg.ToolMarkerSecret); existing != "" {
		return existing
	}
	generated := generateToolMarkerSecret()
	s.cfg.ToolMarkerSecret = generated
	s.cfgDirty = true
	if err := s.saveLocked(); err != nil {
		Logger.Warn("[config] persist tool_marker_secret failed", "error", err)
	}
	return generated
}

func generateToolMarkerSecret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	// crypto/rand failure is effectively impossible; fall back to a value that
	// is at least not a compile-time constant.
	return "fallback-" + strings.TrimSpace(os.Getenv("DS2API_ADMIN_KEY"))
}
