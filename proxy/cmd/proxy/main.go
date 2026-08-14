package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"macocr/proxy/internal/config"
	"macocr/proxy/internal/native"
	"macocr/proxy/internal/notifications"
	pgrepo "macocr/proxy/internal/repository/postgres"
	redisrepo "macocr/proxy/internal/repository/redis"
	s3repo "macocr/proxy/internal/repository/s3"
	"macocr/proxy/internal/rest"
	"macocr/proxy/internal/retention"
	"macocr/proxy/internal/scheduler"
	"macocr/proxy/internal/usecase/auth"
	"macocr/proxy/internal/usecase/document"
	"macocr/proxy/internal/usecase/object"
	"macocr/proxy/internal/usecase/system"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	objRepo, err := s3repo.New(s3repo.Config{
		Endpoint:        cfg.S3Endpoint,
		Region:          cfg.S3Region,
		Bucket:          cfg.S3Bucket,
		AccessKeyID:     cfg.S3AccessKeyID,
		SecretAccessKey: cfg.S3SecretAccessKey,
		ForcePathStyle:  cfg.S3ForcePathStyle,
	}, logger)
	if err != nil {
		logger.Error("failed to init s3 repository", "error", err)
		os.Exit(1)
	}

	objSvc := object.NewService(objRepo)

	redisRepo, err := redisrepo.New(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to init redis repository", "error", err)
		os.Exit(1)
	}
	defer redisRepo.Close()

	pgRepo, err := pgrepo.New(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to init postgres repository", "error", err)
		os.Exit(1)
	}
	defer pgRepo.Close()

	if err := pgRepo.Migrate(ctx); err != nil {
		logger.Error("failed to migrate postgres schema", "error", err)
		os.Exit(1)
	}

	systemSvc := system.NewService(objSvc, redisRepo, pgRepo)

	userRepo := pgrepo.NewUserRepository(pgRepo.Pool())
	configRepo := pgrepo.NewAccountConfigRepository(pgRepo.Pool())
	apiKeyRepo := pgrepo.NewAPIKeyRepository(pgRepo.Pool())
	authSvc := auth.NewService(userRepo, configRepo, apiKeyRepo, redisRepo)

	secretCipher, err := notifications.NewSecretCipher(cfg.NotificationEncryptionKey)
	if err != nil {
		logger.Error("failed to initialize notification encryption", "error", err)
		os.Exit(1)
	}
	docRepo := pgrepo.NewDocumentRepository(pgRepo.Pool(), secretCipher)
	notificationRepo := pgrepo.NewNotificationRepository(pgRepo.Pool(), secretCipher)
	notificationSvc := notifications.NewService(notificationRepo, cfg.PublicAPIBaseURL, logger)
	docSvc := document.NewServiceWithMaxUploadBytes(docRepo, objRepo, authSvc, notificationSvc, redisRepo, cfg.MaxUploadBytes)
	go notificationSvc.Run(ctx)
	go retention.New(docRepo, notificationRepo, objRepo, logger, cfg.InputTTL, cfg.UploadTTL, cfg.DocumentTTL, cfg.NotificationTTL, configRepo).Run(ctx)

	nativeClient := native.NewClient(cfg.NativeBaseURL, cfg.NativeAuthSecret)
	callbackURL := fmt.Sprintf("%s/webhooks/native/events", cfg.PublicAPIBaseURL)

	sched := scheduler.New(docRepo, objRepo, nativeClient, authSvc, callbackURL, logger, cfg.ProcessingLease, cfg.ProcessingMaxAttempts, notificationSvc)
	go sched.Run(ctx)

	sessionMgr := rest.NewSessionManager()
	adminAuthHandler := rest.NewAdminAuthHandler(userRepo, docSvc, sessionMgr, cfg.Env == "production")

	healthHandler := rest.NewHealthHandler(systemSvc)
	authHandler := rest.NewAuthHandler(authSvc)
	docHandler := rest.NewDocumentHandler(docSvc, cfg.PublicAPIBaseURL, cfg.PublicDocsBaseURL)
	batchHandler := rest.NewBatchHandler(docSvc, cfg.PublicAPIBaseURL, cfg.PublicDocsBaseURL)
	uploadHandler := rest.NewUploadHandlerWithReservationTTL(objRepo, cfg.MaxUploadBytes, configRepo, cfg.UploadTTL, authSvc)
	capHandler := rest.NewCapabilitiesHandler()
	webhookHandler := rest.NewWebhookHandler(docRepo, objRepo, cfg.NativeAuthSecret, sched, logger, notificationSvc, redisRepo, cfg.ResultTTL)
	notificationHandler := rest.NewNotificationHandler(notificationSvc)
	mcpHandler := rest.NewMCPHandler(docSvc, notificationSvc, cfg.PublicAPIBaseURL)

	router := rest.NewRouter(
		logger,
		healthHandler,
		authHandler,
		docHandler,
		batchHandler,
		uploadHandler,
		capHandler,
		adminAuthHandler,
		webhookHandler,
		notificationHandler,
		mcpHandler,
	)

	logger.Info("proxy server starting", "addr", cfg.HTTPAddr, "env", cfg.Env)
	if err := router.ListenAndServe(ctx, cfg.HTTPAddr, cfg.ShutdownTimeout); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
	logger.Info("server shutdown complete")
}
