package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/user/url-shortener/internal/model"
)

type Cache struct {
	client *redis.Client
}

func NewCache(addr, password string) (*Cache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       0,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}

	return &Cache{client: client}, nil
}

func (c *Cache) Client() *redis.Client {
	return c.client
}

func (c *Cache) Close() error {
	return c.client.Close()
}

func (c *Cache) SetLink(ctx context.Context, code string, originalURL string, ttl time.Duration) error {
	return c.client.Set(ctx, "link:"+code, originalURL, ttl).Err()
}

func (c *Cache) GetLink(ctx context.Context, code string) (string, error) {
	val, err := c.client.Get(ctx, "link:"+code).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return "", model.ErrNotFound
		}
		return "", err
	}
	return val, nil
}

func (c *Cache) DeleteLink(ctx context.Context, code string) error {
	return c.client.Del(ctx, "link:"+code).Err()
}

func (c *Cache) AllowRequest(ctx context.Context, ip string, limit int, window time.Duration) (bool, error) {
	key := "rl:" + ip
	pipe := c.client.Pipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}
	return incr.Val() <= int64(limit), nil
}
