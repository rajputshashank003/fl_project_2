package service

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/shashankrajput/ngo-platform/api/internal/config"
	"github.com/shashankrajput/ngo-platform/api/internal/models"
	"github.com/shashankrajput/ngo-platform/api/internal/repository"
)

// OTPService handles OTP generation, rate-limiting, storage, and verification.
type OTPService struct {
	repo      *repository.OTPRepository
	messenger Messenger
	cfg       *config.Config
}

// NewOTPService constructs an OTPService.
func NewOTPService(repo *repository.OTPRepository, messenger Messenger, cfg *config.Config) *OTPService {
	return &OTPService{repo: repo, messenger: messenger, cfg: cfg}
}

// Send generates a 6-digit OTP, stores it, and sends via SMS.
// Returns an error if rate-limited or DB fails.
func (s *OTPService) Send(phone string) error {
	// Rate limit: max N sends per 10 minutes
	count, err := s.repo.CountRecentByPhone(phone)
	if err != nil {
		return fmt.Errorf("otp: rate-limit check failed: %w", err)
	}
	if count >= int64(s.cfg.OTPMaxPer10Min) {
		return fmt.Errorf("otp: too many requests — try again later")
	}

	// Invalidate any previous unused OTPs for this phone
	if err := s.repo.InvalidatePrevious(phone); err != nil {
		log.Warn().Err(err).Str("phone", phone).Msg("otp: failed to invalidate previous")
	}

	// Generate OTP: use DEV_OTP in dev mode
	code := s.generateCode()
	if s.cfg.DevMode {
		code = s.cfg.DevOTP
		log.Info().Str("phone", phone).Str("otp", code).Msg("[DEV] OTP generated")
	}

	otp := &models.OTP{
		Phone:     phone,
		OTPCode:   code,
		ExpiresAt: time.Now().Add(time.Duration(s.cfg.OTPExpiryMinutes) * time.Minute),
		Used:      false,
	}

	if err := s.repo.Create(otp); err != nil {
		return fmt.Errorf("otp: failed to store: %w", err)
	}

	// Send message (fire-and-forget in prod; logged in dev)
	go s.messenger.Send(phone, fmt.Sprintf("Your NGO Portal OTP is %s. Valid for %d minutes.", code, s.cfg.OTPExpiryMinutes))

	return nil
}

// Verify checks the OTP and marks it used if valid.
// Returns error for wrong, expired, or already-used OTP.
func (s *OTPService) Verify(phone, code string) error {
	if s.cfg.DevMode && (code == s.cfg.DevOTP || code == "123456") {
		// In dev mode, allow dev OTP directly
		if otp, err := s.repo.FindValidByPhone(phone); err == nil {
			_ = s.repo.MarkUsed(otp.ID)
		}
		return nil
	}

	otp, err := s.repo.FindValidByPhone(phone)
	if err != nil {
		return fmt.Errorf("otp: invalid or expired")
	}

	if otp.OTPCode != code {
		return fmt.Errorf("otp: incorrect OTP")
	}

	if err := s.repo.MarkUsed(otp.ID); err != nil {
		return fmt.Errorf("otp: failed to mark used: %w", err)
	}

	return nil
}

// generateCode returns a random 6-digit string.
func (s *OTPService) generateCode() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%06d", r.Intn(1000000))
}
