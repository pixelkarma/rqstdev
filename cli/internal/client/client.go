package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type Account struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	Role string `json:"role"`
}

type VM struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	State        string `json:"state"`
	GuestWebPort int    `json:"guestWebPort"`
	HostSSHPort  int    `json:"hostSSHPort"`
	HostWebPort  int    `json:"hostWebPort"`
	SSHReady     bool   `json:"sshReady"`
	WebReady     bool   `json:"webReady"`
	LastError    string `json:"lastError,omitempty"`
}

type SSHResolution struct {
	VM  VM `json:"vm"`
	SSH struct {
		Host            string `json:"host"`
		Port            int    `json:"port"`
		DefaultUsername string `json:"defaultUsername"`
		Ready           bool   `json:"ready"`
	} `json:"ssh"`
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:   strings.TrimSpace(token),
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) ListAccounts() ([]Account, error) {
	var resp struct {
		Accounts []Account `json:"accounts"`
	}
	if err := c.doJSON(http.MethodGet, "/v1/accounts", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Accounts, nil
}

func (c *Client) CreateAccount(name string) (Account, error) {
	var resp struct {
		Account Account `json:"account"`
	}
	if err := c.doJSON(http.MethodPost, "/v1/accounts", map[string]string{"name": name}, &resp); err != nil {
		return Account{}, err
	}
	return resp.Account, nil
}

func (c *Client) ListVMs(accountUUID string) ([]VM, error) {
	var resp struct {
		VMs []VM `json:"vms"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/vms", url.PathEscape(strings.TrimSpace(accountUUID)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.VMs, nil
}

func (c *Client) ResolveVM(accountUUID, vmName string) (SSHResolution, error) {
	var resp SSHResolution
	path := fmt.Sprintf("/v1/accounts/%s/resolve-vm/%s", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return SSHResolution{}, err
	}
	return resp, nil
}

func (c *Client) PoweroffVM(accountUUID, vmName string) (VM, error) {
	return c.vmAction(accountUUID, vmName, "poweroff")
}

func (c *Client) KillVM(accountUUID, vmName string) (VM, error) {
	return c.vmAction(accountUUID, vmName, "kill")
}

func (c *Client) PoweronVM(accountUUID, vmName string) (VM, error) {
	return c.vmAction(accountUUID, vmName, "poweron")
}

func RunSSH(resolution SSHResolution, username string) error {
	targetUser := strings.TrimSpace(username)
	if targetUser == "" {
		targetUser = resolution.SSH.DefaultUsername
	}
	target := resolution.SSH.Host
	if targetUser != "" {
		target = targetUser + "@" + target
	}
	cmd := exec.Command(
		"ssh",
		"-o", "StrictHostKeyChecking=accept-new",
		"-p", strconv.Itoa(resolution.SSH.Port),
		target,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *Client) vmAction(accountUUID, vmName, action string) (VM, error) {
	var resp struct {
		VM VM `json:"vm"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/vms/%s/%s", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)), action)
	if err := c.doJSON(http.MethodPost, path, map[string]any{}, &resp); err != nil {
		return VM{}, err
	}
	return resp.VM, nil
}

func (c *Client) doJSON(method, path string, reqBody any, out any) error {
	var body io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode >= 400 {
		var apiErr apiError
		if err := json.NewDecoder(res.Body).Decode(&apiErr); err == nil && apiErr.Error.Code != "" {
			return fmt.Errorf("%s: %s", apiErr.Error.Code, apiErr.Error.Message)
		}
		return fmt.Errorf("unexpected HTTP status %d", res.StatusCode)
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
