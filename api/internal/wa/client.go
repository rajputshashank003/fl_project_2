package wa

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

// Status values returned by Status().
const (
	StatusConnected    = "connected"
	StatusQRPending    = "qr_pending"
	StatusDisconnected = "disconnected"
)

// WAClient wraps a whatsmeow client with connection lifecycle management.
type WAClient struct {
	mu        sync.RWMutex
	container *wastore.Container
	client    *whatsmeow.Client
	qrCode    string // base64 PNG of current QR code (only set when qr_pending)
	status    string
	isPairing bool
}

// NewWAClient creates a WAClient, loads session from SQLite, and connects or initiates QR.
func NewWAClient(container *wastore.Container) (*WAClient, error) {
	wac := &WAClient{
		container: container,
		status:    StatusDisconnected,
	}

	if err := wac.initClientLocked(); err != nil {
		return nil, err
	}

	wac.mu.Lock()
	defer wac.mu.Unlock()

	// If a paired device already exists in the store, connect directly.
	if wac.client.Store.ID != nil {
		log.Info().Str("jid", wac.client.Store.ID.String()).Msg("wa: existing session found, connecting")
		if err := wac.client.Connect(); err != nil {
			log.Error().Err(err).Msg("wa: failed to connect existing session (will auto-reconnect)")
		}
	} else {
		// No session exists; start on-demand QR pairing.
		log.Info().Msg("wa: no session found in store, starting initial QR pairing")
		if err := wac.startQRLocked(); err != nil {
			log.Error().Err(err).Msg("wa: failed to start initial QR pairing")
		}
	}

	return wac, nil
}

// initClientLocked initializes or re-initializes the underlying whatsmeow client.
// Must be called with wac.mu held or before concurrent access.
func (w *WAClient) initClientLocked() error {
	deviceStore, err := w.container.GetFirstDevice(context.Background())
	if err != nil {
		return fmt.Errorf("wa: get device failed: %w", err)
	}

	clientLog := waLog.Stdout("Client", "WARN", true)
	w.client = whatsmeow.NewClient(deviceStore, clientLog)

	w.client.AddEventHandler(func(evt interface{}) {
		switch evt.(type) {
		case *events.Connected:
			w.mu.Lock()
			if w.client.Store.ID != nil {
				w.status = StatusConnected
				w.qrCode = ""
				w.isPairing = false
				log.Info().Str("jid", w.client.Store.ID.String()).Msg("wa: connected to WhatsApp")
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = w.client.SendPresence(ctx, types.PresenceAvailable)
				}()
			}
			w.mu.Unlock()

		case *events.LoggedOut:
			w.mu.Lock()
			w.status = StatusDisconnected
			w.qrCode = ""
			w.isPairing = false
			log.Warn().Msg("wa: logged out from WhatsApp by server or mobile device")
			if w.client.Store != nil {
				_ = w.client.Store.Delete(context.Background())
			}
			w.mu.Unlock()

		case *events.StreamReplaced:
			log.Warn().Msg("wa: stream replaced by another active session")

		case *events.Disconnected:
			w.mu.Lock()
			if w.client.Store.ID != nil {
				w.status = StatusDisconnected
			}
			w.mu.Unlock()
			log.Warn().Msg("wa: disconnected from WhatsApp (auto-reconnect active)")
		}
	})

	return nil
}

// startQRLocked initiates the QR code channel. Must be called while holding w.mu.Lock().
func (w *WAClient) startQRLocked() error {
	if w.client == nil {
		return fmt.Errorf("wa: client not initialized")
	}

	// If already authenticated, no QR code is needed.
	if w.client.Store.ID != nil {
		w.status = StatusConnected
		w.qrCode = ""
		w.isPairing = false
		return nil
	}

	// Disconnect existing socket before requesting QR channel (whatsmeow requirement).
	if w.client.IsConnected() {
		w.client.Disconnect()
	}

	qrChan, err := w.client.GetQRChannel(context.Background())
	if err != nil {
		return fmt.Errorf("wa: failed to get qr channel: %w", err)
	}

	w.status = StatusQRPending
	w.isPairing = true

	if err := w.client.Connect(); err != nil {
		w.isPairing = false
		w.status = StatusDisconnected
		return fmt.Errorf("wa: connect for QR failed: %w", err)
	}

	go func() {
		for evt := range qrChan {
			switch evt.Event {
			case "code":
				png, err := qrToPNG(evt.Code)
				if err != nil {
					log.Error().Err(err).Msg("wa: failed to encode QR code")
					continue
				}
				w.mu.Lock()
				w.qrCode = base64.StdEncoding.EncodeToString(png)
				w.status = StatusQRPending
				w.mu.Unlock()
				log.Info().Msg("wa: QR code ready — open /qr in browser to scan")

			case "success":
				w.mu.Lock()
				w.status = StatusConnected
				w.qrCode = ""
				w.isPairing = false
				w.mu.Unlock()
				log.Info().Msg("wa: QR pairing successful — connected to WhatsApp")
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = w.client.SendPresence(ctx, types.PresenceAvailable)
				}()

			case "timeout":
				w.mu.Lock()
				w.status = StatusDisconnected
				w.qrCode = ""
				w.isPairing = false
				w.mu.Unlock()
				log.Warn().Msg("wa: QR code pairing timed out (can be refreshed on /qr)")

			case "error":
				w.mu.Lock()
				w.status = StatusDisconnected
				w.qrCode = ""
				w.isPairing = false
				w.mu.Unlock()
				log.Error().Err(evt.Error).Msg("wa: QR pairing error")
			}
		}

		w.mu.Lock()
		w.isPairing = false
		w.mu.Unlock()
	}()

	return nil
}

