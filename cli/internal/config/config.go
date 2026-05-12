package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultBaseURL = "https://api.rqst.dev"

type AccountRef struct {
	Alias   string `json:"alias"`
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

type Config struct {
	BaseURL        string                `json:"base_url"`
	LastEmail      string                `json:"last_email,omitempty"`
	DefaultAccount string                `json:"default_account,omitempty"`
	Accounts       map[string]AccountRef `json:"accounts,omitempty"`
}

func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Config{BaseURL: defaultBaseURL, Accounts: map[string]AccountRef{}}
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(&cfg)
	return cfg, nil
}

func Save(cfg Config) error {
	applyDefaults(&cfg)
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}

func Path() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config dir: %w", err)
	}
	return filepath.Join(root, "rqstdev", "config.json"), nil
}

func (c *Config) UpsertAccount(ref AccountRef) {
	applyDefaults(c)
	alias := strings.TrimSpace(ref.Alias)
	if alias == "" {
		alias = strings.TrimSpace(ref.Name)
	}
	alias = uniqueAlias(c.Accounts, alias, ref.UUID)
	ref.Alias = alias
	if strings.TrimSpace(ref.BaseURL) == "" {
		ref.BaseURL = c.BaseURL
	}
	c.Accounts[alias] = ref
}

func (c *Config) RemoveAccount(identifier string) bool {
	applyDefaults(c)
	alias, _, ok := c.ResolveLocalAccount(identifier)
	if !ok {
		return false
	}
	delete(c.Accounts, alias)
	if c.DefaultAccount == alias {
		c.DefaultAccount = ""
	}
	return true
}

func (c Config) ResolveLocalAccount(identifier string) (alias string, ref AccountRef, ok bool) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", AccountRef{}, false
	}
	for key, account := range c.Accounts {
		if key == identifier || account.UUID == identifier || strings.EqualFold(account.Name, identifier) {
			return key, account, true
		}
	}
	return "", AccountRef{}, false
}

func (c Config) SortedAccounts() []AccountRef {
	list := make([]AccountRef, 0, len(c.Accounts))
	for _, account := range c.Accounts {
		list = append(list, account)
	}
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(list[i].Alias) < strings.ToLower(list[j].Alias)
	})
	return list
}

func applyDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Accounts == nil {
		cfg.Accounts = map[string]AccountRef{}
	}
}

func uniqueAlias(existing map[string]AccountRef, preferred, uuid string) string {
	alias := strings.TrimSpace(preferred)
	if alias == "" {
		alias = uuid
	}
	if current, ok := existing[alias]; !ok || current.UUID == uuid {
		return alias
	}
	for index := 2; ; index++ {
		candidate := fmt.Sprintf("%s-%d", alias, index)
		if current, ok := existing[candidate]; !ok || current.UUID == uuid {
			return candidate
		}
	}
}
