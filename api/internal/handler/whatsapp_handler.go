package handler

import (
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shashankrajput/ngo-platform/api/internal/wa"
)

// WhatsAppHandler handles WhatsApp QR, status, password login, and lifecycle endpoints.
type WhatsAppHandler struct {
	client        *wa.WAClient
	adminPassword string
}

// NewWhatsAppHandler constructs a WhatsAppHandler.
func NewWhatsAppHandler(client *wa.WAClient, adminPassword string) *WhatsAppHandler {
	return &WhatsAppHandler{
		client:        client,
		adminPassword: adminPassword,
	}
}

// isAuthenticated checks if the request has valid admin credentials via cookie, header, or query.
func (h *WhatsAppHandler) isAuthenticated(c *gin.Context) bool {
	if h.adminPassword == "" {
		return false
	}

	// 1. Check HTTP-only cookie
	if cookie, err := c.Cookie("wa_admin_auth"); err == nil && cookie == h.adminPassword {
		return true
	}

	// 2. Check query parameter
	if qPass := c.Query("password"); qPass != "" && qPass == h.adminPassword {
		return true
	}

	// 3. Check custom header
	if hPass := c.GetHeader("X-WhatsApp-Password"); hPass != "" && hPass == h.adminPassword {
		return true
	}

	return false
}

// Login handles POST /qr/login to verify the admin password.
func (h *WhatsAppHandler) Login(c *gin.Context) {
	if h.adminPassword == "" {
		h.renderPasswordPrompt(c, "WhatsApp Admin Password is not configured in server environment. Access is disabled.")
		return
	}

	if c.Request != nil {
		_ = c.Request.ParseForm()
	}
	password := c.PostForm("password")
	if password == "" {
		password = c.Query("password")
	}

	if password == h.adminPassword {
		c.SetCookie("wa_admin_auth", h.adminPassword, 7*24*3600, "/", "", false, true)
		c.Redirect(http.StatusFound, "/qr")
		return
	}

	h.renderPasswordPrompt(c, "Invalid password. Please check your credentials and try again.")
}