// EnsureQR ensures that an active QR code is available if the client is not logged in.
// If the previous QR timed out or was never started, it cleanly starts a fresh QR pairing.
func (w *WAClient) EnsureQR() error {
	if w == nil {
		return fmt.Errorf("wa: client is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// If already authenticated, no QR code is needed.
	if w.client != nil && w.client.Store.ID != nil {
		return nil
	}

	// If currently pairing and we have a valid QR code, keep it.
	if w.isPairing && w.qrCode != "" {
		return nil
	}

	return w.startQRLocked()
}

// RefreshQR forces a fresh QR pairing session.
func (w *WAClient) RefreshQR() error {
	if w == nil {
		return fmt.Errorf("wa: client is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// If already authenticated, don't generate QR.
	if w.client != nil && w.client.Store.ID != nil {
		return nil
	}

	w.qrCode = ""
	w.isPairing = false
	return w.startQRLocked()
}

// Logout unlinks the current device session from WhatsApp and resets for a fresh QR scan.
func (w *WAClient) Logout(ctx context.Context) error {
	if w == nil {
		return fmt.Errorf("wa: client is nil")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.client != nil {
		if w.client.IsConnected() && w.client.Store.ID != nil {
			err := w.client.Logout(ctx)
			if err != nil {
				log.Warn().Err(err).Msg("wa: client.Logout returned error, forcing disconnect and local store delete")
				w.client.Disconnect()
				if w.client.Store != nil {
					_ = w.client.Store.Delete(ctx)
				}
			}
		} else {
			w.client.Disconnect()
			if w.client.Store != nil {
				_ = w.client.Store.Delete(ctx)
			}
		}
	}

	w.status = StatusDisconnected
	w.qrCode = ""
	w.isPairing = false

	// Reinitialize clean client with new device store.
	if err := w.initClientLocked(); err != nil {
		return fmt.Errorf("wa: re-init client after logout failed: %w", err)
	}

	// Automatically start fresh QR pairing so user can immediately link a new account.
	return w.startQRLocked()
}

// GetQR returns the current QR code as a base64-encoded PNG.
// Returns empty string if not in qr_pending state.
func (w *WAClient) GetQR() string {
	if w == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.qrCode
}

// Status returns the current connection status string.
// Strictly checks that device ID exists before returning StatusConnected.
func (w *WAClient) Status() string {
	if w == nil || w.client == nil {
		return StatusDisconnected
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	// If no device JID exists, it cannot be connected.
	if w.client.Store.ID == nil {
		if w.qrCode != "" {
			return StatusQRPending
		}
		return StatusDisconnected
	}

	if !w.client.IsConnected() {
		return StatusDisconnected
	}

	return StatusConnected
}

// GetLinkedPhone returns the phone number (user part of JID) of the logged-in device, or empty string.
func (w *WAClient) GetLinkedPhone() string {
	if w == nil || w.client == nil {
		return ""
	}
	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.client.Store != nil && w.client.Store.ID != nil {
		return w.client.Store.ID.User
	}
	return ""
}

// Disconnect cleanly disconnects the WhatsApp client.
func (w *WAClient) Disconnect() {
	if w != nil && w.client != nil {
		w.client.Disconnect()
	}
}

// Client returns the underlying whatsmeow client (used by Sender).
func (w *WAClient) Client() *whatsmeow.Client {
	if w == nil {
		return nil
	}
	return w.client
}
