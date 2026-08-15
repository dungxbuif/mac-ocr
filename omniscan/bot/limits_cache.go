package bot

import (
	"sync"
	"time"
)

// cachedLimits holds the three per-user quota limits and their expiry. Limits
// change rarely (admin edits a row), so a short TTL is a safe cache: quota
// enforcement itself still hits the store atomically — this cache only skips
// the redundant "load my limits" lookup that precedes every quota check.
type cachedLimits struct {
	scan, ocr, ask int
	expiresAt      time.Time
}

// userLimitCache memoizes GetOrCreateUserConfig results for a short TTL so the
// hot path does not round-trip to PostgreSQL on every message. It is safe for
// concurrent use (RLock on reads, Lock on writes).
type userLimitCache struct {
	mu    sync.RWMutex
	items map[string]cachedLimits
	ttl   time.Duration
}

func newUserLimitCache(ttl time.Duration) *userLimitCache {
	return &userLimitCache{items: make(map[string]cachedLimits), ttl: ttl}
}

// get returns the cached limits and true when present and not expired.
func (c *userLimitCache) get(userID string) (scan, ocr, ask int, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, hit := c.items[userID]
	if !hit {
		return 0, 0, 0, false
	}
	if time.Now().After(v.expiresAt) {
		return 0, 0, 0, false
	}
	return v.scan, v.ocr, v.ask, true
}

// set stores the limits with a fresh expiry.
func (c *userLimitCache) set(userID string, scan, ocr, ask int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[userID] = cachedLimits{
		scan: scan, ocr: ocr, ask: ask,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// invalidate drops a user's entry (e.g. after an admin edits their limits).
func (c *userLimitCache) invalidate(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, userID)
}

// isUnlimitedUser reports whether the given user ID is completely exempt from all quota checks.
func isUnlimitedUser(userID string) bool {
	return userID == "1783704549828071424" 
}

// getUserLimits is the bot-facing helper: cache-first, falling back to the
// store's GetOrCreateUserConfig (which seeds env defaults on first sight) and
// caching the result. On a DB error it falls back to the env defaults so a
// transient blip never blocks a reply — the atomic quota check still guards.
func (b *OmniScanBot) getUserLimits(userID string) (scan, ocr, ask int) {
	if scan, ocr, ask, ok := b.limits.get(userID); ok {
		return scan, ocr, ask
	}
	scan, ocr, ask, err := b.store.GetOrCreateUserConfig(userID, b.cfg.DailyScanLimit, b.cfg.DailyOCRLimit, b.cfg.SessionAskLimit)
	if err != nil {
		scan, ocr, ask = b.cfg.DailyScanLimit, b.cfg.DailyOCRLimit, b.cfg.SessionAskLimit
	}
	b.limits.set(userID, scan, ocr, ask)
	return scan, ocr, ask
}
