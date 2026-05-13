package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultConfigPath = "/etc/rqstdev/config.json"

type Config struct {
	ListenAddr               string `json:"listen_addr"`
	BaseURL                  string `json:"base_url"`
	BaseDomain               string `json:"base_domain"`
	DBPath                   string `json:"db_path"`
	DataDir                  string `json:"data_dir"`
	VMsDir                   string `json:"vms_dir"`
	QEMUBinaryPath           string `json:"qemu_binary_path"`
	EmailScriptPath          string `json:"email_script_path"`
	DefaultWebPort           int    `json:"default_web_port"`
	DefaultSSHUser           string `json:"default_ssh_user"`
	TemplatesBaseDir         string `json:"templates_base_dir"`
	DefaultTemplateName      string `json:"default_template_name"`
	DefaultTemplateImagePath string `json:"default_template_image_path"`
}

func FlagPath() string {
	path := flag.String("config", defaultConfigPath, "path to server config")
	flag.Parse()
	return strings.TrimSpace(*path)
}

func Load(path string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read config %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %q: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := validate(cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		cfg.ListenAddr = ":8090"
	}
	if cfg.DefaultWebPort == 0 {
		cfg.DefaultWebPort = 80
	}
	if strings.TrimSpace(cfg.DefaultSSHUser) == "" {
		cfg.DefaultSSHUser = "root"
	}
	if strings.TrimSpace(cfg.QEMUBinaryPath) == "" {
		cfg.QEMUBinaryPath = "qemu-system-x86_64"
	}
	if strings.TrimSpace(cfg.VMsDir) == "" && strings.TrimSpace(cfg.DataDir) != "" {
		cfg.VMsDir = filepath.Join(cfg.DataDir, "vms")
	}
	if strings.TrimSpace(cfg.DefaultTemplateName) == "" {
		cfg.DefaultTemplateName = "default"
	}
	if strings.TrimSpace(cfg.DefaultTemplateImagePath) == "" && strings.TrimSpace(cfg.TemplatesBaseDir) != "" {
		cfg.DefaultTemplateImagePath = filepath.Join(cfg.TemplatesBaseDir, "default-disk.qcow2")
	}
}

func validate(cfg Config) error {
	switch {
	case strings.TrimSpace(cfg.BaseURL) == "":
		return fmt.Errorf("base_url is required")
	case strings.TrimSpace(cfg.BaseDomain) == "":
		return fmt.Errorf("base_domain is required")
	case strings.TrimSpace(cfg.DBPath) == "":
		return fmt.Errorf("db_path is required")
	case strings.TrimSpace(cfg.DataDir) == "":
		return fmt.Errorf("data_dir is required")
	case strings.TrimSpace(cfg.VMsDir) == "":
		return fmt.Errorf("vms_dir is required")
	case strings.TrimSpace(cfg.DefaultTemplateImagePath) == "":
		return fmt.Errorf("default_template_image_path is required")
	}

	cfg.DBPath = filepath.Clean(cfg.DBPath)
	cfg.DataDir = filepath.Clean(cfg.DataDir)
	cfg.VMsDir = filepath.Clean(cfg.VMsDir)

	return nil
}
