package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type State struct {
	ActiveAccount string `json:"active_account"`
}

func Load() (State, error) {
	path, err := path()
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read session state: %w", err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("parse session state: %w", err)
	}
	return state, nil
}

func Save(state State) error {
	path, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	return nil
}

func Clear() error {
	path, err := path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove session state: %w", err)
	}
	return nil
}

func path() (string, error) {
	tempDir := filepath.Join(os.TempDir(), "rqstdev")
	return filepath.Join(tempDir, "session-"+sessionID()+".json"), nil
}

func sessionID() string {
	if value := strings.TrimSpace(os.Getenv("RQSTDEV_SESSION_ID")); value != "" {
		return shortHash(value)
	}
	tty := currentTTY()
	if tty != "" {
		return shortHash(tty)
	}
	return shortHash(fmt.Sprintf("%d", os.Getppid()))
}

func currentTTY() string {
	command := exec.Command("tty")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(output))
	if tty == "" || tty == "not a tty" {
		return ""
	}
	return tty
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}
