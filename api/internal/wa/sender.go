package wa

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

// normalizePhone ensures the phone number has a valid country code without '+' or spaces.
// Raw 10-digit Indian numbers get "91" prefix automatically.
func normalizePhone(phone string) string {
	var sb strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	digits := sb.String()
	if len(digits) == 10 {
		return "91" + digits
	}
	return digits
}

// Send sends a plain text WhatsApp message.
// phone: 10-digit Indian number or full international digits.
// JID user part must be pure digits (no '+' prefix).
func (w *WAClient) Send(phone, message string) error {
	if w == nil || w.client == nil {
		return fmt.Errorf("wa: client is uninitialized")
	}

	if w.client.Store == nil || w.client.Store.ID == nil {
		return fmt.Errorf("wa: not logged in (no linked device). Please scan QR at /qr")
	}

	if !w.client.IsConnected() {
		return fmt.Errorf("wa: client is disconnected/reconnecting. Please retry in a few moments")
	}

	targetPhone := normalizePhone(phone)
	if len(targetPhone) < 10 {
		return fmt.Errorf("wa: invalid phone number: %s", phone)
	}

	// WhatsApp JIDs use plain digits — no '+' sign.
	jid := types.NewJID(targetPhone, types.DefaultUserServer)

	msg := &waE2E.Message{
		Conversation: proto.String(message),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	resp, err := w.client.SendMessage(ctx, jid, msg)
	if err != nil {
		log.Error().Err(err).Str("to", targetPhone).Msg("wa: send failed")
		return fmt.Errorf("wa: send failed: %w", err)
	}

	log.Info().Str("to", targetPhone).Str("msgID", resp.ID).Msg("wa: message sent")
	return nil
}