// renderPasswordPrompt displays a sleek password entry card.
func (h *WhatsAppHandler) renderPasswordPrompt(c *gin.Context, errorMsg string) {
	if h.adminPassword == "" {
		errorMsg = "WhatsApp Admin Password is not configured in server environment (WHATSAPP_ADMIN_PASSWORD). Access is disabled."
	}

	errorHTML := ""
	if errorMsg != "" {
		errorHTML = `<div style="background:#fee2e2;color:#991b1b;padding:12px 16px;border-radius:10px;font-size:13px;font-weight:600;margin-bottom:18px;border:1px solid #fecaca;line-height:1.4;">` + errorMsg + `</div>`
	}

	formHTML := `
    <form method="POST" action="/qr/login">
      <input type="password" name="password" placeholder="Enter password..." autofocus required />
      <button type="submit">Unlock Service</button>
    </form>`
	if h.adminPassword == "" {
		formHTML = `<p style="font-size:13px;color:#dc2626;font-weight:600;margin-top:16px;">Please set WHATSAPP_ADMIN_PASSWORD in the server environment and restart the server.</p>`
	}

	html := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>WhatsApp Service — Admin Access</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #f1f5f9; color: #0f172a; }
    .card { background: white; padding: 40px; border-radius: 20px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.06), 0 8px 10px -6px rgba(0,0,0,0.04); text-align: center; width: 100%; max-width: 360px; box-sizing: border-box; border: 1px solid #e2e8f0; }
    .lock-icon { width: 56px; height: 56px; background: #e0f2fe; color: #0284c7; border-radius: 50%; display: inline-flex; align-items: center; justify-content: center; font-size: 26px; margin-bottom: 20px; }
    h2 { margin: 0 0 8px 0; font-size: 22px; font-weight: 700; color: #0f172a; }
    p { color: #64748b; font-size: 14px; margin: 0 0 24px 0; line-height: 1.5; }
    input[type="password"] { width: 100%; padding: 12px 16px; border: 1.5px solid #cbd5e1; border-radius: 10px; font-size: 15px; outline: none; box-sizing: border-box; transition: border-color 0.2s; }
    input[type="password"]:focus { border-color: #0284c7; ring: 2px solid #bae6fd; }
    button { width: 100%; margin-top: 14px; padding: 12px 16px; background: #0284c7; color: white; border: none; border-radius: 10px; font-size: 15px; font-weight: 600; cursor: pointer; transition: background 0.2s; }
    button:hover { background: #0369a1; }
  </style>
</head>
<body>
  <div class="card">
    <div class="lock-icon">🔒</div>
    <h2>Admin Authentication</h2>
    <p>Please enter the WhatsApp Admin Password to manage this service.</p>
    ` + errorHTML + formHTML + `
  </div>
</body>
</html>`

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

// Status godoc — GET /api/v1/whatsapp/status
func (h *WhatsAppHandler) Status(c *gin.Context) {
	status := "not_configured"
	linkedPhone := ""
	if h.client != nil {
		status = h.client.Status()
		linkedPhone = h.client.GetLinkedPhone()
	}
	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"status":      status,
			"linkedPhone": linkedPhone,
		},
	})
}

// Logout godoc — POST /qr/logout or GET /qr/logout
// Unlinks the WhatsApp account and immediately generates a fresh QR code.
func (h *WhatsAppHandler) Logout(c *gin.Context) {
	if !h.isAuthenticated(c) {
		h.renderPasswordPrompt(c, "Please enter your password first.")
		return
	}

	if h.client != nil {
		_ = h.client.Logout(c.Request.Context())
	}

	c.Redirect(http.StatusFound, "/qr")
}

// RefreshQR godoc — POST /qr/refresh or GET /qr/refresh
// Forces a fresh QR pairing handshake without needing a server restart.
func (h *WhatsAppHandler) RefreshQR(c *gin.Context) {
	if !h.isAuthenticated(c) {
		h.renderPasswordPrompt(c, "Please enter your password first.")
		return
	}

	if h.client != nil {
		_ = h.client.RefreshQR()
	}

	c.Redirect(http.StatusFound, "/qr")
}

// QR godoc — GET /qr or GET /api/v1/whatsapp/qr
// Returns an auto-refreshing HTML page with QR image, active status, or logout controls.
func (h *WhatsAppHandler) QR(c *gin.Context) {
	if !h.isAuthenticated(c) {
		h.renderPasswordPrompt(c, "")
		return
	}

	if h.client == nil {
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!DOCTYPE html>
<html>
<head><title>WhatsApp Service</title></head>
<body style="font-family:sans-serif;display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh;margin:0;background:#f0f2f5;">
  <h2>WhatsApp Client Not Initialized</h2>
  <p style="color:#666;">Please check server logs.</p>
</body>
</html>`))
		return
	}

	// If not connected and no QR is currently active, ensure QR pairing is started.
	status := h.client.Status()
	if status != wa.StatusConnected {
		_ = h.client.EnsureQR()
		status = h.client.Status()
	}

	// 1. CONNECTED STATE
	if status == wa.StatusConnected {
		linkedPhone := h.client.GetLinkedPhone()
		phoneBadge := ""
		if linkedPhone != "" {
			phoneBadge = fmt.Sprintf(`<div style="background:#f8fafc;border:1px solid #e2e8f0;padding:8px 16px;border-radius:10px;font-size:14px;color:#334155;margin:16px 0;font-weight:600;">📱 Linked Number: +%s</div>`, linkedPhone)
		}

		html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>WhatsApp Service — Connected</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #f0fdf4; color: #166534; }
    .card { background: white; padding: 40px 48px; border-radius: 20px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.06), 0 8px 10px -6px rgba(0,0,0,0.04); text-align: center; border: 1px solid #bbf7d0; max-width: 420px; width: 100%%; box-sizing: border-box; }
    .badge { display: inline-flex; align-items: center; gap: 8px; background: #dcfce7; color: #15803d; font-weight: 700; font-size: 14px; padding: 6px 18px; border-radius: 9999px; margin-bottom: 16px; }
    h2 { margin: 0 0 8px 0; color: #0f172a; font-size: 22px; }
    p { color: #64748b; margin: 0 0 20px 0; font-size: 14px; line-height: 1.5; }
    .danger-btn { width: 100%%; padding: 12px 18px; background: #fee2e2; color: #b91c1c; border: 1px solid #fecaca; border-radius: 10px; font-weight: 600; font-size: 14px; cursor: pointer; transition: all 0.2s; }
    .danger-btn:hover { background: #fecaca; color: #991b1b; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">✓ Connected</div>
    <h2>WhatsApp is Active</h2>
    <p>Your device is linked and actively delivering OTPs and system notifications.</p>
    %s
    <form method="POST" action="/qr/logout" onsubmit="return confirm('Are you sure you want to unlink this WhatsApp account? You will need to scan a new QR code to reconnect.');">
      <button type="submit" class="danger-btn">Disconnect & Relink WhatsApp</button>
    </form>
  </div>
</body>
</html>`, phoneBadge)

		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
		return
	}

	// 2. QR PENDING STATE
	qrBase64 := h.client.GetQR()
	if qrBase64 == "" {
		html := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>WhatsApp Service — Generating QR</title>
  <meta http-equiv="refresh" content="3">
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #f8fafc; color: #334155; }
    .card { background: white; padding: 36px; border-radius: 20px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.06); text-align: center; max-width: 380px; width: 100%; box-sizing: border-box; border: 1px solid #e2e8f0; }
    .spinner { width: 40px; height: 40px; border: 3.5px solid #e2e8f0; border-top-color: #25d366; border-radius: 50%; animation: spin 0.8s linear infinite; margin: 0 auto 16px auto; }
    @keyframes spin { to { transform: rotate(360deg); } }
    h2 { margin: 0 0 8px 0; font-size: 20px; color: #0f172a; }
    p { color: #64748b; font-size: 14px; margin: 0; }
  </style>
</head>
<body>
  <div class="card">
    <div class="spinner"></div>
    <h2>Generating WhatsApp QR...</h2>
    <p>Please wait, connecting to WhatsApp servers...</p>
  </div>
</body>
</html>`
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
		return
	}

	// Verify valid base64
	if _, err := base64.StdEncoding.DecodeString(qrBase64); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": gin.H{"code": "QR_DECODE_ERROR", "message": "invalid QR data"},
		})
		return
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>WhatsApp Service — Scan QR</title>
  <meta http-equiv="refresh" content="20">
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; display: flex; flex-direction: column; align-items: center; justify-content: center; min-height: 100vh; margin: 0; background: #f0f2f5; }
    .card { background: white; padding: 32px 36px; border-radius: 20px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.08); text-align: center; max-width: 380px; width: 100%%; box-sizing: border-box; }
    .badge { display: inline-block; background: #e0f2fe; color: #0369a1; font-weight: 700; font-size: 12px; padding: 4px 14px; border-radius: 9999px; margin-bottom: 12px; letter-spacing: 0.5px; }
    img { border: 4px solid #25d366; border-radius: 14px; margin: 14px 0; }
    h2 { margin: 0 0 6px 0; font-size: 20px; color: #0f172a; }
    p { color: #64748b; font-size: 13px; margin: 4px 0 0 0; line-height: 1.5; }
    .footer { margin-top: 16px; display: flex; flex-direction: column; gap: 8px; }
    .refresh-btn { padding: 10px 16px; background: #f1f5f9; color: #334155; border: 1px solid #cbd5e1; border-radius: 10px; font-weight: 600; font-size: 13px; cursor: pointer; transition: all 0.2s; }
    .refresh-btn:hover { background: #e2e8f0; color: #0f172a; }
  </style>
</head>
<body>
  <div class="card">
    <div class="badge">READY TO PAIR</div>
    <h2>Link WhatsApp Account</h2>
    <p>Open WhatsApp on your phone → <strong>Settings</strong> → <strong>Linked Devices</strong> → <strong>Link a Device</strong> and point your camera here.</p>
    <img src="data:image/png;base64,%s" width="256" height="256" alt="WhatsApp QR Code"/>
    <div class="footer">
      <p style="font-size:12px;color:#94a3b8;">Page auto-refreshes every 20 seconds.</p>
      <form method="POST" action="/qr/refresh">
        <button type="submit" class="refresh-btn">↻ Refresh QR Code</button>
      </form>
    </div>
  </div>
</body>
</html>`, qrBase64)

	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
}

