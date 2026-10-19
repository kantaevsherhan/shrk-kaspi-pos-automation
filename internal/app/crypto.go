package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const ocraSuite = "OCRA-1:HOTP-SHA256-6:QH64-T1M"

func md5Hex(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
func uuid() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
func nowISO() string { return time.Now().Format("2006-01-02T15:04:05.000-0700") }

func encrypt(key, plain []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(append(nonce, aead.Seal(nil, nonce, plain, nil)...)), nil
}
func decrypt(key []byte, encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	return aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], nil)
}

func tokenMac(token string, secret []byte, at time.Time) string {
	if len(secret) == 0 {
		return "000000"
	}
	q := make([]byte, 128)
	tokenBytes := []byte(token)
	copy(q, tokenBytes[:min(len(tokenBytes), 32)])
	data := append([]byte(ocraSuite), 0)
	data = append(data, q...)
	t := make([]byte, 8)
	binary.BigEndian.PutUint64(t, uint64(at.Unix()/30))
	data = append(data, t...)
	mac := hmac.New(sha256.New, secret)
	mac.Write(data)
	hash := mac.Sum(nil)
	off := int(hash[len(hash)-1] & 0x0f)
	code := (uint32(hash[off]&0x7f) << 24) | (uint32(hash[off+1]) << 16) | (uint32(hash[off+2]) << 8) | uint32(hash[off+3])
	return fmt.Sprintf("%06d", code%1000000)
}

func ecSign(key *ecdsa.PrivateKey, data []byte) (string, error) {
	hash := sha256.Sum256(data)
	sig, err := ecdsa.SignASN1(rand.Reader, key, hash[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}
func xSign(key *ecdsa.PrivateKey, url string, headers map[string]string, xsh, body string) (string, error) {
	lines := []string{}
	for _, name := range strings.Split(xsh, ",") {
		value := headers[name]
		if name == "url" {
			value = strings.ToLower(url)
		}
		lines = append(lines, strings.ToLower(name)+":"+value)
	}
	text := strings.Join(lines, "\n")
	if body != "" {
		text += "\n" + body
	}
	hash := sha256.Sum256([]byte(text))
	return ecSign(key, hash[:])
}

func ecdhStart() (*ecdh.PrivateKey, string, error) {
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	der, err := x509.MarshalPKIXPublicKey(key.PublicKey())
	if err != nil {
		return nil, "", err
	}
	return key, base64.StdEncoding.EncodeToString(der), nil
}
func ecdhFinish(key *ecdh.PrivateKey, remote string) ([]byte, error) {
	der, err := base64.StdEncoding.DecodeString(remote)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	switch p := pub.(type) {
	case *ecdh.PublicKey:
		return key.ECDH(p)
	case *ecdsa.PublicKey:
		ep, err := p.ECDH()
		if err != nil {
			return nil, err
		}
		return key.ECDH(ep)
	default:
		return nil, errors.New("Kaspi x509 is not an EC public key")
	}
}

func (c *Config) signedHeaders(url string, s *KaspiSession, body string) (http.Header, error) {
	xsh := "url,X-Install-ID,X-PI,X-App-Bld,X-Platform-Ver,X-Locale,X-App-Ver,X-Device-ID,X-SV,X-Time,X-Platform-Type,X-Call,X-Kb-TokenSnMac,X-Kb-TokenSn"
	values := map[string]string{"X-Kb-TokenSn": s.TokenSN, "X-Kb-TokenSnMac": tokenMac(s.TokenSN, s.Secret, time.Now()), "X-PI": s.ProfileID, "X-Install-ID": c.Device.InstallID, "X-Device-ID": c.Device.DeviceID, "X-App-Ver": c.App.Version, "X-App-Bld": c.App.Build, "X-Platform-Type": c.App.Platform, "X-Platform-Ver": c.App.PlatformVer, "X-Locale": c.App.Locale, "X-Time": nowISO(), "X-Request-ID": uuid(), "X-Call": "notConnected", "X-SV": "2", "X-SH": xsh, "User-Agent": c.nativeUA(), "Accept": "*/*", "Accept-Language": "ru"}
	sign, err := xSign(c.PrivateKey, url, values, xsh, body)
	if err != nil {
		return nil, err
	}
	values["X-Sign"] = sign
	h := http.Header{}
	for k, v := range values {
		h.Set(k, v)
	}
	if body != "" {
		h.Set("Content-Type", "application/json")
	}
	return h, nil
}
func (c *Config) nativeUA() string {
	return "Kaspi%20Pay/" + c.App.Build + " " + c.App.CFNetwork + " " + c.App.Darwin
}
