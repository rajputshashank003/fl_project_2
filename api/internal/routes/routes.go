package routes

import (
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/shashankrajput/ngo-platform/api/internal/handler"
	"github.com/shashankrajput/ngo-platform/api/internal/middleware"
	"github.com/shashankrajput/ngo-platform/api/internal/repository"
	"github.com/shashankrajput/ngo-platform/api/internal/service"
	"github.com/shashankrajput/ngo-platform/api/internal/wa"
	"gorm.io/gorm"
)

// Setup wires all routes onto the gin Engine and returns it.
func Setup(
	db *gorm.DB,
	authSvc *service.AuthService,
	donationSvc *service.DonationService,
	idCardSvc *service.IDCardService,
	noticeSvc *service.NoticeService,
	gallerySvc *service.GalleryService,
	eventSvc *service.EventService,
	teamSvc *service.TeamService,
	ngoSvc *service.NgoService,
	userSvc *service.UserService,
	smsSvc *service.SMSService,
	emailSvc *service.EmailService,
	whatsappTwilioSvc *service.WhatsAppTwilioService,
	whatsappLocalSvc *service.WhatsAppLocalService,
	healthSvc *service.HealthService,
	waClient *wa.WAClient,
	whatsappAdminPassword string,
	userRepo *repository.UserRepository,
	idempotencyRepo *repository.IdempotencyRepository,
	bodyLimitBytes int64,
	feURLs []string,
) *gin.Engine {
	r := gin.New()

	// ---- Global middleware --------------------------------------------------
	r.Use(middleware.RequestID())
	r.Use(middleware.RequestLogger())
	r.Use(gin.Recovery())
	r.Use(middleware.BodyLimit(bodyLimitBytes))

	var corsConfig cors.Config
	if len(feURLs) == 0 || (len(feURLs) == 1 && feURLs[0] == "*") {
		corsConfig = cors.Config{
			AllowAllOrigins:  true,
			AllowMethods:     []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS", "HEAD"},
			AllowHeaders:     []string{"Authorization", "Content-Type", "X-Request-ID", "Idempotency-Key", "Origin", "Accept", "X-Requested-With"},
			ExposeHeaders:    []string{"X-Request-ID", "Content-Length", "Content-Disposition"},
			AllowCredentials: false,
		}
	} else {
		allowedMap := make(map[string]bool)
		for _, u := range feURLs {
			allowedMap[strings.ToLower(strings.TrimRight(u, "/"))] = true
		}

		corsConfig = cors.Config{
			AllowOrigins: feURLs,
			AllowOriginFunc: func(origin string) bool {
				norm := strings.ToLower(strings.TrimRight(origin, "/"))
				return allowedMap[norm]
			},
			AllowMethods:     []string{"GET", "POST", "PATCH", "PUT", "DELETE", "OPTIONS", "HEAD"},
			AllowHeaders:     []string{"Authorization", "Content-Type", "X-Request-ID", "Idempotency-Key", "Origin", "Accept", "X-Requested-With"},
			ExposeHeaders:    []string{"X-Request-ID", "Content-Length", "Content-Disposition"},
			AllowCredentials: true,
		}
	}
	r.Use(cors.New(corsConfig))

	// ---- Handlers ----------------------------------------------------------
	authH := handler.NewAuthHandler(authSvc)
	donationH := handler.NewDonationHandler(donationSvc, idempotencyRepo)
	idCardH := handler.NewIDCardHandler(idCardSvc, idempotencyRepo)
	noticeH := handler.NewNoticeHandler(noticeSvc)
	galleryH := handler.NewGalleryHandler(gallerySvc)
	eventH := handler.NewEventHandler(eventSvc)
	teamH := handler.NewTeamHandler(teamSvc)
	ngoH := handler.NewNgoHandler(ngoSvc)
	userH := handler.NewUserHandler(userSvc)
	notifyH := handler.NewNotifyHandler(smsSvc, emailSvc, whatsappTwilioSvc, whatsappLocalSvc)
	healthH := handler.NewHealthHandler(healthSvc)
	whatsAppH := handler.NewWhatsAppHandler(waClient, whatsappAdminPassword)

	// ---- Infra routes (no version prefix) ----------------------------------
	r.GET("/healthz", healthH.Liveness)
	r.GET("/readyz", healthH.Readiness)
	r.GET("/api/health", healthH.Liveness)
	r.GET("/health", healthH.Liveness)
	r.GET("/health/whatsapp", healthH.WhatsAppHealth)
	r.GET("/qr", whatsAppH.QR)
	r.POST("/qr/login", whatsAppH.Login)
	r.GET("/qr/logout", whatsAppH.Logout)
	r.POST("/qr/logout", whatsAppH.Logout)
	r.GET("/qr/refresh", whatsAppH.RefreshQR)
	r.POST("/qr/refresh", whatsAppH.RefreshQR)
	r.GET("/whatsapp/qr", whatsAppH.QR)
	r.GET("/config", ngoH.GetConfig)
	r.GET("/ngo/config", ngoH.GetConfig)

	// ---- API v1 routes -----------------------------------------------------
	v1 := r.Group("/api/v1")
	v1.GET("/health/whatsapp", healthH.WhatsAppHealth)
	v1.GET("/whatsapp/qr", whatsAppH.QR)
	v1.POST("/whatsapp/login", whatsAppH.Login)
	v1.GET("/whatsapp/logout", whatsAppH.Logout)
	v1.POST("/whatsapp/logout", whatsAppH.Logout)
	v1.GET("/whatsapp/refresh", whatsAppH.RefreshQR)
	v1.POST("/whatsapp/refresh", whatsAppH.RefreshQR)
	v1.GET("/whatsapp/status", whatsAppH.Status)

	// Auth (public)
	auth := v1.Group("/auth")
	{
		auth.POST("/send-otp", authH.SendOTP)
		auth.POST("/verify-otp", authH.VerifyOTP)
	}

	// NGO config & aliases (/api/v1/ngo/config, /api/v1/config, /api/v1/ngo)
	v1.GET("/config", ngoH.GetConfig)
	v1.PATCH("/config", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), ngoH.UpdateConfig)
	v1.GET("/ngo", ngoH.GetConfig)
	v1.PATCH("/ngo", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), ngoH.UpdateConfig)

	ngo := v1.Group("/ngo")
	{
		ngo.GET("/config", ngoH.GetConfig)
		ngo.PATCH("/config", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), ngoH.UpdateConfig)
	}

	// Notices
	notices := v1.Group("/notices")
	{
		notices.GET("", noticeH.List)
		notices.POST("", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), noticeH.Create)
		notices.PATCH("/:id", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), noticeH.Update)
		notices.DELETE("/:id", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), noticeH.Delete)
	}

	// Gallery
	gallery := v1.Group("/gallery")
	{
		gallery.GET("", galleryH.List)
		gallery.POST("", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), galleryH.Upload)
		gallery.DELETE("/:id", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), galleryH.Delete)
	}

	// Events
	events := v1.Group("/events")
	{
		events.GET("", eventH.List)
		events.POST("", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), eventH.Create)
		events.PATCH("/:id", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), eventH.Update)
		events.DELETE("/:id", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), eventH.Delete)
	}

	// Team
	team := v1.Group("/team")
	{
		team.GET("", teamH.List)
		team.POST("/add-slot", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), teamH.AddSlot)
		team.PATCH("/:slot", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), teamH.UpdateSlot)
		team.PATCH("/:slot/clear", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), teamH.ClearSlot)
		team.DELETE("/slot/:slot", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), teamH.RemoveSlot)
	}

	// Donations
	donations := v1.Group("/donations")
	{
		donations.GET("", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), donationH.List)
		donations.GET("/:id", middleware.Auth(authSvc, userRepo), donationH.GetByID)
		donations.POST("", middleware.Auth(authSvc, userRepo), donationH.Create)
		donations.PATCH("/:id/status", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), donationH.UpdateStatus)
	}

	// ID Cards
	idCards := v1.Group("/id-cards")
	{
		idCards.GET("", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), idCardH.List)
		idCards.GET("/:id", middleware.Auth(authSvc, userRepo), idCardH.GetByID)
		idCards.POST("", middleware.Auth(authSvc, userRepo), idCardH.Create)
		idCards.PATCH("/:id/status", middleware.Auth(authSvc, userRepo), middleware.AdminOnly(), idCardH.UpdateStatus)
	}

	// Users
	users := v1.Group("/users")
	users.Use(middleware.Auth(authSvc, userRepo), middleware.AdminOnly())
	{
		users.GET("", userH.List)
		users.PATCH("/:id", userH.Update)
		users.PATCH("/:id/promote", userH.Promote)
		users.PATCH("/:id/demote", userH.Demote)
	}

	// Notifications (manual)
	notify := v1.Group("/notify")
	notify.Use(middleware.Auth(authSvc, userRepo))
	{
		notify.POST("/sms", notifyH.SendSMS)
		notify.POST("/email", notifyH.SendEmail)
		notify.POST("/whatsapp_twilio", notifyH.SendWhatsAppTwilio)
		notify.POST("/whatsapp_local", notifyH.SendWhatsAppLocal)
	}

	// User profile: authenticated user's own records & aliases
	v1.GET("/profile", middleware.Auth(authSvc, userRepo), userH.GetMyProfile)
	v1.PATCH("/profile", middleware.Auth(authSvc, userRepo), userH.UpdateMyProfile)
	v1.GET("/user/profile", middleware.Auth(authSvc, userRepo), userH.GetMyProfile)
	v1.PATCH("/user/profile", middleware.Auth(authSvc, userRepo), userH.UpdateMyProfile)

	my := v1.Group("/my")
	my.Use(middleware.Auth(authSvc, userRepo))
	{
		my.GET("/donations", donationH.ListMy)
		my.GET("/id-cards", idCardH.ListMy)
		my.GET("/profile", userH.GetMyProfile)
		my.PATCH("/profile", userH.UpdateMyProfile)
	}

	return r
}
