package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/user/url-shortener/internal/config"
	"github.com/user/url-shortener/internal/repository/postgres"
	"github.com/user/url-shortener/internal/repository/redis"
	"github.com/user/url-shortener/internal/service"
	httptransport "github.com/user/url-shortener/internal/transport/http"
	"github.com/user/url-shortener/internal/worker"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := postgres.RunMigrations(cfg.DatabaseURL, "migrations"); err != nil {
		slog.Error("migrations failed", "err", err)
		os.Exit(1)
	}

	pgPool, err := postgres.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("postgres connection failed", "err", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	redisCache, err := redis.NewCache(cfg.RedisAddr, cfg.RedisPassword)
	if err != nil {
		slog.Error("redis connection failed", "err", err)
		os.Exit(1)
	}
	defer redisCache.Close()

	userRepo := postgres.NewUserRepo(pgPool)
	linkRepo := postgres.NewLinkRepo(pgPool)

	authSvc := service.NewAuthService(userRepo, cfg.JWTSecret)
	shortenerSvc := service.NewShortenerService(linkRepo, redisCache, cfg.BaseURL)

	batcher := worker.NewClickBatcher(linkRepo, cfg.BatchSize, cfg.BatchInterval)
	batcher.Start(ctx)

	handler := httptransport.NewHandler(
		authSvc,
		shortenerSvc,
		batcher,
		redisCache,
		cfg.RateLimitRequests,
		cfg.RateLimitWindow,
		cfg.TrustedProxies,
	)

	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      handler.Router(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("starting http server", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown error", "err", err)
	}

	batcher.Wait()
	slog.Info("server stopped", "dropped_events_total", batcher.DroppedEventsTotal())
}
