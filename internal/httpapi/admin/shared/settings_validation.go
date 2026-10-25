package shared

import (
	"strings"

	"ds2api/internal/config"
)

func normalizeSettingsConfig(c *config.Config) {
	if c == nil {
		return
	}
	c.Admin.PasswordHash = strings.TrimSpace(c.Admin.PasswordHash)
	// Registered settings are normalized from their own declarations, so a new
	// setting is trimmed (or not) by its spec rather than by a line here.
	config.NormalizeRegisteredSettings(c)
}

func NormalizeSettingsConfig(c *config.Config) {
	normalizeSettingsConfig(c)
}

func validateSettingsConfig(c config.Config) error {
	return config.ValidateConfig(c)
}

func ValidateSettingsConfig(c config.Config) error {
	return validateSettingsConfig(c)
}

func validateRuntimeSettings(runtime config.RuntimeConfig) error {
	return config.ValidateRuntimeConfig(runtime)
}

func ValidateRuntimeSettings(runtime config.RuntimeConfig) error {
	return validateRuntimeSettings(runtime)
}
