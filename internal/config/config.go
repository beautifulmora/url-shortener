package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr          string
	BaseURL           string
	DatabaseURL       string
	RedisAddr         string
	RedisPassword     string
	JWTSecret         string
	BatchSize         int
	BatchInterval     time.Duration
	RateLimitRequests int
	RateLimitWindow   time.Duration
	TrustedProxies    []string
}

func Load() *Config {
	return &Config{
		HTTPAddr:          getEnv("HTTP_ADDR", ":8080"),
		BaseURL:           getEnv("BASE_URL", "http://localhost:8080"),
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://postgres:password@localhost:5432/shortener?sslmode=disable"),
		RedisAddr:         getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:     getEnv("REDIS_PASSWORD", ""),
		JWTSecret:         getEnv("JWT_SECRET", "super-secret-key"),
		BatchSize:         getEnvAsInt("BATCH_SIZE", 100),
		BatchInterval:     getEnvAsDuration("BATCH_INTERVAL", 5*time.Second),
		RateLimitRequests: getEnvAsInt("RATE_LIMIT_REQUESTS", 60),
		RateLimitWindow:   getEnvAsDuration("RATE_LIMIT_WINDOW", time.Minute),
		TrustedProxies:    getEnvAsSlice("TRUSTED_PROXIES"),
	}
}

func getEnv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvAsDuration(key string, fallback time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok {
		if parsed, err := time.ParseDuration(val); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvAsSlice(key string) []string {
	val, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(val) == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	res := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}
