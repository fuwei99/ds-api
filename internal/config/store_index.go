package config

import "strings"

// rebuildIndexes must be called with the lock already held (or during init).
func (s *Store) rebuildIndexes() {
	prevStatus := s.accTest
	s.keyMap = make(map[string]struct{}, len(s.cfg.Keys))
	for _, k := range s.cfg.Keys {
		s.keyMap[k] = struct{}{}
	}
	s.keyFlags = make(map[string]map[string]bool, len(s.cfg.APIKeys))
	for _, item := range s.cfg.APIKeys {
		key := strings.TrimSpace(item.Key)
		if key == "" {
			continue
		}
		// Only non-default values are indexed, so a missing entry means "reads
		// as the declared default" for every registered flag.
		flags := make(map[string]bool, len(item.flags))
		for _, spec := range CredentialFlagSpecs() {
			if value, ok := item.flags[spec.Name]; ok && value != spec.Default {
				flags[spec.Name] = value
			}
		}
		if len(flags) > 0 {
			s.keyFlags[key] = flags
		}
	}
	s.accMap = make(map[string]int, len(s.cfg.Accounts))
	s.accTest = make(map[string]string, len(s.cfg.Accounts))
	for i, acc := range s.cfg.Accounts {
		id := acc.Identifier()
		if id != "" {
			s.accMap[id] = i
			if status, ok := prevStatus[id]; ok {
				s.setAccountTestStatusLocked(acc, status, "")
			}
		}
	}
}

// findAccountIndexLocked expects the store lock to already be held.
func (s *Store) findAccountIndexLocked(identifier string) (int, bool) {
	if idx, ok := s.accMap[identifier]; ok && idx >= 0 && idx < len(s.cfg.Accounts) {
		return idx, true
	}
	// Fallback for token-only accounts whose derived identifier changed after
	// a token refresh; this preserves correctness on map misses.
	for i, acc := range s.cfg.Accounts {
		if acc.Identifier() == identifier {
			return i, true
		}
	}
	return -1, false
}

func (s *Store) setAccountTestStatusLocked(acc Account, status, hintedIdentifier string) {
	status = lower(status)
	if status == "" {
		return
	}
	if id := acc.Identifier(); id != "" {
		s.accTest[id] = status
	}
	if email := acc.Email; email != "" {
		s.accTest[email] = status
	}
	if mobile := CanonicalMobileKey(acc.Mobile); mobile != "" {
		s.accTest[mobile] = status
	}
	if hintedIdentifier = lower(hintedIdentifier); hintedIdentifier != "" {
		s.accTest[hintedIdentifier] = status
	}
}
