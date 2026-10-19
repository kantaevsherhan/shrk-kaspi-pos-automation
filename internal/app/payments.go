package app

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

func validAmount(n float64) bool {
	return !math.IsNaN(n) && !math.IsInf(n, 0) && n > 0 && n <= 1000000000
}
func (a *App) createInvoice(ctx context.Context, s *KaspiSession, phone string, amount float64, comment string) (map[string]any, error) {
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
		a.track(&TrackedPayment{ID: id, Type: "invoice", Status: status, Amount: amount, ReceiptURL: stringValue(data["ReceiptUrl"]), OrderNumber: stringValue(data["OrderNumber"]), Created: time.Now()})
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
		a.track(&TrackedPayment{ID: id, Type: "qr", Status: status, Amount: amount, QRToken: stringValue(data["QrToken"]), ReceiptURL: stringValue(data["ReceiptUrl"]), ExpireDate: stringValue(data["ExpireDate"]), Created: time.Now()})
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
func (a *App) invoiceHistory(ctx context.Context, s *KaspiSession) (map[string]any, error) {
	return a.qrRequest(ctx, "POST", "/v01/remote/history", s, map[string]any{"MaxResult": 20})
}
func (a *App) operations(ctx context.Context, s *KaspiSession, endDate, lastDate string, period int) (map[string]any, error) {
	if endDate == "" {
		return nil, errors.New("endDate required")
	}
	return a.qrRequest(ctx, "POST", "/v02/history/operations", s, map[string]any{"EndDate": endDate, "LastTransactionDate": lastDate, "StatementPeriodCode": period})
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
	var n int64
	_, err := fmt.Sscan(raw, &n)
	if err != nil {
		return raw
	}
	return n
}
func (a *App) checkSession(ctx context.Context, s *KaspiSession) (bool, map[string]any, error) {
	result, err := a.operations(ctx, s, time.Now().In(time.FixedZone("KZT", 5*3600)).Format("2006-01-02")+"T23:59:59.000+0500", "", 0)
	if err != nil {
		return false, nil, err
	}
	return kaspiOK(result), result, nil
}
