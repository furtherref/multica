package auth

import (
	"context"
	"errors"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

// accountCachePrefix namespaces account-status cache keys away from the
// realtime relay (ws:*), local-skill (mul:local_skill:*), and PAT
// (mul:auth:pat:*) keys.
const accountCachePrefix = "mul:auth:acct:"

// AccountStatusCache caches resolved account_status lookups in Redis. A nil
// *AccountStatusCache is safe to use — every method becomes a no-op or
// reports a cache miss, and AccountGuard degrades to direct DB lookups.
type AccountStatusCache struct {
	rdb *redis.Client
}

// NewAccountStatusCache returns a cache backed by rdb. Pass nil to disable
// caching; the returned *AccountStatusCache is safe to call but never hits
// Redis.
func NewAccountStatusCache(rdb *redis.Client) *AccountStatusCache {
	if rdb == nil {
		return nil
	}
	return &AccountStatusCache{rdb: rdb}
}

func accountCacheKey(userID string) string { return accountCachePrefix + userID }

// Get returns the cached account_status for a user id. ok=false on cache
// miss or any Redis error — a dead Redis must not take down auth.
func (c *AccountStatusCache) Get(ctx context.Context, userID string) (status string, ok bool) {
	if c == nil {
		return "", false
	}
	v, err := c.rdb.Get(ctx, accountCacheKey(userID)).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("account_cache: get failed; falling back to DB", "error", err)
		}
		return "", false
	}
	return v, true
}

// Set populates the cache with the fixed AuthCacheTTL. Unlike PATCache,
// account statuses don't have their own expiry to bound the TTL against, so
// every entry uses AuthCacheTTL.
//
// Errors are logged and swallowed — a cache write failure is not a request
// failure.
func (c *AccountStatusCache) Set(ctx context.Context, userID, status string) {
	if c == nil {
		return
	}
	if err := c.rdb.Set(ctx, accountCacheKey(userID), status, AuthCacheTTL).Err(); err != nil {
		slog.Warn("account_cache: set failed", "error", err)
	}
}

// Invalidate removes the entry for userID. Called on suspend/unsuspend so
// the change takes effect immediately rather than waiting for the TTL —
// including on the PAT-cache-hit path.
func (c *AccountStatusCache) Invalidate(ctx context.Context, userID string) {
	if c == nil {
		return
	}
	if err := c.rdb.Del(ctx, accountCacheKey(userID)).Err(); err != nil {
		slog.Warn("account_cache: invalidate failed; entry will expire on TTL", "error", err)
	}
}
