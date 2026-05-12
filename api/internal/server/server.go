package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rqstdev/api/internal/config"
	"rqstdev/api/internal/store"
	"rqstdev/api/internal/vmruntime"
)

type Server struct {
	http  *http.Server
	cfg   config.Config
	store *store.Store
	vmrt  vmruntime.Runtime
}

type statusResponse struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type authHandler struct {
	cfg   config.Config
	store *store.Store
	vmrt  vmruntime.Runtime
}

type signupRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	AccountName string `json:"accountName"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type createAccountRequest struct {
	Name string `json:"name"`
}

type createVMRequest struct {
	Name         string `json:"name"`
	TemplateUUID string `json:"templateUUID"`
	GuestWebPort int    `json:"guestWebPort"`
}

type sshResolveResponse struct {
	VM  store.VM `json:"vm"`
	SSH struct {
		Host            string `json:"host"`
		Port            int    `json:"port"`
		DefaultUsername string `json:"defaultUsername"`
		Ready           bool   `json:"ready"`
	} `json:"ssh"`
}

type authResponse struct {
	Token string       `json:"token"`
	User  userResponse `json:"user"`
}

type userResponse struct {
	UUID  string `json:"uuid"`
	Email string `json:"email"`
}

type meResponse struct {
	User     userResponse              `json:"user"`
	Accounts []store.AccountMembership `json:"accounts"`
}

func New(cfg config.Config, logger *log.Logger, st *store.Store) *Server {
	mux := http.NewServeMux()

	srv := &Server{
		cfg:   cfg,
		store: st,
		vmrt:  vmruntime.New(cfg),
	}
	registerRoutes(mux, srv)

	srv.http = &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           loggingMiddleware(logger, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	return srv
}

func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func registerRoutes(mux *http.ServeMux, srv *Server) {
	auth := authHandler{cfg: srv.cfg, store: srv.store, vmrt: srv.vmrt}

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, statusResponse{
			Name:    "rqstdev-api",
			Version: "dev",
			Status:  "ok",
		})
	})

	mux.HandleFunc("/v1/me", method(http.MethodGet, auth.handleMe))
	mux.HandleFunc("/v1/auth/signup", method(http.MethodPost, auth.handleSignup))
	mux.HandleFunc("/v1/auth/login", method(http.MethodPost, auth.handleLogin))
	mux.HandleFunc("/v1/auth/challenge/verify", method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		writeNotImplemented(w, "challenge_verify")
	}))
	mux.HandleFunc("/v1/auth/forgot", method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		writeNotImplemented(w, "forgot")
	}))
	mux.HandleFunc("/v1/auth/reset", method(http.MethodPost, func(w http.ResponseWriter, r *http.Request) {
		writeNotImplemented(w, "reset")
	}))
	mux.HandleFunc("/v1/auth/logout", method(http.MethodPost, auth.handleLogout))

	mux.HandleFunc("/v1/accounts", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			auth.handleAccountsList(w, r)
		case http.MethodPost:
			auth.handleAccountCreate(w, r)
		default:
			writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
		}
	})
	mux.HandleFunc("/v1/accounts/", auth.handleAccountSubroutes)

	mux.HandleFunc("/v1/invites", method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		writeNotImplemented(w, "invites_list")
	}))

	mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Route not found.")
	})
}

func (h authHandler) handleSignup(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(h.cfg.EmailScriptPath) != "" {
		writeError(w, http.StatusNotImplemented, "two_factor_required", "Email challenge flow is not implemented yet.")
		return
	}

	var req signupRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	user, token, err := h.store.CreateUserWithAccount(r.Context(), store.SignupParams{
		Email:       req.Email,
		Password:    req.Password,
		AccountName: req.AccountName,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, "conflict", "A user with that email already exists.")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create user.")
		}
		return
	}

	writeJSON(w, http.StatusCreated, authResponse{
		Token: token,
		User: userResponse{
			UUID:  user.UUID,
			Email: user.Email,
		},
	})
}

func (h authHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(h.cfg.EmailScriptPath) != "" {
		writeError(w, http.StatusNotImplemented, "two_factor_required", "Email challenge flow is not implemented yet.")
		return
	}

	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	user, err := h.store.Authenticate(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidCredentials):
			writeError(w, http.StatusUnauthorized, "invalid_credentials", "Invalid email or password.")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to authenticate user.")
		}
		return
	}

	token, err := h.store.CreateSessionToken(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create session.")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		Token: token,
		User: userResponse{
			UUID:  user.UUID,
			Email: user.Email,
		},
	})
}

func (h authHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, err := bearerToken(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "auth_required", "Authentication is required.")
		return
	}

	if err := h.store.RevokeToken(r.Context(), token); err != nil {
		if errors.Is(err, store.ErrTokenInvalid) {
			writeError(w, http.StatusUnauthorized, "token_invalid", "Token is invalid.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to logout.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h authHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	user, err := h.currentUser(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	accounts, err := h.store.AccountsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load account memberships.")
		return
	}

	writeJSON(w, http.StatusOK, meResponse{
		User: userResponse{
			UUID:  user.UUID,
			Email: user.Email,
		},
		Accounts: accounts,
	})
}

func (h authHandler) handleAccountsList(w http.ResponseWriter, r *http.Request) {
	user, err := h.currentUser(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	accounts, err := h.store.AccountsForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load accounts.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

func (h authHandler) handleAccountCreate(w http.ResponseWriter, r *http.Request) {
	user, err := h.currentUser(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	var req createAccountRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}

	account, err := h.store.CreateAccountForUser(r.Context(), store.CreateAccountParams{
		UserID: user.ID,
		Name:   req.Name,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create account.")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"account": account})
}

func (h authHandler) handleAccountSubroutes(w http.ResponseWriter, r *http.Request) {
	user, err := h.currentUser(r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	accountUUID, tail, ok := splitAccountPath(r.URL.Path)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "Route not found.")
		return
	}

	scope, err := h.store.AccountScopeForUser(r.Context(), user.ID, accountUUID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusForbidden, "forbidden", "Account access denied.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load account scope.")
		return
	}

	switch {
	case tail == "/templates" && r.Method == http.MethodGet:
		h.handleTemplatesList(w, r, scope)
	case tail == "/vms" && r.Method == http.MethodGet:
		h.handleVMsList(w, r, scope)
	case tail == "/vms" && r.Method == http.MethodPost:
		h.handleVMCreate(w, r, scope)
	case strings.HasPrefix(tail, "/vms/"):
		h.handleVMSubroutes(w, r, scope, strings.TrimPrefix(tail, "/vms/"))
	case strings.HasPrefix(tail, "/resolve-vm/") && r.Method == http.MethodGet:
		h.handleVMResolve(w, r, scope, strings.TrimPrefix(tail, "/resolve-vm/"))
	default:
		writeError(w, http.StatusNotFound, "not_found", "Route not found.")
	}
}

func (h authHandler) handleTemplatesList(w http.ResponseWriter, r *http.Request, scope store.AccountScope) {
	templates, err := h.store.VisibleTemplatesForAccount(r.Context(), scope.AccountID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load templates.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
}

func (h authHandler) handleVMsList(w http.ResponseWriter, r *http.Request, scope store.AccountScope) {
	vms, err := h.store.VMsForAccount(r.Context(), scope.AccountID, scope.AccountUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load VMs.")
		return
	}
	for index := range vms {
		h.refreshVMObservation(&vms[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"vms": vms})
}

func (h authHandler) handleVMCreate(w http.ResponseWriter, r *http.Request, scope store.AccountScope) {
	if scope.Role != "owner" && scope.Role != "admin" {
		writeError(w, http.StatusForbidden, "forbidden", "Only admins and owners can create VMs.")
		return
	}

	var req createVMRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.TemplateUUID) == "" {
		writeError(w, http.StatusBadRequest, "validation_error", "name and templateUUID are required")
		return
	}
	if req.GuestWebPort == 0 {
		req.GuestWebPort = h.cfg.DefaultWebPort
	}

	template, err := h.store.TemplateForAccount(r.Context(), scope.AccountID, req.TemplateUUID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "Template not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load template.")
		return
	}

	vmUUID := newUUIDLike()
	files := h.vmrt.FilesForVM(vmUUID)
	vm, err := h.store.CreateVM(r.Context(), store.CreateVMParams{
		UUID:          vmUUID,
		AccountID:     scope.AccountID,
		AccountUUID:   scope.AccountUUID,
		Name:          req.Name,
		TemplateID:    template.ID,
		TemplateUUID:  template.UUID,
		GuestWebPort:  req.GuestWebPort,
		CPUCount:      template.DefaultCPU,
		MemoryMB:      template.DefaultMemoryMB,
		RuntimeDir:    files.RuntimeDir,
		DiskImagePath: files.DiskImagePath,
		PIDFilePath:   files.PIDFilePath,
		QMPSocketPath: files.QMPSocketPath,
		SerialLogPath: files.SerialLogPath,
	})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			writeError(w, http.StatusConflict, "conflict", "A VM with that name already exists.")
		default:
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create VM.")
		}
		return
	}

	if err := h.vmrt.PrepareDisk(files, template.DiskImageRef); err != nil {
		_ = h.store.UpdateVMState(r.Context(), vm.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to prepare VM disk.")
		return
	}
	if err := h.vmrt.StartVM(vm, files, template.DefaultCPU, template.DefaultMemoryMB); err != nil {
		_ = h.store.UpdateVMState(r.Context(), vm.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to start VM.")
		return
	}
	if err := h.vmrt.WriteNginxSnippet(vm); err != nil {
		_ = h.store.UpdateVMState(r.Context(), vm.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to write nginx configuration.")
		return
	}
	if err := h.vmrt.ReloadNginx(); err != nil {
		_ = h.store.UpdateVMState(r.Context(), vm.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to reload nginx.")
		return
	}
	go h.monitorVMReadiness(vm)
	writeJSON(w, http.StatusCreated, map[string]any{"vm": vm})
}

func (h authHandler) handleVMSubroutes(w http.ResponseWriter, r *http.Request, scope store.AccountScope, tail string) {
	vmName, action := splitVMSubroute(tail)
	if vmName == "" {
		writeError(w, http.StatusNotFound, "not_found", "Route not found.")
		return
	}

	switch {
	case action == "" && r.Method == http.MethodGet:
		h.handleVMGet(w, r, scope, vmName)
	case action == "/poweroff" && r.Method == http.MethodPost:
		h.handleVMPoweroff(w, r, scope, vmName)
	case action == "/kill" && r.Method == http.MethodPost:
		h.handleVMKill(w, r, scope, vmName)
	case action == "/poweron" && r.Method == http.MethodPost:
		h.handleVMPoweron(w, r, scope, vmName)
	default:
		writeError(w, http.StatusNotFound, "not_found", "Route not found.")
	}
}

func (h authHandler) handleVMGet(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) {
	vm, err := h.store.VMForAccountByName(r.Context(), scope.AccountID, scope.AccountUUID, vmName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "VM not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load VM.")
		return
	}
	h.refreshVMObservation(&vm)
	writeJSON(w, http.StatusOK, map[string]any{"vm": vm})
}

func (h authHandler) handleVMResolve(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) {
	vm, err := h.store.VMForAccountByName(r.Context(), scope.AccountID, scope.AccountUUID, vmName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "VM not found.")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load VM.")
		return
	}
	h.refreshVMObservation(&vm)

	host := resolveSSHHost(h.cfg.BaseURL)
	var resp sshResolveResponse
	resp.VM = vm
	resp.SSH.Host = host
	resp.SSH.Port = vm.HostSSHPort
	resp.SSH.DefaultUsername = h.cfg.DefaultSSHUser
	resp.SSH.Ready = vm.SSHReady
	writeJSON(w, http.StatusOK, resp)
}

func (h authHandler) handleVMPoweroff(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) {
	vm, ok := h.loadVMForAction(w, r, scope, vmName)
	if !ok {
		return
	}
	if err := h.vmrt.PoweroffVM(vm); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to power off VM.")
		return
	}
	if h.vmrt.WaitForSSHClosed(vm.HostSSHPort, 30*time.Second) {
		if err := h.store.UpdateVMState(r.Context(), vm.ID, "stopped", ""); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM state.")
			return
		}
		if err := h.store.UpdateVMReadiness(r.Context(), vm.ID, false, false); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM readiness.")
			return
		}
		vm.State = "stopped"
		vm.SSHReady = false
		vm.WebReady = false
		writeJSON(w, http.StatusOK, map[string]any{"vm": vm})
		return
	}
	h.refreshVMObservation(&vm)
	writeJSON(w, http.StatusOK, map[string]any{"vm": vm})
}

func (h authHandler) handleVMKill(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) {
	vm, ok := h.loadVMForAction(w, r, scope, vmName)
	if !ok {
		return
	}
	if err := h.vmrt.KillVM(vm); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to kill VM.")
		return
	}
	if !h.vmrt.WaitForSSHClosed(vm.HostSSHPort, 10*time.Second) {
		writeError(w, http.StatusInternalServerError, "internal_error", "VM did not stop after kill.")
		return
	}
	if err := h.store.UpdateVMState(r.Context(), vm.ID, "stopped", ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM state.")
		return
	}
	if err := h.store.UpdateVMReadiness(r.Context(), vm.ID, false, false); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM readiness.")
		return
	}
	vm.State = "stopped"
	vm.SSHReady = false
	vm.WebReady = false
	writeJSON(w, http.StatusOK, map[string]any{"vm": vm})
}

func (h authHandler) handleVMPoweron(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) {
	vm, ok := h.loadVMForAction(w, r, scope, vmName)
	if !ok {
		return
	}
	if err := h.vmrt.StartExistingVM(vm); err != nil {
		_ = h.store.UpdateVMState(r.Context(), vm.ID, "error", err.Error())
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to start VM.")
		return
	}
	if err := h.store.UpdateVMState(r.Context(), vm.ID, "creating", ""); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM state.")
		return
	}
	if err := h.store.UpdateVMReadiness(r.Context(), vm.ID, false, false); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update VM readiness.")
		return
	}
	vm.State = "creating"
	vm.SSHReady = false
	vm.WebReady = false
	go h.monitorVMReadiness(vm)
	writeJSON(w, http.StatusOK, map[string]any{"vm": vm})
}

func (h authHandler) loadVMForAction(w http.ResponseWriter, r *http.Request, scope store.AccountScope, vmName string) (store.VM, bool) {
	if scope.Role != "owner" && scope.Role != "admin" && scope.Role != "user" {
		writeError(w, http.StatusForbidden, "forbidden", "Account access denied.")
		return store.VM{}, false
	}
	vm, err := h.store.VMForAccountByName(r.Context(), scope.AccountID, scope.AccountUUID, vmName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "VM not found.")
		} else {
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to load VM.")
		}
		return store.VM{}, false
	}
	return vm, true
}

func (h authHandler) monitorVMReadiness(vm store.VM) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			_ = h.store.UpdateVMState(context.Background(), vm.ID, "error", "timed out waiting for SSH readiness")
			_ = h.store.UpdateVMReadiness(context.Background(), vm.ID, false, false)
			return
		case <-ticker.C:
			if !h.vmrt.WaitForSSHReady(vm.HostSSHPort, 4*time.Second) {
				continue
			}
			webReady := h.vmrt.ProbeWebReady(vm.HostWebPort, 6*time.Second)
			_ = h.store.UpdateVMReadiness(context.Background(), vm.ID, true, webReady)
			_ = h.store.UpdateVMState(context.Background(), vm.ID, "running", "")
			return
		}
	}
}

func (h authHandler) refreshVMObservation(vm *store.VM) {
	if vm == nil {
		return
	}
	if vm.State != "creating" && (vm.State != "running" || vm.SSHReady) {
		return
	}
	sshReady := h.vmrt.WaitForSSHReady(vm.HostSSHPort, 200*time.Millisecond)
	if !sshReady {
		return
	}
	if vm.State != "running" || !vm.SSHReady {
		_ = h.store.UpdateVMReadiness(context.Background(), vm.ID, true, vm.WebReady)
		_ = h.store.UpdateVMState(context.Background(), vm.ID, "running", "")
		vm.State = "running"
		vm.SSHReady = true
	}
}

func (h authHandler) currentUser(r *http.Request) (store.User, error) {
	token, err := bearerToken(r)
	if err != nil {
		return store.User{}, err
	}
	return h.store.UserByToken(r.Context(), token)
}

func bearerToken(r *http.Request) (string, error) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if header == "" {
		return "", store.ErrAuthRequired
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", store.ErrAuthRequired
	}
	token := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if token == "" {
		return "", store.ErrAuthRequired
	}
	return token, nil
}

func splitAccountPath(path string) (accountUUID string, tail string, ok bool) {
	const prefix = "/v1/accounts/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", "", false
	}
	accountUUID = parts[0]
	if len(parts) == 1 {
		return accountUUID, "", true
	}
	return accountUUID, "/" + parts[1], true
}

func splitVMSubroute(path string) (vmName string, tail string) {
	parts := strings.SplitN(strings.Trim(path, "/"), "/", 2)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "", ""
	}
	vmName = parts[0]
	if len(parts) == 1 {
		return vmName, ""
	}
	return vmName, "/" + parts[1]
}

func resolveSSHHost(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil && parsed.Host != "" {
		if host := parsed.Hostname(); host != "" {
			return host
		}
	}
	return "127.0.0.1"
}

func newUUIDLike() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4],
		b[4:6],
		b[6:8],
		b[8:10],
		b[10:16],
	)
}

func decodeJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fmt.Errorf("request body is required")
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("invalid JSON body")
	}
	return nil
}

func method(verb string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != verb {
			writeMethodNotAllowed(w, verb)
			return
		}
		next(w, r)
	}
}

func writeMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	for _, method := range allowed {
		w.Header().Add("Allow", method)
	}
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed.")
}

func writeNotImplemented(w http.ResponseWriter, code string) {
	writeError(w, http.StatusNotImplemented, code, "Not implemented yet.")
}

func writeAuthError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTokenInvalid):
		writeError(w, http.StatusUnauthorized, "token_invalid", "Token is invalid.")
	default:
		writeError(w, http.StatusUnauthorized, "auth_required", "Authentication is required.")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func loggingMiddleware(logger *log.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
