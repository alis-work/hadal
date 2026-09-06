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

	"github.com/alimohamed/hadal/internal/access"
	"github.com/alimohamed/hadal/internal/httpapi"
	"github.com/alimohamed/hadal/internal/transcription"
	"github.com/alimohamed/hadal/internal/translation"
	"github.com/alimohamed/hadal/internal/whatsapp"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databaseURL, redisURL := required("DATABASE_URL"), required("REDIS_URL")
	verifyToken := required("WHATSAPP_VERIFY_TOKEN")
	appSecret := required("WHATSAPP_APP_SECRET")
	accessToken := required("WHATSAPP_ACCESS_TOKEN")
	phoneNumberID := required("WHATSAPP_PHONE_NUMBER_ID")
	graphVersion := value("WHATSAPP_GRAPH_API_VERSION", "v26.0")
	directory := value("AUDIO_TEMP_DIR", "./data/audio")
	address := value("API_ADDR", ":8080")
	maxMB, err := strconv.ParseInt(value("MAX_AUDIO_UPLOAD_MB", "50"), 10, 64)
	if err != nil || maxMB < 1 {
		logger.Error("invalid MAX_AUDIO_UPLOAD_MB")
		os.Exit(1)
	}
	globalDailyLimit, err := strconv.Atoi(value("GLOBAL_DAILY_AUDIO_LIMIT", "100"))
	if err != nil || globalDailyLimit < 1 {
		logger.Error("invalid GLOBAL_DAILY_AUDIO_LIMIT")
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
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
	if err := client.Ping(ctx).Err(); err != nil {
		logger.Error("redis connection failed", "error", err)
		os.Exit(1)
	}
	senders := access.NewPostgresRepository(pool, os.Getenv("WHATSAPP_TESTER_PHONE"))
	transcriptionService := transcription.NewService(transcription.NewPostgresRepository(pool, globalDailyLimit), transcription.NewRedisQueue(client), transcription.FileStorage{Directory: directory}, transcription.FFProbe{}, senders)
	translationService := translation.NewService(translation.NewPostgresRepository(pool, globalDailyLimit))
	codes := access.RegistrationCodesFromEnvironment()
	limiter := transcription.NewRedisRateLimiter(client)
	handler := httpapi.NewHandler(transcriptionService, logger, maxMB*1024*1024, senders, codes, limiter)
	whatsappRepository := whatsapp.NewPostgresRepository(pool)
	graphClient := whatsapp.NewGraphClient(&http.Client{Timeout: 30 * time.Second}, "", graphVersion, accessToken)
	processor := whatsapp.NewProcessor(whatsappRepository, graphClient, senders, limiter, transcriptionService, translationService, maxMB*1024*1024, 5)
	dispatcher := whatsapp.NewDispatcher(whatsappRepository, graphClient, phoneNumberID, 5)
	webhook := httpapi.NewWebhook(logger, verifyToken, httpapi.WithWebhookAppSecret(appSecret), httpapi.WithWebhookInboundStore(whatsappRepository), httpapi.WithWebhookRegistrationCodes(codes))
	mux := http.NewServeMux()
	mux.Handle("/", handler.Routes())
	mux.Handle("/webhook", webhook.Routes())
	server := &http.Server{Addr: address, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go runWhatsAppRunner(ctx, logger, "intake", processor.Runner(time.Second))
	go runWhatsAppRunner(ctx, logger, "outbound", dispatcher.Runner(time.Second))
	logger.Info("api listening", "address", address)
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.ListenAndServe() }()
	select {
	case err := <-serverErrors:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("api stopped", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			logger.Error("api shutdown failed", "error", err)
		}
	}
}

func runWhatsAppRunner(ctx context.Context, logger *slog.Logger, name string, runner whatsapp.Runner) {
	for ctx.Err() == nil {
		if err := runner.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("whatsapp runner stopped", "runner", name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
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
