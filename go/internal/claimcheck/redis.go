// Redis backend (profile claimcheck-redis). Objects are written with SET ... EX <CLAIMCHECK_TTL>, so Redis
// itself expires staged data. The server runs with AOF persistence and maxmemory-policy noeviction (see
// docker-compose.yml): claim-check data must survive a restart and must never be evicted mid-draft.
// This is the ONLY package (besides the DEMO-ONLY expire action in internal/control) that imports Redis.
package claimcheck

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Store backed by Redis strings.
type Redis struct {
	client *redis.Client
	ttl    time.Duration
}

// NewRedis connects lazily to addr; Ping verifies the connection.
func NewRedis(addr string, ttl time.Duration) *Redis {
	return &Redis{client: redis.NewClient(&redis.Options{Addr: addr}), ttl: ttl}
}

func (r *Redis) Name() string                   { return "redis" }
func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *Redis) Put(ctx context.Context, key string, data []byte, _ string) error {
	return r.client.Set(ctx, key, data, r.ttl).Err()
}

func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := r.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	return data, err
}
