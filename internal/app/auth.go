package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var errLoginFlow = errors.New("unknown, completed or expired processId; start login again")
var errTooManyFlows = errors.New("too many active SMS logins")

func (c *Config) entranceCookie(token string) string {
	d := c.Device
	p := c.App
	cookie := fmt.Sprintf("deviceId=%s; installId=%s; is_mobile_app=true; locale=%s; ma_bld=%s; ma_platform_type=%s; ma_platform_ver=%s; ma_ver=%s; pk=%s; pkTag=%s; xs=R:0|E:0|RH:0|N:0", d.DeviceID, d.InstallID, p.Locale, p.Build, p.Platform, p.PlatformVer, p.Version, d.PK, d.PKTag)
	if token != "" {
		cookie += "; user_token=" + token
	}
	return cookie
}
func (c *Config) entranceHeaders(referer, token string) http.Header {
	h := http.Header{}
	values := map[string]string{"Accept": "application/json, text/plain, */*", "Content-Type": "application/json", "Accept-Language": "ru", "Origin": c.EntranceURL, "Sec-Fetch-Site": "same-origin", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty", "User-Agent": fmt.Sprintf("Mozilla/5.0 (iPhone; CPU iPhone OS %s like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148", strings.ReplaceAll(c.App.PlatformVer, ".", "_")), "Referer": referer, "Cookie": c.entranceCookie(token)}
	for k, v := range values {
		h.Set(k, v)
	}
	return h
}
func getUserToken(resp *http.Response) string {
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "user_token" {
			return cookie.Value
		}
	}
	return ""
}

func (a *App) initLogin(ctx context.Context) (map[string]any, error) {
	a.mu.Lock()
	for id, flow := range a.flows {
		if time.Since(flow.Created) > 10*time.Minute {
			delete(a.flows, id)
		}
	}
	count := len(a.flows) + a.authStarts
	if count >= 100 {
		a.mu.Unlock()
		return nil, errTooManyFlows
	}
	a.authStarts++
	a.mu.Unlock()
	defer func() { a.mu.Lock(); a.authStarts--; a.mu.Unlock() }()
	c := a.Cfg
	p := c.App
	d := c.Device
	referer := fmt.Sprintf("%s/process/entrance/?auth=2&appBuild=%s&appVersion=%s&platformVersion=%s&platformType=IOS&deviceBrand=%s&deviceModel=%s&deviceId=%s&installId=%s&frontCameraAvailable=true&sf=registration&pc=KPEntrance&noPass=0", c.EntranceURL, p.Build, p.Version, p.PlatformVer, p.Brand, p.Model, d.DeviceID, d.InstallID)
	// Kaspi binds Data/data case-insensitively. Go sorts map keys, so sending
	// both would put the empty lowercase field last and erase the launch data.
	payload := map[string]any{"data": map[string]any{"auth": "2", "appBuild": p.Build, "appVersion": p.Version, "platformVersion": p.PlatformVer, "platformType": "IOS", "deviceBrand": p.Brand, "deviceModel": p.Model, "deviceId": d.DeviceID, "installId": d.InstallID, "frontCameraAvailable": "true", "sf": "registration", "pc": "KPEntrance", "noPass": "0"}, "actType": "Success"}
	body, _ := json.Marshal(payload)
	result, resp, err := a.kaspiRequest(ctx, "POST", c.EntranceURL+"/api/v1/entrance/step", c.entranceHeaders(referer, ""), body)
	if err != nil {
		return nil, err
	}
	processID := stringValue(field(result, "meta", "pId"))
	if processID != "" {
		a.mu.Lock()
		a.flows[processID] = &authFlow{ProcessID: processID, UserToken: getUserToken(resp), Created: time.Now(), Step: stringValue(field(result, "meta", "sn")), View: stringValue(field(result, "view", "code"))}
		a.mu.Unlock()
	}
	return map[string]any{"success": processID != "", "processId": processID, "view": field(result, "view", "code"), "body": result}, nil
}
func (a *App) getFlow(id string) (*authFlow, error) {
	a.mu.RLock()
	flow := a.flows[id]
	a.mu.RUnlock()
	if flow == nil || time.Since(flow.Created) > 10*time.Minute {
		return nil, errLoginFlow
	}
	return flow, nil
}
func (a *App) authStep(ctx context.Context, flow *authFlow, data map[string]any, sn string) (map[string]any, error) {
	c := a.Cfg
	referer := c.EntranceURL + "/process/universal-enter-phone-number?pId=" + flow.ProcessID + "&firstPage=KPUniversalEnterPhoneNumber"
	if flow.View == "KPEnterLoginPassword" {
		referer = c.EntranceURL + "/process/enter-login-password?pId=" + flow.ProcessID + "&firstPage=KPUniversalEnterPhoneNumber"
	}
	payload := map[string]any{"meta": map[string]any{"pId": flow.ProcessID, "sn": sn}, "data": data, "actType": "Success"}
	body, _ := json.Marshal(payload)
	result, resp, err := a.kaspiRequest(ctx, "POST", c.EntranceURL+"/api/v1/entrance/step", c.entranceHeaders(referer, flow.UserToken), body)
	if err != nil {
		return nil, err
	}
	if token := getUserToken(resp); token != "" {
		flow.UserToken = token
	}
	if step := stringValue(field(result, "meta", "sn")); step != "" {
		flow.Step = step
	}
	if processID := stringValue(field(result, "meta", "pId")); processID != "" {
		flow.ProcessID = processID
	}
	if view := stringValue(field(result, "view", "code")); view != "" {
		flow.View = view
	}
	if main := stringValue(field(result, "view", "main")); main != "" {
		flow.View = main
	}
	return result, nil
}
func (a *App) sendPhone(ctx context.Context, id, phone string) (map[string]any, error) {
	phone = smsPhone(phone)
	if phone == "" {
		return nil, errors.New("valid phoneNumber required: 10 digits or a full +7/8 number")
	}
	flow, err := a.getFlow(id)
	if err != nil {
		return nil, err
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.Finished {
		return nil, errLoginFlow
	}
	flow.Phone = "7" + phone
	result, err := a.authStep(ctx, flow, map[string]any{"phoneNumber": phone}, "EnterPhoneNumber")
	if err != nil {
		return nil, err
	}
	return a.authProgress(ctx, id, flow, result)
}

func (a *App) authProgress(ctx context.Context, id string, flow *authFlow, result map[string]any) (map[string]any, error) {
	reason := obj(field(result, "view", "onOpenAlarm", "error"))
	if len(reason) == 0 {
		reason = obj(result["error"])
	}
	if len(reason) == 0 {
		reason = obj(field(result, "data", "error"))
	}
	message := stringValue(reason["label"])
	if message == "" {
		message = stringValue(reason["desc"])
	}
	if message == "" {
		message = stringValue(reason["description"])
	}
	if message == "" {
		message = stringValue(field(result, "data", "desc"))
	}
	response := map[string]any{"success": false, "processId": id, "desc": stringValue(field(result, "data", "desc")), "view": field(result, "view", "code"), "errorCode": stringValue(reason["code"]), "errorMessage": message, "smsSent": false}
	registration := stringValue(field(result, "data", "type")) == "kpDeviceRegistration" || stringValue(field(result, "view", "code")) == "KPMobileCall"
	if len(reason) > 0 || result["actType"] == "Alarm" || (result["isClosed"] == true && !registration) {
		if message == "" {
			response["errorMessage"] = "Kaspi отклонил шаг входа. Попробуйте начать подключение заново."
		}
		return response, nil
	}
	view := stringValue(field(result, "view", "code"))
	step := stringValue(field(result, "meta", "sn"))
	if registration {
		connected, err := a.finishLogin(ctx, flow)
		if err != nil {
			return nil, err
		}
		flow.Finished = true
		a.mu.Lock()
		delete(a.flows, id)
		a.mu.Unlock()
		return map[string]any{"success": true, "processId": id, "step": "finished", "nextStep": "connected", "profileId": connected.ProfileID, "organizationId": connected.OrganizationID, "orgName": connected.OrgName, "phone": connected.Phone}, nil
	}
	main := field(result, "view", "main")
	if view == "EnterOtp" || step == "ViewEnterOtp" || (main != nil && main != false && main != "") {
		response["success"], response["nextStep"], response["smsSent"] = true, "otp", true
		return response, nil
	}
	if view == "KPEnterLoginPassword" || step == "ViewEnterLoginPassword" {
		response["success"], response["nextStep"] = true, "password"
		return response, nil
	}
	response["nextStep"] = "unsupported"
	if message == "" {
		response["errorMessage"] = "Kaspi запросил другой шаг входа: " + view
	}
	return response, nil
}

func (a *App) verifyKaspiPassword(ctx context.Context, id, password string) (map[string]any, error) {
	flow, err := a.getFlow(id)
	if err != nil {
		return nil, err
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	passwordRequested := flow.Step == "ViewEnterLoginPassword" || (flow.Step == "" && flow.View == "KPEnterLoginPassword")
	if flow.Finished || !passwordRequested {
		return nil, fmt.Errorf("%w: Kaspi has not requested a password", errLoginFlow)
	}
	result, err := a.authStep(ctx, flow, map[string]any{"password": password}, "ViewEnterLoginPassword")
	if err != nil {
		return nil, err
	}
	response, err := a.authProgress(ctx, id, flow, result)
	// Provider responses must never echo the submitted password to a caller.
	for key, value := range response {
		if text, ok := value.(string); ok && password != "" {
			response[key] = strings.ReplaceAll(text, password, "[скрыто]")
		}
	}
	return response, err
}
func (a *App) verifyOTP(ctx context.Context, id, otp string) (map[string]any, error) {
	flow, err := a.getFlow(id)
	if err != nil {
		return nil, err
	}
	flow.mu.Lock()
	defer flow.mu.Unlock()
	if flow.Finished {
		return nil, errLoginFlow
	}
	result, err := a.authStep(ctx, flow, map[string]any{"userOtp": otp, "inputType": "auto"}, "ViewEnterOtp")
	if err != nil {
		return nil, err
	}
	return a.authProgress(ctx, id, flow, result)
}
func (a *App) finishLogin(ctx context.Context, flow *authFlow) (*KaspiSession, error) {
	c := a.Cfg
	p := c.App
	d := c.Device
	ecdhKey, x509B64, err := ecdhStart()
	if err != nil {
		return nil, err
	}
	signedData := map[string]any{"installId": d.InstallID, "time": nowISO(), "auth": []any{map[string]any{"value": "", "type": "pincode"}}, "userIdHash": ""}
	signedJSON, _ := json.Marshal(signedData)
	signedB64 := base64.StdEncoding.EncodeToString(signedJSON)
	signed, err := ecSign(c.PrivateKey, []byte(signedB64))
	if err != nil {
		return nil, err
	}
	finishURL := c.EntranceURL + "/api/v1/kpentrance/finish"
	xsh := "url,X-Time-Zone,X-Request-ID,X-Net-Type,X-Emulator,X-Call,X-Platform-Type,X-Locale,X-Time,X-SV"
	headers := map[string]string{"Content-Type": "application/json", "Accept": "*/*", "Accept-Language": "ru", "User-Agent": c.nativeUA(), "X-Time": nowISO(), "X-Call": "notConnected", "X-Platform-Type": p.Platform, "X-PkTag": d.PKTag, "X-SU": md5Hex(strings.ToLower(finishURL)), "X-Net-Type": "WIFI/ETHERNET", "X-Emulator": "0", "X-Locale": p.Locale, "X-SV": "2", "X-Request-ID": uuid(), "X-Time-Zone": "GMT+05:00", "X-SH": xsh}
	payload := map[string]any{"signed": map[string]any{"sign": signed, "data": signedB64}, "guard": map[string]any{"pinHash": d.PinHash, "x509": x509B64}, "processId": flow.ProcessID}
	body, _ := json.Marshal(payload)
	headers["X-Sign"], err = xSign(c.PrivateKey, finishURL, headers, xsh, string(body))
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	result, _, err := a.kaspiRequest(ctx, "POST", finishURL, h, body)
	if err != nil {
		return nil, err
	}
	if result["success"] != true {
		return nil, errors.New("Kaspi finish rejected the login")
	}
	tokenSN := stringValue(field(result, "data", "tokenSN"))
	remoteX509 := stringValue(field(result, "data", "x509"))
	if tokenSN == "" || remoteX509 == "" {
		return nil, errors.New("Kaspi did not return a secure session")
	}
	secret, err := ecdhFinish(ecdhKey, remoteX509)
	if err != nil {
		return nil, err
	}
	session := &KaspiSession{TokenSN: tokenSN, Secret: secret, Phone: flow.Phone}
	if err := a.fetchOrganization(ctx, session); err != nil {
		return nil, err
	}
	if err := a.saveSession(session); err != nil {
		return nil, err
	}
	return session, nil
}
func (a *App) fetchOrganization(ctx context.Context, s *KaspiSession) error {
	c := a.Cfg
	p := c.App
	d := c.Device
	url := c.MTokenURL + "/v08/organizations/org-context-otp"
	xsh := "url,X-Kb-Client-Ip,X-Time,X-App-Ver,X-SV,X-Locale,X-App-Bld,X-Install-ID,X-Kb-TokenSn,X-S,X-Kb-TokenSnMac,X-Call"
	headers := map[string]string{"Content-Type": "application/json", "Accept": "*/*", "Accept-Language": "ru", "User-Agent": c.nativeUA(), "X-Kb-TokenSn": s.TokenSN, "X-Kb-TokenSnMac": tokenMac(s.TokenSN, s.Secret, time.Now()), "X-Install-ID": d.InstallID, "X-App-Ver": p.Version, "X-App-Bld": p.Build, "X-Locale": p.Locale, "X-Call": "notConnected", "X-Time": nowISO(), "X-S": "R:0|E:0|RH:0|N:0", "X-SV": "2", "X-Kb-Client-Ip": "192.168.1.96", "X-PkTag": d.PKTag, "X-SU": md5Hex(strings.ToLower(url)), "X-SH": xsh, "X-Request-ID": uuid()}
	info := map[string]any{"SdkVersion": "AOTP service", "DeviceId": d.DeviceID, "ApplicationId": "kz.kaspi.business", "ScreenWidth": p.ScreenW, "Model": p.Model, "ScreenHeight": p.ScreenH, "DeviceName": p.DeviceName, "VersionName": p.Version, "BuildRelease": p.Platform + " " + p.PlatformVer, "Brand": p.Brand, "Board": p.PlatformVer, "Platform": p.Platform, "Product": "Kaspi Pay", "frontCameraAvailable": true, "VersionCode": p.Build, "InstallId": d.InstallID}
	body, _ := json.Marshal(map[string]any{"DeviceInformation": info, "OrganizationId": 0})
	sign, err := xSign(c.PrivateKey, url, headers, xsh, string(body))
	if err != nil {
		return err
	}
	headers["X-Sign"] = sign
	h := http.Header{}
	for k, v := range headers {
		h.Set(k, v)
	}
	result, _, err := a.kaspiRequest(ctx, "POST", url, h, body)
	if err != nil {
		return err
	}
	current := obj(field(result, "Data", "Current"))
	s.ProfileID = stringValue(current["ProfileId"])
	if s.ProfileID == "" {
		return errors.New("Kaspi did not return an organization profile")
	}
	s.OrgName = stringValue(current["OrganizationName"])
	s.OrganizationID = stringValue(current["OrganizationId"])
	if phone := stringValue(field(result, "Data", "PhoneNumber")); phone != "" {
		s.Phone = phone
	}
	return nil
}
