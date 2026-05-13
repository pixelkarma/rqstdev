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

type AccountMember struct {
	UserUUID string `json:"userUUID"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

type Invite struct {
	UUID        string `json:"uuid"`
	AccountUUID string `json:"accountUUID"`
	AccountName string `json:"accountName"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
}

type Template struct {
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	DefaultCPU      int    `json:"defaultCPU"`
	DefaultMemoryMB int    `json:"defaultMemoryMB"`
}

type VM struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	State        string `json:"state"`
	TemplateName string `json:"templateName"`
	GuestWebPort int    `json:"guestWebPort"`
	CPUCount     int    `json:"cpuCount"`
	MemoryMB     int    `json:"memoryMB"`
	SSHReady     bool   `json:"sshReady"`
	LastError    string `json:"lastError,omitempty"`
}

type PublishedPort struct {
	PublicPort int    `json:"publicPort"`
	GuestPort  int    `json:"guestPort"`
	Protocol   string `json:"protocol"`
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

type AuthResult struct {
	Token     string
	User      User
	Challenge *Challenge
}

type User struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`
}

type Challenge struct {
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
	Email   string `json:"email"`
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type Error struct {
	Code    string
	Message string
	Status  int
}

func (e Error) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
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

func (c *Client) AccountDetails(accountUUID string) (Account, []AccountMember, error) {
	var resp struct {
		Account struct {
			UUID    string          `json:"uuid"`
			Name    string          `json:"name"`
			Role    string          `json:"role"`
			Members []AccountMember `json:"members"`
		} `json:"account"`
	}
	path := fmt.Sprintf("/v1/accounts/%s", url.PathEscape(strings.TrimSpace(accountUUID)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return Account{}, nil, err
	}
	return Account{UUID: resp.Account.UUID, Name: resp.Account.Name, Role: resp.Account.Role}, resp.Account.Members, nil
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

func (c *Client) Signup(email, password, accountName string) (AuthResult, error) {
	return c.authJSON("/v1/auth/signup", map[string]string{
		"email":       email,
		"password":    password,
		"accountName": accountName,
	})
}

func (c *Client) Login(email, password string) (AuthResult, error) {
	return c.authJSON("/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
}

func (c *Client) VerifyChallenge(email, purpose, code string) (AuthResult, error) {
	return c.authJSON("/v1/auth/challenge/verify", map[string]string{
		"email":   email,
		"purpose": purpose,
		"code":    code,
	})
}

func (c *Client) Forgot(email string) error {
	return c.doJSON(http.MethodPost, "/v1/auth/forgot", map[string]string{"email": email}, nil)
}

func (c *Client) Reset(email, code, newPassword string) error {
	return c.doJSON(http.MethodPost, "/v1/auth/reset", map[string]string{
		"email":       email,
		"code":        code,
		"newPassword": newPassword,
	}, nil)
}

func (c *Client) Logout() error {
	return c.doJSON(http.MethodPost, "/v1/auth/logout", map[string]any{}, nil)
}

func (c *Client) Me() (User, []Account, error) {
	var resp struct {
		User     User      `json:"user"`
		Accounts []Account `json:"accounts"`
	}
	if err := c.doJSON(http.MethodGet, "/v1/me", nil, &resp); err != nil {
		return User{}, nil, err
	}
	return resp.User, resp.Accounts, nil
}

func (c *Client) ListInvites() ([]Invite, error) {
	var resp struct {
		Invites []Invite `json:"invites"`
	}
	if err := c.doJSON(http.MethodGet, "/v1/invites", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Invites, nil
}

func (c *Client) AcceptInvite(inviteUUID string) error {
	path := fmt.Sprintf("/v1/invites/%s/accept", url.PathEscape(strings.TrimSpace(inviteUUID)))
	return c.doJSON(http.MethodPost, path, map[string]any{}, nil)
}

func (c *Client) RefuseInvite(inviteUUID string) error {
	path := fmt.Sprintf("/v1/invites/%s/refuse", url.PathEscape(strings.TrimSpace(inviteUUID)))
	return c.doJSON(http.MethodPost, path, map[string]any{}, nil)
}

func (c *Client) InviteUser(accountUUID, email, role string) (Invite, error) {
	var resp struct {
		Invite Invite `json:"invite"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/invites", url.PathEscape(strings.TrimSpace(accountUUID)))
	if err := c.doJSON(http.MethodPost, path, map[string]string{"email": email, "role": role}, &resp); err != nil {
		return Invite{}, err
	}
	return resp.Invite, nil
}

