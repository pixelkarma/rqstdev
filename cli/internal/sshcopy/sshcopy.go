package sshcopy

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/crypto/ssh"

	"rqstdev/cli/internal/client"
)

type PublicKey struct {
	Label string
	Path  string
}

func DiscoverPublicKeys() ([]PublicKey, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("find home directory: %w", err)
	}
	dir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read ssh directory: %w", err)
	}

	var keys []PublicKey
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".pub") {
			continue
		}
		keys = append(keys, PublicKey{
			Label: name,
			Path:  filepath.Join(dir, name),
		})
	}
	sort.Slice(keys, func(i, j int) bool {
		return keyRank(keys[i].Label) < keyRank(keys[j].Label)
	})
	return keys, nil
}

func ReadPublicKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read public key: %w", err)
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("public key file is empty")
	}
	return value, nil
}

func InstallPublicKey(resolution client.SSHResolution, username, password, publicKey string) error {
	targetUser := strings.TrimSpace(username)
	if targetUser == "" {
		targetUser = resolution.SSH.DefaultUsername
	}
	if targetUser == "" {
		targetUser = "root"
	}

	config := &ssh.ClientConfig{
		User:            targetUser,
		Auth:            []ssh.AuthMethod{ssh.Password(password)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	address := fmt.Sprintf("%s:%d", resolution.SSH.Host, resolution.SSH.Port)
	conn, err := ssh.Dial("tcp", address, config)
	if err != nil {
		return fmt.Errorf("connect over ssh: %w", err)
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	encoded := base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(publicKey)))
	command := fmt.Sprintf(
		"set -eu; umask 077; mkdir -p ~/.ssh; touch ~/.ssh/authorized_keys; key=$(printf '%%s' %s | base64 -d); grep -Fqx -- \"$key\" ~/.ssh/authorized_keys || printf '%%s\\n' \"$key\" >> ~/.ssh/authorized_keys; chmod 700 ~/.ssh; chmod 600 ~/.ssh/authorized_keys",
		shellQuote(encoded),
	)
	output, err := session.CombinedOutput(command)
	if err != nil {
		if strings.TrimSpace(string(output)) != "" {
			return fmt.Errorf("install public key: %w: %s", err, strings.TrimSpace(string(output)))
		}
		return fmt.Errorf("install public key: %w", err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func keyRank(name string) string {
	order := map[string]string{
		"id_ed25519.pub": "00",
		"id_rsa.pub":     "01",
		"id_ecdsa.pub":   "02",
		"id_dsa.pub":     "03",
	}
	if rank, ok := order[strings.ToLower(name)]; ok {
		return rank + ":" + strings.ToLower(name)
	}
	return "99:" + strings.ToLower(name)
}
