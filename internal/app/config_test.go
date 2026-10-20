package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeviceIdentityMatchesOriginalAndSurvivesRestart(t *testing.T) {
	first := &Config{StateDir: t.TempDir()}
	if err := first.loadIdentity(); err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&first.PrivateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	// Node used the final 65 SPKI bytes as the uncompressed P-256 point.
	point := base64.StdEncoding.EncodeToString(der[len(der)-65:])
	if first.Device.PK != point || first.Device.PKTag != md5Hex(point) {
		t.Fatal("device public key format differs from Node")
	}
	second := &Config{StateDir: first.StateDir}
	if err := second.loadIdentity(); err != nil || second.Device != first.Device {
		t.Fatalf("device changed after restart: %v", err)
	}
	wrong, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	wrongDER, _ := x509.MarshalPKCS8PrivateKey(wrong)
	if err := writeJSON(filepath.Join(first.StateDir, "keypair.json"), map[string]string{"privateKey": base64.StdEncoding.EncodeToString(wrongDER)}); err != nil {
		t.Fatal(err)
	}
	if err := second.loadIdentity(); err == nil {
		t.Fatal("unsupported EC curve accepted")
	}
}

func TestConfigurationRejectsInvalidSecretsAndMonitorSettings(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"short encryption key", "TOKEN_SECRET_KEY", "12"},
		{"short API key", "SERVICE_API_KEYS", `{"service":"short"}`},
		{"duplicate API key", "SERVICE_API_KEYS", `{"one":"abcdefghijklmnopqrstuvwxyz1234567890","two":"abcdefghijklmnopqrstuvwxyz1234567890"}`},
		{"partial Telegram config", "TELEGRAM_BOT_TOKEN", "test-token"},
		{"short monitor interval", "SESSION_CHECK_INTERVAL", "1s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TOKEN_SECRET_KEY", strings.Repeat("11", 32))
			t.Setenv("SERVICE_API_KEYS", `{"service":"abcdefghijklmnopqrstuvwxyz1234567890"}`)
			t.Setenv("STATE_DIR", t.TempDir())
			t.Setenv("TELEGRAM_BOT_TOKEN", "")
			t.Setenv("TELEGRAM_CHAT_ID", "")
			t.Setenv("SESSION_CHECK_INTERVAL", "2m")
			t.Setenv(tc.key, tc.value)
			if _, err := LoadConfig(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