func (c *Client) RevokeUser(accountUUID, userUUID string) error {
	path := fmt.Sprintf("/v1/accounts/%s/users/%s/revoke", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(userUUID)))
	return c.doJSON(http.MethodPost, path, map[string]any{}, nil)
}

func (c *Client) TransferAccount(accountUUID, email string) error {
	path := fmt.Sprintf("/v1/accounts/%s/transfer", url.PathEscape(strings.TrimSpace(accountUUID)))
	return c.doJSON(http.MethodPost, path, map[string]string{"email": email}, nil)
}

func (c *Client) ListTemplates(accountUUID string) ([]Template, error) {
	var resp struct {
		Templates []Template `json:"templates"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/templates", url.PathEscape(strings.TrimSpace(accountUUID)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Templates, nil
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

func (c *Client) CreateVM(accountUUID, name, templateUUID string, guestWebPort int) (VM, error) {
	var resp struct {
		VM VM `json:"vm"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/vms", url.PathEscape(strings.TrimSpace(accountUUID)))
	req := map[string]any{
		"name":         name,
		"templateUUID": templateUUID,
		"guestWebPort": guestWebPort,
	}
	if err := c.doJSON(http.MethodPost, path, req, &resp); err != nil {
		return VM{}, err
	}
	return resp.VM, nil
}

func (c *Client) DeleteVM(accountUUID, vmName string) error {
	path := fmt.Sprintf("/v1/accounts/%s/vms/%s", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)))
	return c.doJSON(http.MethodDelete, path, nil, nil)
}

func (c *Client) ResolveVM(accountUUID, vmName string) (SSHResolution, error) {
	var resp SSHResolution
	path := fmt.Sprintf("/v1/accounts/%s/resolve-vm/%s", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return SSHResolution{}, err
	}
	return resp, nil
}

func (c *Client) ListPublishedPorts(accountUUID, vmName string) ([]PublishedPort, error) {
	var resp struct {
		Ports []PublishedPort `json:"ports"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/vms/%s/ports", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)))
	if err := c.doJSON(http.MethodGet, path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Ports, nil
}

func (c *Client) AddPublishedPort(accountUUID, vmName string, publicPort, guestPort int) (PublishedPort, error) {
	var resp struct {
		Port PublishedPort `json:"port"`
	}
	path := fmt.Sprintf("/v1/accounts/%s/vms/%s/ports", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)))
	req := map[string]int{"publicPort": publicPort, "guestPort": guestPort}
	if err := c.doJSON(http.MethodPost, path, req, &resp); err != nil {
		return PublishedPort{}, err
	}
	return resp.Port, nil
}

func (c *Client) RemovePublishedPort(accountUUID, vmName string, publicPort int) error {
	path := fmt.Sprintf("/v1/accounts/%s/vms/%s/ports/%d", url.PathEscape(strings.TrimSpace(accountUUID)), url.PathEscape(strings.TrimSpace(vmName)), publicPort)
	return c.doJSON(http.MethodDelete, path, nil, nil)
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

func (c *Client) authJSON(path string, reqBody any) (AuthResult, error) {
	var resp struct {
		Token     string     `json:"token"`
		User      User       `json:"user"`
		Challenge *Challenge `json:"challenge"`
	}
	if err := c.doJSON(http.MethodPost, path, reqBody, &resp); err != nil {
		return AuthResult{}, err
	}
	return AuthResult{
		Token:     strings.TrimSpace(resp.Token),
		User:      resp.User,
		Challenge: resp.Challenge,
	}, nil
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
			return Error{Code: apiErr.Error.Code, Message: apiErr.Error.Message, Status: res.StatusCode}
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
