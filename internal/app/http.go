package app

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]any{"error": message})
}
func decode(r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return errors.New("only one JSON value allowed")
	}
	return nil
}
func (a *App) authorized(r *http.Request) bool {
	raw := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || raw == "" {
		return false
	}
	for _, secret := range a.Cfg.ServiceKeys {
		if subtle.ConstantTimeCompare([]byte(raw), []byte(secret)) == 1 {
			return true
		}
	}
	return false
}
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"ok": true, "status": "ok"})
	})
	mux.HandleFunc("GET /api/admin/status", func(w http.ResponseWriter, r *http.Request) {
		a.mu.RLock()
		configured := a.admin != nil
		a.mu.RUnlock()
		respond(w, 200, map[string]any{"configured": configured})
	})
	mux.HandleFunc("POST /api/admin/setup", func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if decode(r, &input) != nil {
			fail(w, 400, "Invalid request")
			return
		}
		if err := a.createAdmin(input.Username, input.Password); err != nil {
			if err.Error() == "administrator already configured" {
				fail(w, 409, err.Error())
			} else if !validUsername(input.Username) || len(input.Password) < 12 || len(input.Password) > 256 {
				fail(w, 400, "Username must be 3-64 letters/numbers and password 12-256 characters")
			} else {
				fail(w, 500, "Could not save administrator")
			}
			return
		}
		respond(w, 201, map[string]any{"configured": true})
	})
	mux.HandleFunc("POST /api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			fail(w, 403, "Origin not allowed")
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if decode(r, &input) != nil || len(input.Password) > 256 {
			fail(w, 400, "Invalid request")
			return
		}
		a.mu.RLock()
		configured := a.admin != nil
		a.mu.RUnlock()
		if !configured {
			fail(w, 503, "Administrator not configured")
			return
		}
		ip := clientIP(r)
		if !a.beginLoginAttempt(ip) {
			fail(w, 429, "Too many login attempts; try again later")
			return
		}
		if !a.checkAdmin(input.Username, input.Password) {
			fail(w, 401, "Invalid username or password")
			return
		}
		a.loginSucceeded(ip)
		if err := a.newAdminSession(w, r); err != nil {
			fail(w, 500, "Could not create session")
			return
		}
		respond(w, 200, map[string]any{"username": input.Username})
	})
	mux.HandleFunc("GET /api/admin/me", func(w http.ResponseWriter, r *http.Request) {
		if !a.adminAuthenticated(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		a.mu.RLock()
		name := a.admin.Username
		a.mu.RUnlock()
		respond(w, 200, map[string]any{"username": name})
	})
	mux.HandleFunc("POST /api/admin/logout", func(w http.ResponseWriter, r *http.Request) {
		if !a.adminAuthenticated(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		if !sameOrigin(r) {
			fail(w, 403, "Origin not allowed")
			return
		}
		a.endAdminSession(w, r)
		respond(w, 200, map[string]any{"success": true})
	})
	mux.HandleFunc("GET /api/admin/monitor", func(w http.ResponseWriter, r *http.Request) {
		if !a.adminAuthenticated(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		a.mu.RLock()
		state := a.monitor
		a.mu.RUnlock()
		respond(w, 200, map[string]any{"telegramConfigured": a.Cfg.TelegramBotToken != "", "status": state.Status, "reason": state.Reason, "lastChecked": state.LastChecked, "intervalSeconds": int(a.Cfg.SessionCheckInterval.Seconds())})
	})
	mux.HandleFunc("POST /api/admin/notifications/test", func(w http.ResponseWriter, r *http.Request) {
		if !a.adminAuthenticated(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		if !sameOrigin(r) {
			fail(w, 403, "Origin not allowed")
			return
		}
		if a.Cfg.TelegramBotToken == "" {
			fail(w, 503, "Telegram is not configured")
			return
		}
		if err := a.sendTelegram(r.Context(), "🔔 Kaspi POS: тестовое уведомление. Мониторинг подключен."); err != nil {
			fail(w, 502, err.Error())
			return
		}
		respond(w, 200, map[string]any{"success": true})
	})
	api := func(method, path string, session bool, fn func(http.ResponseWriter, *http.Request, *KaspiSession)) {
		mux.HandleFunc(method+" "+path, func(w http.ResponseWriter, r *http.Request) {
			if !a.authorized(r) && !a.adminAuthenticated(r) {
				fail(w, 401, "Unauthorized")
				return
			}
			if method != "GET" && !a.authorized(r) && !sameOrigin(r) {
				fail(w, 403, "Origin not allowed")
				return
			}
			var s *KaspiSession
			if session {
				s = a.currentSession()
				if s == nil {
					fail(w, 401, "Kaspi session required")
					return
				}
			}
			fn(w, r, s)
		})
	}
	call := func(w http.ResponseWriter, result map[string]any, err error) {
		if err != nil {
			if errors.Is(err, errLoginFlow) {
				fail(w, 400, err.Error())
				return
			}
			if errors.Is(err, errTooManyFlows) {
				fail(w, 429, err.Error())
				return
			}
			fail(w, 502, err.Error())
			return
		}
		respond(w, 200, result)
	}
	sessionStatus := func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		s := a.currentSession()
		if s == nil {
			respond(w, 200, map[string]any{"authenticated": false})
			return
		}
		respond(w, 200, map[string]any{"authenticated": true, "orgName": s.OrgName, "phone": s.Phone, "organizationId": s.OrganizationID})
	}
	api("GET", "/api/auth/session", false, sessionStatus)
	api("POST", "/api/auth/session", false, sessionStatus)
	api("POST", "/api/auth/logout", false, func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		if err := a.clearSession(); err != nil {
			fail(w, 500, "Could not clear session")
			return
		}
		respond(w, 200, map[string]any{"success": true})
	})
	api("POST", "/api/auth/init", false, func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		result, err := a.initLogin(r.Context())
		call(w, result, err)
	})
	api("POST", "/api/auth/send-phone", false, func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		var x struct {
			ProcessID string `json:"processId"`
			Phone     string `json:"phoneNumber"`
		}
		if decode(r, &x) != nil || x.ProcessID == "" || !clientPhone(x.Phone) {
			fail(w, 400, "valid processId and phoneNumber required")
			return
		}
		result, err := a.sendPhone(r.Context(), x.ProcessID, x.Phone)
		call(w, result, err)
	})
	api("POST", "/api/auth/verify-otp", false, func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		var x struct {
			ProcessID string `json:"processId"`
			OTP       string `json:"otp"`
		}
		if decode(r, &x) != nil || x.ProcessID == "" || len(x.OTP) < 4 {
			fail(w, 400, "valid processId and otp required")
			return
		}
		result, err := a.verifyOTP(r.Context(), x.ProcessID, x.OTP)
		call(w, result, err)
	})
	api("GET", "/api/session/check", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		active, result, err := a.checkSession(r.Context(), s)
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		if !active {
			respond(w, 401, map[string]any{"active": false, "error": "Kaspi rejected the session", "details": result})
			return
		}
		respond(w, 200, map[string]any{"active": true, "orgName": s.OrgName, "phone": s.Phone})
	})
	api("GET", "/api/payments/active", false, func(w http.ResponseWriter, r *http.Request, _ *KaspiSession) {
		respond(w, 200, map[string]any{"items": a.activePayments()})
	})
	api("GET", "/api/invoice/client-info", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		if !clientPhone(r.URL.Query().Get("phoneNumber")) {
			fail(w, 400, "valid phoneNumber required")
			return
		}
		result, err := a.clientInfo(r.Context(), s, r.URL.Query().Get("phoneNumber"))
		call(w, result, err)
	})
	api("POST", "/api/invoice/create", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			Phone   string      `json:"phoneNumber"`
			Amount  amountValue `json:"amount"`
			Comment string      `json:"comment"`
		}
		if decode(r, &x) != nil || !clientPhone(x.Phone) || !validAmount(float64(x.Amount)) {
			fail(w, 400, "valid phoneNumber and positive amount required")
			return
		}
		result, err := a.createInvoice(r.Context(), s, x.Phone, float64(x.Amount), x.Comment)
		call(w, result, err)
	})
	api("GET", "/api/invoice/details", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		if !safeID(r.URL.Query().Get("operationId")) {
			fail(w, 400, "operationId required")
			return
		}
		result, err := a.invoiceDetails(r.Context(), s, r.URL.Query().Get("operationId"))
		call(w, result, err)
	})
	api("POST", "/api/invoice/cancel", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			ID identifier `json:"operationId"`
		}
		if decode(r, &x) != nil || !safeID(string(x.ID)) {
			fail(w, 400, "operationId required")
			return
		}
		result, err := a.cancelInvoice(r.Context(), s, string(x.ID))
		call(w, result, err)
	})
	api("POST", "/api/invoice/history", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		result, err := a.invoiceHistory(r.Context(), s)
		call(w, result, err)
	})
	api("POST", "/api/qr/create", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			Amount    amountValue `json:"amount"`
			Latitude  float64     `json:"latitude"`
			Longitude float64     `json:"longitude"`
		}
		if decode(r, &x) != nil || !validAmount(float64(x.Amount)) {
			fail(w, 400, "positive amount required")
			return
		}
		result, err := a.createQR(r.Context(), s, float64(x.Amount), x.Latitude, x.Longitude)
		call(w, result, err)
	})
	api("GET", "/api/qr/status", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		if !safeID(r.URL.Query().Get("qrOperationId")) {
			fail(w, 400, "qrOperationId required")
			return
		}
		result, err := a.qrStatus(r.Context(), s, r.URL.Query().Get("qrOperationId"))
		call(w, result, err)
	})
	api("POST", "/api/history/operations", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			EndDate  string `json:"endDate"`
			LastDate string `json:"lastTransactionDate"`
			Period   int    `json:"statementPeriodCode"`
		}
		if decode(r, &x) != nil || x.EndDate == "" {
			fail(w, 400, "endDate required")
			return
		}
		result, err := a.operations(r.Context(), s, x.EndDate, x.LastDate, x.Period)
		call(w, result, err)
	})
	api("POST", "/api/history/details", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			ID     identifier `json:"id"`
			Method int        `json:"operationMethod"`
		}
		if decode(r, &x) != nil || !safeID(string(x.ID)) {
			fail(w, 400, "id required")
			return
		}
		result, err := a.operationDetails(r.Context(), s, string(x.ID), x.Method)
		call(w, result, err)
	})
	api("POST", "/api/refund/create", true, func(w http.ResponseWriter, r *http.Request, s *KaspiSession) {
		var x struct {
			ID     identifier  `json:"qrOperationId"`
			Amount amountValue `json:"returnAmount"`
		}
		if decode(r, &x) != nil || !safeID(string(x.ID)) || !validAmount(float64(x.Amount)) {
			fail(w, 400, "valid qrOperationId and positive returnAmount required")
			return
		}
		result, err := a.refund(r.Context(), s, string(x.ID), float64(x.Amount))
		call(w, result, err)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if !a.authorized(r) && !a.adminAuthenticated(r) {
			fail(w, 401, "Unauthorized")
			return
		}
		fail(w, 404, "Unknown API route")
	})
	dist := http.Dir(a.Cfg.FrontendDir)
	files := http.FileServer(dist)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			http.Error(w, "Method not allowed", 405)
			return
		}
		if r.URL.Path == "/" {
			http.ServeFile(w, r, filepath.Join(a.Cfg.FrontendDir, "index.html"))
			return
		}
		if strings.Contains(filepath.Clean(r.URL.Path), "..") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	return mux
}
