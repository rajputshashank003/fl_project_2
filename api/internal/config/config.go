package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Config holds all environment-driven configuration for the application.
// No defaults are assumed — required fields cause a startup panic.
type Config struct {
	// Server
	Port        string
	GinMode     string
	BodyLimitMB int64
	LogLevel    string

	// PostgreSQL
	DBHost         string
	DBPort         string
	DBUser         string
	DBPassword     string
	DBName         string
	DBSSLMode      string
	DBMaxOpenConns int
	DBMaxIdleConns int

	// JWT
	JWTSecret      string
	JWTExpiryHours int

	// Admin seed
	AdminPhone string
	AdminName  string

	// Dev mode
	DevMode bool
	DevOTP  string

	// Twilio
	TwilioAccountSID  string
	TwilioAuthToken   string
	TwilioFromPhone   string
	TwilioWhatsAppFrom string // "whatsapp:+14155238886" for sandbox; production: "whatsapp:+91XXXXXXXXXX"

	// App & CORS
	AppBaseURL string   // public-facing URL e.g. "https://ngo.costop.in" — used in notification deep links
	FEUrls     []string // Allowed CORS frontend origins parsed from FE_URLS

	// Messaging
	MessagingType         string // "sms" | "whatsapp_twilio" | "whatsapp_local"
	WhatsAppDBPath        string // SQLite session database path (e.g. "store/whatsapp.db")
	WhatsAppLocalURL      string // (Optional fallback) base URL of standalone whatsapp_service (e.g. "http://localhost:8080")
	WhatsAppLocalAPIKey   string // API key for authenticating with whatsapp_service
	WhatsAppAdminPassword string // Admin password for /qr access & actions

	// Resend
	ResendAPIKey    string
	ResendFromEmail string

	// Cloudinary
	CloudinaryCloudName    string
	CloudinaryAPIKey       string
	CloudinaryAPISecret    string
	CloudinaryUploadFolder string

	// OTP
	OTPExpiryMinutes int
	OTPMaxPer10Min   int
}

// Load reads .env (if present) then environment variables, validates required
// fields, and returns a fully populated Config.
func Load() (*Config, error) {
	// Load .env file — non-fatal if missing (env vars may be set by Docker/OS)
	_ = godotenv.Load()

	cfg := &Config{
		Port:        getEnv("PORT", "3000"),
		GinMode:     getEnv("GIN_MODE", "debug"),
		BodyLimitMB: getEnvInt64("BODY_LIMIT_MB", 20),
		LogLevel:    getEnv("LOG_LEVEL", "info"),

		DBHost:         getEnv("DB_HOST", "localhost"),
		DBPort:         getEnv("DB_PORT", "5432"),
		DBUser:         mustGetEnv("DB_USER"),
		DBPassword:     mustGetEnv("DB_PASSWORD"),
		DBName:         mustGetEnv("DB_NAME"),
		DBSSLMode:      getEnv("DB_SSLMODE", "disable"),
		DBMaxOpenConns: getEnvInt("DB_MAX_OPEN_CONNS", 25),
		DBMaxIdleConns: getEnvInt("DB_MAX_IDLE_CONNS", 10),

		JWTSecret:      mustGetEnv("JWT_SECRET"),
		JWTExpiryHours: getEnvInt("JWT_EXPIRY_HOURS", 72),

		AdminPhone: getEnv("ADMIN_PHONE", ""),
		AdminName:  getEnv("ADMIN_NAME", "Admin"),

		DevMode: getEnv("DEV_MODE", "false") == "true",
		DevOTP:  getEnv("DEV_OTP", "123456"),

		TwilioAccountSID:   getEnv("TWILIO_ACCOUNT_SID", ""),
		TwilioAuthToken:    getEnv("TWILIO_AUTH_TOKEN", ""),
		TwilioFromPhone:    getEnv("TWILIO_FROM_PHONE", ""),
		TwilioWhatsAppFrom: getEnv("TWILIO_WHATSAPP_FROM", "whatsapp:+14155238886"),

		AppBaseURL: getEnv("APP_BASE_URL", "https://ngo.costop.in"),
		FEUrls:     parseOrigins(getEnv("FE_URLS", "")),

		MessagingType:         getEnv("MESSAGING_TYPE", "sms"),
		WhatsAppDBPath:        getEnv("WHATSAPP_DB_PATH", "store/whatsapp.db"),
		WhatsAppLocalURL:      getEnv("WHATSAPP_LOCAL_URL", "http://localhost:8080"),
		WhatsAppLocalAPIKey:   getEnv("WHATSAPP_LOCAL_API_KEY", ""),
		WhatsAppAdminPassword: getEnv("WHATSAPP_ADMIN_PASSWORD", ""),

		ResendAPIKey:    getEnv("RESEND_API_KEY", ""),
		ResendFromEmail: getEnv("RESEND_FROM_EMAIL", "noreply@example.com"),

		CloudinaryCloudName:    getEnv("CLOUDINARY_CLOUD_NAME", ""),
		CloudinaryAPIKey:       getEnv("CLOUDINARY_API_KEY", ""),
		CloudinaryAPISecret:    getEnv("CLOUDINARY_API_SECRET", ""),
		CloudinaryUploadFolder: getEnv("CLOUDINARY_UPLOAD_FOLDER", "ngo_platform"),

		OTPExpiryMinutes: getEnvInt("OTP_EXPIRY_MINUTES", 5),
		OTPMaxPer10Min:   getEnvInt("OTP_MAX_PER_10MIN", 3),
	}

	return cfg, nil
}

// DSN returns the PostgreSQL connection string for GORM.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// ---- helpers ----------------------------------------------------------------

// parseOrigins parses a comma, semicolon, space, or newline-delimited string of URLs.
// Trims quotes, trailing slashes, and normalizes plain domain names to both https:// and http://.
func parseOrigins(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	tokens := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t' || r == '\r'
	})

	seen := make(map[string]bool)
	var origins []string

	for _, token := range tokens {
		item := strings.TrimSpace(token)
		item = strings.Trim(item, "\"'`")
		if item == "" {
			continue
		}
		if item == "*" {
			if !seen["*"] {
				seen["*"] = true
				origins = append(origins, "*")
			}
			continue
		}
		item = strings.TrimRight(item, "/")
		if strings.HasPrefix(item, "http://") || strings.HasPrefix(item, "https://") {
			if !seen[item] {
				seen[item] = true
				origins = append(origins, item)
			}
		} else {
			httpsItem := "https://" + item
			httpItem := "http://" + item
			if !seen[httpsItem] {
				seen[httpsItem] = true
				origins = append(origins, httpsItem)
			}
			if !seen[httpItem] {
				seen[httpItem] = true
				origins = append(origins, httpItem)
			}
		}
	}
	return origins
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func mustGetEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return i
		}
	}
	return fallback
}
