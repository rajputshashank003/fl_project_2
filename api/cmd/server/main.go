package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/shashankrajput/ngo-platform/api/internal/config"
	"github.com/shashankrajput/ngo-platform/api/internal/database"
	"github.com/shashankrajput/ngo-platform/api/internal/repository"
	"github.com/shashankrajput/ngo-platform/api/internal/routes"
	"github.com/shashankrajput/ngo-platform/api/internal/service"
	"github.com/shashankrajput/ngo-platform/api/internal/wa"
)

func main() {
	// ---- Logging -----------------------------------------------------------
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	logLevel, _ := zerolog.ParseLevel(os.Getenv("LOG_LEVEL"))
	if logLevel == zerolog.NoLevel {
		logLevel = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(logLevel)
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})

	// ---- Config ------------------------------------------------------------
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("main: config load failed")
	}

	// ---- Database ----------------------------------------------------------
	db := database.Connect(cfg)

	// ---- Repositories ------------------------------------------------------
	userRepo := repository.NewUserRepository(db)
	donationRepo := repository.NewDonationRepository(db)
	idCardRepo := repository.NewIDCardRepository(db)
	noticeRepo := repository.NewNoticeRepository(db)
	galleryRepo := repository.NewGalleryRepository(db)
	eventRepo := repository.NewEventRepository(db)
	teamRepo := repository.NewTeamRepository(db)
	ngoRepo := repository.NewOrgSettingsRepository(db)
	otpRepo := repository.NewOTPRepository(db)
	idempotencyRepo := repository.NewIdempotencyRepository(db)

	// ---- In-memory WhatsApp service -----------------------------------------
	waContainer, err := wa.OpenContainer(cfg.WhatsAppDBPath)
	if err != nil {
		log.Error().Err(err).Msg("main: failed to open WhatsApp SQLite store; continuing without WhatsApp")
	}
	var waClient *wa.WAClient
	if waContainer != nil {
		waClient, err = wa.NewWAClient(waContainer)
		if err != nil {
			log.Error().Err(err).Msg("main: failed to initialize WhatsApp client; continuing without WhatsApp")
		}
	}

	// ---- External services -------------------------------------------------
	cloudinarySvc := service.NewCloudinaryService(cfg)
	smsSvc := service.NewSMSService(cfg)
	emailSvc := service.NewEmailService(cfg)
	whatsappTwilioSvc := service.NewWhatsAppTwilioService(cfg)
	whatsappLocalSvc := service.NewWhatsAppLocalService(waClient, cfg.DevMode)

	// MultiMessenger routes to the correct channel based on MESSAGING_TYPE env var
	messenger := service.NewMultiMessenger(smsSvc, whatsappTwilioSvc, whatsappLocalSvc, cfg.MessagingType)
	log.Info().Str("messagingType", cfg.MessagingType).Str("appBaseURL", cfg.AppBaseURL).Msg("main: messaging configured")

	// ---- Domain services ---------------------------------------------------
	otpSvc := service.NewOTPService(otpRepo, messenger, cfg)
	authSvc := service.NewAuthService(userRepo, otpSvc, cfg)
	donationSvc := service.NewDonationService(donationRepo, ngoRepo, cloudinarySvc, messenger, emailSvc, cfg.AppBaseURL)
	idCardSvc := service.NewIDCardService(idCardRepo, ngoRepo, cloudinarySvc, messenger, emailSvc, cfg.AppBaseURL)
	noticeSvc := service.NewNoticeService(noticeRepo, cloudinarySvc)
	gallerySvc := service.NewGalleryService(galleryRepo, cloudinarySvc)
	eventSvc := service.NewEventService(eventRepo, cloudinarySvc)
	teamSvc := service.NewTeamService(teamRepo, cloudinarySvc)
	ngoSvc := service.NewNgoService(ngoRepo, cloudinarySvc)
	userSvc := service.NewUserService(userRepo)
	healthSvc := service.NewHealthService(db, waClient)

	// ---- Seed admin user ---------------------------------------------------
	authSvc.SeedAdmin()

	// ---- Graceful shutdown context -----------------------------------------
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ---- Background cleanup service (purges expired idempotency keys & OTPs older than 24h) ----
	cleanupSvc := service.NewCleanupService(idempotencyRepo, otpRepo, 1*time.Hour, 24*time.Hour)
	go cleanupSvc.Run(ctx)

	// ---- HTTP router -------------------------------------------------------
	bodyLimitBytes := cfg.BodyLimitMB * 1024 * 1024
	router := routes.Setup(
		db,
		authSvc, donationSvc, idCardSvc, noticeSvc,
		gallerySvc, eventSvc, teamSvc, ngoSvc, userSvc,
		smsSvc, emailSvc, whatsappTwilioSvc, whatsappLocalSvc,
		healthSvc,
		waClient,
		cfg.WhatsAppAdminPassword,
		userRepo, idempotencyRepo,
		bodyLimitBytes,
		cfg.FEUrls,
	)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// ---- Start server ------------------------------------------------------
	go func() {
		log.Info().Str("port", cfg.Port).Msg("main: server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("main: server error")
		}
	}()

	// ---- Wait for shutdown signal ------------------------------------------
	<-ctx.Done()
	log.Info().Msg("main: shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("main: server shutdown error")
	}

	if waClient != nil {
		waClient.Disconnect()
		log.Info().Msg("main: WhatsApp client disconnected")
	}

	if sqlDB, err := db.DB(); err == nil {
		sqlDB.Close()
	}

	log.Info().Msg("main: server exited cleanly")
}
