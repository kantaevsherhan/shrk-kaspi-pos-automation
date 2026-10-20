package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

func validAmount(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 && n <= 1000000000
}
func (a *App) createInvoice(ctx context.Context, s *KaspiSession, phone string, amount float64, comment string) (map[string]any, error) {
	phone = normalizePhone(phone)
	if !clientPhone(phone) || !validAmount(amount) {
		return nil, errors.New("valid phoneNumber and positive amount required")
	}
	result, err := a.qrRequest(ctx, "POST", "/v01/remote/create", s, map[string]any{"PhoneNumber": phone, "Amount": amount, "Comment": comment})
	if err != nil {
		return nil, err
	}
	data := obj(result["Data"])
	id := stringValue(data["QrOperationId"])
	if id == "" {
		id = stringValue(data["Id"])
	}
	if kaspiOK(result) && id != "" {
		status := stringValue(data["Status"])
		if status == "" {
			status = "RemotePaymentCreated"
		}
		a.track(&TrackedPayment{ID: id, Type: "invoice", Status: status, Amount: amount, ReceiptURL: stringValue(data["ReceiptUrl"]), OrderNumber: stringValue(data["OrderNumber"]), Created: time.Now(), ProfileID: s.ProfileID})
	}
	return result, nil
}
func (a *App) createQR(ctx context.Context, s *KaspiSession, amount, lat, lon float64) (map[string]any, error) {
	if !validAmount(amount) {
		return nil, errors.New("positive amount required")
	}
	if lat == 0 {
		lat = 43.204643483375889
	}
	if lon == 0 {
		lon = 76.891962364115912
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return nil, errors.New("invalid coordinates")
	}
	result, err := a.qrRequest(ctx, "POST", "/v01/qr-token/create", s, map[string]any{"PaymentAmount": amount, "DeviceInterface": "Pos", "Latitude": lat, "Longitude": lon})
	if err != nil {
		return nil, err
	}
	data := obj(result["Data"])
	id := stringValue(data["QrOperationId"])
	if kaspiOK(result) && id != "" {
		status := stringValue(data["Status"])
		if status == "" {
			status = "QrTokenCreated"
		}
		a.track(&TrackedPayment{ID: id, Type: "qr", Status: status, Amount: amount, QRToken: stringValue(data["QrToken"]), ReceiptURL: stringValue(data["ReceiptUrl"]), ExpireDate: stringValue(data["ExpireDate"]), Created: time.Now(), ProfileID: s.ProfileID})
	}
	if token := stringValue(data["QrToken"]); token != "" {
		data["QrOriginalToken"] = token
		data["QrToken"] = normalURL(token)
	}
	return result, nil
}
func (a *App) invoiceDetails(ctx context.Context, s *KaspiSession, id string) (map[string]any, error) {
	if !safeID(id) {
		return nil, errors.New("operationId required")
	}
	return a.qrRequest(ctx, "GET", query("/v02/remote/details", "operationId", id), s, nil)
}
func (a *App) qrStatus(ctx context.Context, s *KaspiSession, id string) (map[string]any, error) {
	if !safeID(id) {
		return nil, errors.New("qrOperationId required")
	}
	return a.qrRequest(ctx, "GET", query("/v02/kaspi-qr/status", "qrOperationId", id), s, nil)
}
func (a *App) clientInfo(ctx context.Context, s *KaspiSession, phone string) (map[string]any, error) {
	phone = normalizePhone(phone)
	if !clientPhone(phone) {
		return nil, errors.New("valid phoneNumber required")
	}
	return a.qrRequest(ctx, "GET", query("/v01/remote/client-info", "phoneNumber", phone), s, nil)
}
func (a *App) cancelInvoice(ctx context.Context, s *KaspiSession, id string) (map[string]any, error) {
	if !safeID(id) {
		return nil, errors.New("operationId required")
	}
	return a.qrRequest(ctx, "POST", "/v01/remote/cancel", s, map[string]any{"qrOperationId": jsonNumber(id)})
}
func (a *App) invoiceHistory(ctx context.Context, s *KaspiSession, maxResults ...int) (map[string]any, error) {
	limit := 20
	if len(maxResults) > 0 {
		limit = maxResults[0]
	}
	if limit < 1 || limit > 1000 {
		return nil, errors.New("maxResult must be between 1 and 1000")
	}
	return a.qrRequest(ctx, "POST", "/v01/remote/history", s, map[string]any{"MaxResult": limit})
}

type operationFilter struct {
	StartDate   string `json:"startDate"`
	EndDate     string `json:"endDate"`
	LastDate    string `json:"lastTransactionDate"`
	Period      int    `json:"statementPeriodCode"`
	PageSize    *int   `json:"pageSize"`
	PaymentType *int   `json:"paymentType"`
}

func (a *App) operations(ctx context.Context, s *KaspiSession, endDate, lastDate string, period int) (map[string]any, error) {
	return a.filteredOperations(ctx, s, operationFilter{EndDate: endDate, LastDate: lastDate, Period: period})
}
func (a *App) filteredOperations(ctx context.Context, s *KaspiSession, filter operationFilter) (map[string]any, error) {
	if filter.EndDate == "" {
		return nil, errors.New("endDate required")
	}
	if filter.PageSize != nil && (*filter.PageSize < 1 || *filter.PageSize > 1000) {
		return nil, errors.New("pageSize must be between 1 and 1000")
	}
	body := map[string]any{"EndDate": filter.EndDate, "LastTransactionDate": filter.LastDate, "StatementPeriodCode": filter.Period}
	if filter.StartDate != "" {
		body["StartDate"] = filter.StartDate
	}
	if filter.PageSize != nil {
		body["PageSize"] = *filter.PageSize
	}
	if filter.PaymentType != nil {
		body["PaymentType"] = *filter.PaymentType
	}
	return a.qrRequest(ctx, "POST", "/v02/history/operations", s, body)
}
func (a *App) operationDetails(ctx context.Context, s *KaspiSession, id string, method int) (map[string]any, error) {
	if !safeID(id) {
		return nil, errors.New("id required")
	}
	return a.qrRequest(ctx, "POST", "/v01/kaspi-qr/operations/details", s, map[string]any{"Id": jsonNumber(id), "OperationMethod": method})
}
func (a *App) refund(ctx context.Context, s *KaspiSession, id string, amount float64) (map[string]any, error) {
	if !safeID(id) || !validAmount(amount) {
		return nil, errors.New("valid qrOperationId and positive returnAmount required")
	}
	return a.qrRequest(ctx, "POST", "/v01/kaspi-qr/history-pos-return", s, map[string]any{"ReturnAmount": amount, "QrOperationId": jsonNumber(id), "DeviceInterface": "Pos"})
}
func jsonNumber(raw string) any {
	value := strings.TrimLeft(raw, "0")
	if value == "" {
		value = "0"
	}
	return json.Number(value)
}
func (a *App) checkSession(ctx context.Context, s *KaspiSession) (bool, map[string]any, error) {
	// The working Kaspi integration probes remote history, never client-info:
	// client lookup can succeed even when invoices cannot be created.
	result, err := a.invoiceHistory(ctx, s, 1)
	if err != nil {
		return false, nil, err
	}
	return kaspiOK(result), result, nil
}
