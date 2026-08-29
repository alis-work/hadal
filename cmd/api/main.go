package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/alimohamed/hadal/internal/httpapi"
	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	databaseURL, redisURL := required("DATABASE_URL"), required("REDIS_URL")
	directory := value("AUDIO_TEMP_DIR", "./data/audio")
	address := value("API_ADDR", ":8080")
	maxMB, err := strconv.ParseInt(value("MAX_AUDIO_UPLOAD_MB", "50"), 10, 64)
	if err != nil || maxMB < 1 {
		logger.Error("invalid MAX_AUDIO_UPLOAD_MB")
		os.Exit(1)
	}
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	redisOptions, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "error", err)
		os.Exit(1)
	}
	client := redis.NewClient(redisOptions)
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		logger.Error("redis connection failed", "error", err)
		os.Exit(1)
	}
	service := transcription.NewService(transcription.NewPostgresRepository(pool), transcription.NewRedisQueue(client), transcription.FileStorage{Directory: directory})
	handler := httpapi.NewHandler(service, logger, maxMB*1024*1024)
	server := &http.Server{Addr: address, Handler: handler.Routes(), ReadHeaderTimeout: 5 * time.Second}
	logger.Info("api listening", "address", address)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("api stopped", "error", err)
		os.Exit(1)
	}
}
func required(key string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	slog.Error("required environment variable is unset", "variable", key)
	os.Exit(1)
	return ""
}
func value(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
