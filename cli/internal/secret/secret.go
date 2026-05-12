package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const serviceName = "rqstdev"

type fallbackStore map[string]string

func LoadToken(baseURL string) (string, error) {
	key := tokenKey(baseURL)
	if token, err := keyring.Get(serviceName, key); err == nil && strings.TrimSpace(token) != "" {
		return token, nil
	}
	fallback, err := loadFallback()
	if err != nil {
		return "", err
	}
	encrypted := strings.TrimSpace(fallback[key])
	if encrypted == "" {
		return "", nil
	}
	return decrypt(encrypted)
}

func SaveToken(baseURL, token string) error {
	key := tokenKey(baseURL)
	if err := keyring.Set(serviceName, key, token); err == nil {
		return nil
	}
	fallback, err := loadFallback()
	if err != nil {
		return err
	}
	encrypted, err := encrypt(token)
	if err != nil {
		return err
	}
	fallback[key] = encrypted
	return saveFallback(fallback)
}

func DeleteToken(baseURL string) error {
	key := tokenKey(baseURL)
	_ = keyring.Delete(serviceName, key)
	fallback, err := loadFallback()
	if err != nil {
		return err
	}
	delete(fallback, key)
	return saveFallback(fallback)
}

func tokenKey(baseURL string) string {
	return strings.TrimSpace(baseURL)
}

func fallbackPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config dir: %w", err)
	}
	return filepath.Join(root, "rqstdev", "credentials.json"), nil
}

func loadFallback() (fallbackStore, error) {
	path, err := fallbackPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fallbackStore{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read fallback credentials: %w", err)
	}
	var store fallbackStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("parse fallback credentials: %w", err)
	}
	return store, nil
}

func saveFallback(store fallbackStore) error {
	path, err := fallbackPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create credentials dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fallback credentials: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write fallback credentials: %w", err)
	}
	return nil
}

func encrypt(plain string) (string, error) {
	block, err := aes.NewCipher(machineKey())
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func decrypt(value string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", fmt.Errorf("decode encrypted token: %w", err)
	}
	block, err := aes.NewCipher(machineKey())
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted token is invalid")
	}
	nonce := raw[:gcm.NonceSize()]
	ciphertext := raw[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt token: %w", err)
	}
	return string(plain), nil
}

func machineKey() []byte {
	currentUser, _ := user.Current()
	username := ""
	if currentUser != nil {
		username = currentUser.Username
	}
	hostname, _ := os.Hostname()
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"rqstdev",
		hostname,
		username,
		runtimeFingerprint(),
	}, "|")))
	return sum[:]
}

func runtimeFingerprint() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if data, err := os.ReadFile(path); err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value
			}
		}
	}
	return "fallback"
}
