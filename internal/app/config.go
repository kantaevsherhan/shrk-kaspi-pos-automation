package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Host, Port, StateDir, FrontendDir string
	EntranceURL, MTokenURL, QRPayURL  string
	TokenKey                          []byte
	ServiceKeys                       map[string]string
	App                               AppInfo
	Device                            DeviceInfo
	PrivateKey                        *ecdsa.PrivateKey
}

type AppInfo struct{ Version, Build, Platform, PlatformVer, Locale, Model, Brand, DeviceName, ScreenW, ScreenH, CFNetwork, Darwin string }
type DeviceInfo struct{ DeviceID, InstallID, PinHash, PK, PKTag, X509 string }

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func loadDotEnv() error {
	b, err := os.ReadFile(".env")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	return nil
}

func LoadConfig() (*Config, error) {
	if err := loadDotEnv(); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(os.Getenv("TOKEN_SECRET_KEY"))
	if err != nil || len(key) != 32 {
		return nil, errors.New("TOKEN_SECRET_KEY must be 64 hexadecimal characters")
	}
	keys := map[string]string{}
	if err := json.Unmarshal([]byte(os.Getenv("SERVICE_API_KEYS")), &keys); err != nil || len(keys) == 0 {
		return nil, errors.New("SERVICE_API_KEYS must be a JSON object with named keys")
	}
	seen := map[string]bool{}
	for name, secret := range keys {
		if name == "" || len(secret) < 32 || strings.HasPrefix(secret, "replace-") || seen[secret] {
			return nil, fmt.Errorf("invalid SERVICE_API_KEYS entry: %s", name)
		}
		seen[secret] = true
	}
	state := env("STATE_DIR", "data")
	if err := os.MkdirAll(state, 0700); err != nil {
		return nil, err
	}
	if os.Getenv("STATE_DIR") == "" {
		if err := migrateRootState(".", state); err != nil {
			return nil, err
		}
	}
	c := &Config{Host: env("HOST", "127.0.0.1"), Port: env("PORT", "3000"), StateDir: state, FrontendDir: env("FRONTEND_DIR", "web/dist"), TokenKey: key, ServiceKeys: keys,
		EntranceURL: env("KASPI_ENTRANCE_URL", "https://entrance-pay.kaspi.kz"), MTokenURL: env("KASPI_MTOKEN_URL", "https://mtoken.kaspi.kz"), QRPayURL: env("KASPI_QRPAY_URL", "https://qrpay.kaspi.kz"),
		App: AppInfo{Version: env("APP_VERSION", "26.0921"), Build: env("APP_BUILD", "1115"), Platform: env("APP_PLATFORM", "iOS"), PlatformVer: env("APP_PLATFORM_VER", "18.4"), Locale: env("APP_LOCALE", "ru-RU"), Model: env("APP_MODEL", "iPhone16,2"), Brand: env("APP_BRAND", "Apple"), DeviceName: env("APP_DEVICE_NAME", "iPhone"), ScreenW: env("APP_SCREEN_W", "430.0"), ScreenH: env("APP_SCREEN_H", "932.0"), CFNetwork: env("APP_CFNETWORK", "CFNetwork/3826.400.120"), Darwin: env("APP_DARWIN", "Darwin/24.4.0")}}
	if err := c.loadIdentity(); err != nil {
		return nil, err
	}
	return c, nil
}

func migrateRootState(root, state string) error {
	for _, name := range []string{"keypair.json", "device.json", "tracked-payments.json", "webhook-retries.json", "webhooks.json"} {
		target := filepath.Join(state, name)
		if _, err := os.Stat(target); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err = os.WriteFile(target, data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp := path + ".tmp"
	if err = os.WriteFile(temp, b, 0600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func (c *Config) loadIdentity() error {
	keyPath := filepath.Join(c.StateDir, "keypair.json")
	var saved struct {
		PrivateKey string `json:"privateKey"`
		PublicKey  string `json:"publicKey"`
	}
	if b, err := os.ReadFile(keyPath); err == nil {
		if err = json.Unmarshal(b, &saved); err != nil {
			return err
		}
		der, err := base64.StdEncoding.DecodeString(saved.PrivateKey)
		if err != nil {
			return err
		}
		pk, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return err
		}
		var ok bool
		c.PrivateKey, ok = pk.(*ecdsa.PrivateKey)
		if !ok {
			return errors.New("keypair.json is not ECDSA")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		c.PrivateKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		privateDER, err := x509.MarshalPKCS8PrivateKey(c.PrivateKey)
		if err != nil {
			return err
		}
		publicDER, err := x509.MarshalPKIXPublicKey(&c.PrivateKey.PublicKey)
		if err != nil {
			return err
		}
		saved.PrivateKey = base64.StdEncoding.EncodeToString(privateDER)
		saved.PublicKey = base64.StdEncoding.EncodeToString(publicDER)
		if err := writeJSON(keyPath, saved); err != nil {
			return err
		}
	} else {
		return err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&c.PrivateKey.PublicKey)
	if err != nil {
		return err
	}
	point := elliptic.Marshal(elliptic.P256(), c.PrivateKey.PublicKey.X, c.PrivateKey.PublicKey.Y)
	pk := base64.StdEncoding.EncodeToString(point)
	tag := md5.Sum([]byte(pk))
	devicePath := filepath.Join(c.StateDir, "device.json")
	var device struct {
		DeviceID  string `json:"deviceId"`
		InstallID string `json:"installId"`
		PinHash   string `json:"pinHash"`
	}
	if b, err := os.ReadFile(devicePath); err == nil {
		if err = json.Unmarshal(b, &device); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		device.DeviceID = uuid()
		device.InstallID = uuid()
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		sum := md5.Sum(bytes)
		device.PinHash = hex.EncodeToString(sum[:])
		if err := writeJSON(devicePath, device); err != nil {
			return err
		}
	} else {
		return err
	}
	c.Device = DeviceInfo{DeviceID: device.DeviceID, InstallID: device.InstallID, PinHash: device.PinHash, PK: pk, PKTag: hex.EncodeToString(tag[:]), X509: base64.StdEncoding.EncodeToString(publicDER)}
	return nil
}
