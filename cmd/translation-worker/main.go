package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/alimohamed/hadal/internal/translation"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	maxAttempts := positiveInt(logger, "PROVIDER_MAX_ATTEMPTS", 3)
	staleAfter := time.Duration(positiveInt(logger, "TRANSLATION_PROCESSING_STALE_AFTER_SECONDS", 300)) * time.Second
	pool, err := pgxpool.New(ctx, required("DATABASE_URL"))
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}

	provider := translation.NewOpenAIProvider(
		&http.Client{Timeout: 60 * time.Second},
		"",
		required("OPENAI_API_KEY"),
		value("OPENAI_TRANSLATION_MODEL", "gpt-4o-mini"),
	)
	runner := translation.NewRunner(translation.NewPostgresRepository(pool, 100), provider, maxAttempts, staleAfter)
	logger.Info("translation worker started")
	for ctx.Err() == nil {
		if err := runner.Run(ctx, time.Second); err != nil && ctx.Err() == nil {
			logger.Error("translation runner stopped", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
}

func positiveInt(logger *slog.Logger, key string, fallback int) int {
	parsed, err := strconv.Atoi(value(key, strconv.Itoa(fallback)))
	if err != nil || parsed < 1 {
		logger.Error("invalid positive integer environment variable", "variable", key)
		os.Exit(1)
	}
	return parsed
}

func required(key string) string {
	if configured := os.Getenv(key); configured != "" {
		return configured
	}
	slog.Error("required environment variable is unset", "variable", key)
	os.Exit(1)
	return ""
}

func value(key, fallback string) string {
	if configured := os.Getenv(key); configured != "" {
		return configured
	}
	return fallback
}
