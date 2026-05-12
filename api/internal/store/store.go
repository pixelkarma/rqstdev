package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrConflict           = errors.New("conflict")
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAuthRequired       = errors.New("authentication required")
	ErrTokenInvalid       = errors.New("token invalid")
	ErrNotFound           = errors.New("not found")
)

type Store struct {
	db *sql.DB
}

type User struct {
	ID    int64
	UUID  string
	Email string
}

type AccountMembership struct {
	AccountUUID string `json:"uuid"`
	AccountName string `json:"name"`
	Role        string `json:"role"`
}

type AccountScope struct {
	AccountID   int64
	AccountUUID string
	AccountName string
	Role        string
}

type Template struct {
	ID              int64  `json:"-"`
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	DiskImageRef    string `json:"-"`
	DefaultCPU      int    `json:"defaultCPU"`
	DefaultMemoryMB int    `json:"defaultMemoryMB"`
}

type VM struct {
	ID            int64  `json:"-"`
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	AccountUUID   string `json:"accountUUID,omitempty"`
	TemplateUUID  string `json:"templateUUID,omitempty"`
	State         string `json:"state"`
	GuestWebPort  int    `json:"guestWebPort"`
	HostSSHPort   int    `json:"hostSSHPort"`
	HostWebPort   int    `json:"hostWebPort"`
	SSHReady      bool   `json:"sshReady"`
	WebReady      bool   `json:"webReady"`
	LastError     string `json:"lastError,omitempty"`
	CPUCount      int    `json:"-"`
	MemoryMB      int    `json:"-"`
	RuntimeDir    string `json:"-"`
	DiskImagePath string `json:"-"`
	PIDFilePath   string `json:"-"`
	QMPSocketPath string `json:"-"`
	SerialLogPath string `json:"-"`
}

type SignupParams struct {
	Email       string
	Password    string
	AccountName string
}

type CreateAccountParams struct {
	UserID int64
	Name   string
}

type CreateVMParams struct {
	UUID          string
	AccountID     int64
	AccountUUID   string
	Name          string
	TemplateID    int64
	TemplateUUID  string
	GuestWebPort  int
	CPUCount      int
	MemoryMB      int
	RuntimeDir    string
	DiskImagePath string
	PIDFilePath   string
	QMPSocketPath string
	SerialLogPath string
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := initDB(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func initDB(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	for _, stmt := range schemaStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("run migration: %w", err)
		}
	}
	for _, migration := range []struct {
		table      string
		column     string
		definition string
	}{
		{table: "vms", column: "ssh_ready", definition: "INTEGER NOT NULL DEFAULT 0 CHECK (ssh_ready IN (0,1))"},
		{table: "vms", column: "web_ready", definition: "INTEGER NOT NULL DEFAULT 0 CHECK (web_ready IN (0,1))"},
	} {
		if err := ensureColumn(ctx, db, migration.table, migration.column, migration.definition); err != nil {
			return err
		}
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping sqlite: %w", err)
	}
	return nil
}

var schemaStatements = []string{
	`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		email TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS accounts (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS account_users (
		id INTEGER PRIMARY KEY,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
		role TEXT NOT NULL CHECK (role IN ('owner', 'admin', 'user')),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		UNIQUE(account_id, user_id)
	);`,
	`CREATE TABLE IF NOT EXISTS account_invites (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
		role TEXT NOT NULL CHECK (role IN ('admin', 'user')),
		status TEXT NOT NULL CHECK (status IN ('pending', 'accepted', 'refused', 'revoked')),
		created_at TEXT NOT NULL,
		responded_at TEXT
	);`,
	`CREATE TABLE IF NOT EXISTS session_tokens (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash TEXT NOT NULL UNIQUE,
		created_at TEXT NOT NULL,
		last_used_at TEXT NOT NULL,
		expires_at TEXT,
		revoked_at TEXT
	);`,
	`CREATE TABLE IF NOT EXISTS auth_attempts (
		id INTEGER PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		purpose TEXT NOT NULL CHECK (purpose IN ('login', 'reset')),
		code_hash TEXT NOT NULL,
		created_at TEXT NOT NULL,
		used_at TEXT
	);`,
	`CREATE TABLE IF NOT EXISTS vm_templates (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL,
		account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE,
		disk_image_ref TEXT NOT NULL,
		default_cpu INTEGER NOT NULL,
		default_memory_mb INTEGER NOT NULL,
		active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS vms (
		id INTEGER PRIMARY KEY,
		uuid TEXT NOT NULL UNIQUE,
		name TEXT NOT NULL UNIQUE,
		account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
		template_id INTEGER NOT NULL REFERENCES vm_templates(id) ON DELETE RESTRICT,
		state TEXT NOT NULL CHECK (state IN ('creating', 'running', 'stopped', 'deleting', 'error')),
		ssh_ready INTEGER NOT NULL DEFAULT 0 CHECK (ssh_ready IN (0,1)),
		web_ready INTEGER NOT NULL DEFAULT 0 CHECK (web_ready IN (0,1)),
		guest_ip_address TEXT,
		guest_web_port INTEGER NOT NULL DEFAULT 80,
		cpu_count INTEGER NOT NULL,
		memory_mb INTEGER NOT NULL,
		runtime_dir TEXT NOT NULL,
		disk_image_path TEXT NOT NULL,
		pid_file_path TEXT NOT NULL,
		qmp_socket_path TEXT NOT NULL,
		serial_log_path TEXT NOT NULL,
		last_error TEXT,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	);`,
	`CREATE TABLE IF NOT EXISTS published_ports (
		id INTEGER PRIMARY KEY,
		vm_id INTEGER NOT NULL REFERENCES vms(id) ON DELETE CASCADE,
		public_port INTEGER NOT NULL UNIQUE,
		guest_port INTEGER NOT NULL,
		protocol TEXT NOT NULL DEFAULT 'tcp' CHECK (protocol = 'tcp'),
		created_at TEXT NOT NULL
	);`,
	`CREATE INDEX IF NOT EXISTS idx_account_users_user_id ON account_users(user_id);`,
	`CREATE INDEX IF NOT EXISTS idx_account_users_account_role ON account_users(account_id, role);`,
	`CREATE INDEX IF NOT EXISTS idx_account_invites_user_status ON account_invites(user_id, status);`,
	`CREATE INDEX IF NOT EXISTS idx_session_tokens_user_id ON session_tokens(user_id);`,
	`CREATE INDEX IF NOT EXISTS idx_auth_attempts_user_purpose_created ON auth_attempts(user_id, purpose, created_at);`,
	`CREATE INDEX IF NOT EXISTS idx_vm_templates_account_active ON vm_templates(account_id, active);`,
	`CREATE INDEX IF NOT EXISTS idx_vms_account_id ON vms(account_id);`,
	`CREATE INDEX IF NOT EXISTS idx_vms_state ON vms(state);`,
}

func ensureColumn(ctx context.Context, db *sql.DB, table, column, definition string) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &primaryKey); err != nil {
			return fmt.Errorf("scan %s column info: %w", table, err)
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate %s columns: %w", table, err)
	}

	if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func (s *Store) CreateUserWithAccount(ctx context.Context, params SignupParams) (User, string, error) {
	email := normalizeEmail(params.Email)
	password := strings.TrimSpace(params.Password)
	accountName := strings.TrimSpace(params.AccountName)
	if email == "" || password == "" || accountName == "" {
		return User{}, "", fmt.Errorf("email, password, and accountName are required")
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, "", fmt.Errorf("hash password: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, "", fmt.Errorf("begin signup tx: %w", err)
	}
	defer tx.Rollback()

	now := utcNow()
	userUUID := newUUIDLike()
	accountUUID := newUUIDLike()

	userResult, err := tx.ExecContext(ctx, `
		INSERT INTO users (uuid, email, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, userUUID, email, string(passwordHash), now, now)
	if err != nil {
		if isConstraintErr(err) {
			return User{}, "", ErrConflict
		}
		return User{}, "", fmt.Errorf("insert user: %w", err)
	}

	userID, err := userResult.LastInsertId()
	if err != nil {
		return User{}, "", fmt.Errorf("read user id: %w", err)
	}

	accountResult, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (uuid, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, accountUUID, accountName, now, now)
	if err != nil {
		return User{}, "", fmt.Errorf("insert account: %w", err)
	}

	accountID, err := accountResult.LastInsertId()
	if err != nil {
		return User{}, "", fmt.Errorf("read account id: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_users (account_id, user_id, role, created_at, updated_at)
		VALUES (?, ?, 'owner', ?, ?)
	`, accountID, userID, now, now); err != nil {
		return User{}, "", fmt.Errorf("insert account owner: %w", err)
	}

	token, tokenHash, err := newToken()
	if err != nil {
		return User{}, "", err
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO session_tokens (user_id, token_hash, created_at, last_used_at)
		VALUES (?, ?, ?, ?)
	`, userID, tokenHash, now, now); err != nil {
		return User{}, "", fmt.Errorf("insert session token: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return User{}, "", fmt.Errorf("commit signup tx: %w", err)
	}

	return User{ID: userID, UUID: userUUID, Email: email}, token, nil
}

func (s *Store) Authenticate(ctx context.Context, email, password string) (User, error) {
	var user User
	var passwordHash string

	row := s.db.QueryRowContext(ctx, `
		SELECT id, uuid, email, password_hash
		FROM users
		WHERE email = ?
	`, normalizeEmail(email))

	if err := row.Scan(&user.ID, &user.UUID, &user.Email, &passwordHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrInvalidCredentials
		}
		return User{}, fmt.Errorf("lookup user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return user, nil
}

func (s *Store) CreateSessionToken(ctx context.Context, userID int64) (string, error) {
	token, tokenHash, err := newToken()
	if err != nil {
		return "", err
	}

	now := utcNow()
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO session_tokens (user_id, token_hash, created_at, last_used_at)
		VALUES (?, ?, ?, ?)
	`, userID, tokenHash, now, now); err != nil {
		return "", fmt.Errorf("insert session token: %w", err)
	}
	return token, nil
}

func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var user User
	var tokenID int64

	row := s.db.QueryRowContext(ctx, `
		SELECT st.id, u.id, u.uuid, u.email
		FROM session_tokens st
		JOIN users u ON u.id = st.user_id
		WHERE st.token_hash = ? AND st.revoked_at IS NULL
	`, hashToken(token))

	if err := row.Scan(&tokenID, &user.ID, &user.UUID, &user.Email); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrTokenInvalid
		}
		return User{}, fmt.Errorf("lookup session token: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		UPDATE session_tokens
		SET last_used_at = ?
		WHERE id = ?
	`, utcNow(), tokenID); err != nil {
		return User{}, fmt.Errorf("touch session token: %w", err)
	}

	return user, nil
}

func (s *Store) RevokeToken(ctx context.Context, token string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE session_tokens
		SET revoked_at = ?
		WHERE token_hash = ? AND revoked_at IS NULL
	`, utcNow(), hashToken(token))
	if err != nil {
		return fmt.Errorf("revoke token: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read rows affected: %w", err)
	}
	if rows == 0 {
		return ErrTokenInvalid
	}
	return nil
}

func (s *Store) AccountsForUser(ctx context.Context, userID int64) ([]AccountMembership, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.uuid, a.name, au.role
		FROM account_users au
		JOIN accounts a ON a.id = au.account_id
		WHERE au.user_id = ?
		ORDER BY a.created_at ASC, a.id ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query memberships: %w", err)
	}
	defer rows.Close()

	var memberships []AccountMembership
	for rows.Next() {
		var m AccountMembership
		if err := rows.Scan(&m.AccountUUID, &m.AccountName, &m.Role); err != nil {
			return nil, fmt.Errorf("scan membership: %w", err)
		}
		memberships = append(memberships, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	return memberships, nil
}

func (s *Store) CreateAccountForUser(ctx context.Context, params CreateAccountParams) (AccountMembership, error) {
	name := strings.TrimSpace(params.Name)
	if params.UserID == 0 || name == "" {
		return AccountMembership{}, fmt.Errorf("account name is required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountMembership{}, fmt.Errorf("begin account tx: %w", err)
	}
	defer tx.Rollback()

	now := utcNow()
	accountUUID := newUUIDLike()

	result, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (uuid, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, accountUUID, name, now, now)
	if err != nil {
		return AccountMembership{}, fmt.Errorf("insert account: %w", err)
	}

	accountID, err := result.LastInsertId()
	if err != nil {
		return AccountMembership{}, fmt.Errorf("read account id: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_users (account_id, user_id, role, created_at, updated_at)
		VALUES (?, ?, 'owner', ?, ?)
	`, accountID, params.UserID, now, now); err != nil {
		return AccountMembership{}, fmt.Errorf("insert account owner: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return AccountMembership{}, fmt.Errorf("commit account tx: %w", err)
	}

	return AccountMembership{
		AccountUUID: accountUUID,
		AccountName: name,
		Role:        "owner",
	}, nil
}

func (s *Store) AccountScopeForUser(ctx context.Context, userID int64, accountUUID string) (AccountScope, error) {
	var scope AccountScope
	row := s.db.QueryRowContext(ctx, `
		SELECT a.id, a.uuid, a.name, au.role
		FROM accounts a
		JOIN account_users au ON au.account_id = a.id
		WHERE a.uuid = ? AND au.user_id = ?
	`, accountUUID, userID)
	if err := row.Scan(&scope.AccountID, &scope.AccountUUID, &scope.AccountName, &scope.Role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccountScope{}, ErrNotFound
		}
		return AccountScope{}, fmt.Errorf("lookup account scope: %w", err)
	}
	return scope, nil
}

func (s *Store) EnsureDefaultTemplate(ctx context.Context, name, imagePath string) error {
	row := s.db.QueryRowContext(ctx, `
		SELECT id
		FROM vm_templates
		WHERE account_id IS NULL AND name = ? AND disk_image_ref = ?
		LIMIT 1
	`, name, imagePath)

	var existingID int64
	err := row.Scan(&existingID)
	switch {
	case err == nil:
		_, err = s.db.ExecContext(ctx, `
			UPDATE vm_templates
			SET active = 1, updated_at = ?
			WHERE id = ?
		`, utcNow(), existingID)
		return err
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("lookup default template: %w", err)
	}

	now := utcNow()
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO vm_templates (
			uuid, name, account_id, disk_image_ref, default_cpu, default_memory_mb, active, created_at, updated_at
		) VALUES (?, ?, NULL, ?, 1, 1024, 1, ?, ?)
	`, newUUIDLike(), name, imagePath, now, now)
	if err != nil {
		return fmt.Errorf("insert default template: %w", err)
	}
	return nil
}

func (s *Store) VisibleTemplatesForAccount(ctx context.Context, accountID int64) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, uuid, name, disk_image_ref, default_cpu, default_memory_mb
		FROM vm_templates
		WHERE active = 1 AND (account_id IS NULL OR account_id = ?)
		ORDER BY account_id IS NOT NULL, name ASC
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("query templates: %w", err)
	}
	defer rows.Close()

	var templates []Template
	for rows.Next() {
		var t Template
		if err := rows.Scan(&t.ID, &t.UUID, &t.Name, &t.DiskImageRef, &t.DefaultCPU, &t.DefaultMemoryMB); err != nil {
			return nil, fmt.Errorf("scan template: %w", err)
		}
		templates = append(templates, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate templates: %w", err)
	}
	return templates, nil
}

func (s *Store) TemplateForAccount(ctx context.Context, accountID int64, templateUUID string) (Template, error) {
	var t Template
	row := s.db.QueryRowContext(ctx, `
		SELECT id, uuid, name, disk_image_ref, default_cpu, default_memory_mb
		FROM vm_templates
		WHERE uuid = ? AND active = 1 AND (account_id IS NULL OR account_id = ?)
	`, templateUUID, accountID)
	if err := row.Scan(&t.ID, &t.UUID, &t.Name, &t.DiskImageRef, &t.DefaultCPU, &t.DefaultMemoryMB); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Template{}, ErrNotFound
		}
		return Template{}, fmt.Errorf("lookup template: %w", err)
	}
	return t, nil
}

func (s *Store) CreateVM(ctx context.Context, params CreateVMParams) (VM, error) {
	name := strings.TrimSpace(params.Name)
	if name == "" || strings.TrimSpace(params.UUID) == "" {
		return VM{}, fmt.Errorf("vm uuid and name are required")
	}
	now := utcNow()
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO vms (
			uuid, name, account_id, template_id, state, ssh_ready, web_ready, guest_ip_address, guest_web_port,
			cpu_count, memory_mb, runtime_dir, disk_image_path, pid_file_path, qmp_socket_path,
			serial_log_path, last_error, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'creating', 0, 0, '', ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?)
	`, params.UUID, name, params.AccountID, params.TemplateID, params.GuestWebPort, params.CPUCount, params.MemoryMB,
		params.RuntimeDir, params.DiskImagePath, params.PIDFilePath, params.QMPSocketPath, params.SerialLogPath, now, now)
	if err != nil {
		if isConstraintErr(err) {
			return VM{}, ErrConflict
		}
		return VM{}, fmt.Errorf("insert vm: %w", err)
	}

	vmID, err := result.LastInsertId()
	if err != nil {
		return VM{}, fmt.Errorf("read vm id: %w", err)
	}

	return VM{
		ID:            vmID,
		UUID:          params.UUID,
		Name:          name,
		AccountUUID:   params.AccountUUID,
		TemplateUUID:  params.TemplateUUID,
		State:         "creating",
		GuestWebPort:  params.GuestWebPort,
		HostSSHPort:   HostSSHPort(vmID),
		HostWebPort:   HostWebPort(vmID),
		SSHReady:      false,
		WebReady:      false,
		CPUCount:      params.CPUCount,
		MemoryMB:      params.MemoryMB,
		RuntimeDir:    params.RuntimeDir,
		DiskImagePath: params.DiskImagePath,
		PIDFilePath:   params.PIDFilePath,
		QMPSocketPath: params.QMPSocketPath,
		SerialLogPath: params.SerialLogPath,
	}, nil
}

func (s *Store) UpdateVMState(ctx context.Context, vmID int64, state, lastError string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE vms
		SET state = ?, last_error = ?, updated_at = ?
		WHERE id = ?
	`, state, lastError, utcNow(), vmID)
	if err != nil {
		return fmt.Errorf("update vm state: %w", err)
	}
	return nil
}

func (s *Store) UpdateVMReadiness(ctx context.Context, vmID int64, sshReady, webReady bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE vms
		SET ssh_ready = ?, web_ready = ?, updated_at = ?
		WHERE id = ?
	`, boolToInt(sshReady), boolToInt(webReady), utcNow(), vmID)
	if err != nil {
		return fmt.Errorf("update vm readiness: %w", err)
	}
	return nil
}

func (s *Store) VMsForAccount(ctx context.Context, accountID int64, accountUUID string) ([]VM, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, uuid, name, state, ssh_ready, web_ready, guest_web_port, COALESCE(last_error, '')
		FROM vms
		WHERE account_id = ?
		ORDER BY created_at ASC, id ASC
	`, accountID)
	if err != nil {
		return nil, fmt.Errorf("query vms: %w", err)
	}
	defer rows.Close()

	var vms []VM
	for rows.Next() {
		var vm VM
		var sshReadyInt, webReadyInt int
		if err := rows.Scan(&vm.ID, &vm.UUID, &vm.Name, &vm.State, &sshReadyInt, &webReadyInt, &vm.GuestWebPort, &vm.LastError); err != nil {
			return nil, fmt.Errorf("scan vm: %w", err)
		}
		vm.AccountUUID = accountUUID
		vm.SSHReady = sshReadyInt == 1
		vm.WebReady = webReadyInt == 1
		vm.HostSSHPort = HostSSHPort(vm.ID)
		vm.HostWebPort = HostWebPort(vm.ID)
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vms: %w", err)
	}
	return vms, nil
}

func (s *Store) VMForAccountByName(ctx context.Context, accountID int64, accountUUID, vmName string) (VM, error) {
	var vm VM
	var sshReadyInt, webReadyInt int
	row := s.db.QueryRowContext(ctx, `
		SELECT
			id, uuid, name, state, ssh_ready, web_ready, guest_web_port,
			cpu_count, memory_mb, runtime_dir, disk_image_path, pid_file_path,
			qmp_socket_path, serial_log_path, COALESCE(last_error, '')
		FROM vms
		WHERE account_id = ? AND name = ?
		LIMIT 1
	`, accountID, strings.TrimSpace(vmName))
	if err := row.Scan(
		&vm.ID,
		&vm.UUID,
		&vm.Name,
		&vm.State,
		&sshReadyInt,
		&webReadyInt,
		&vm.GuestWebPort,
		&vm.CPUCount,
		&vm.MemoryMB,
		&vm.RuntimeDir,
		&vm.DiskImagePath,
		&vm.PIDFilePath,
		&vm.QMPSocketPath,
		&vm.SerialLogPath,
		&vm.LastError,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VM{}, ErrNotFound
		}
		return VM{}, fmt.Errorf("lookup vm: %w", err)
	}
	vm.AccountUUID = accountUUID
	vm.SSHReady = sshReadyInt == 1
	vm.WebReady = webReadyInt == 1
	vm.HostSSHPort = HostSSHPort(vm.ID)
	vm.HostWebPort = HostWebPort(vm.ID)
	return vm, nil
}

func HostSSHPort(vmID int64) int {
	return 22000 + int(vmID)
}

func HostWebPort(vmID int64) int {
	return 23000 + int(vmID)
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func utcNow() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func newUUIDLike() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexed[0:8], hexed[8:12], hexed[12:16], hexed[16:20], hexed[20:32])
}

func newToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	plain := base64.RawURLEncoding.EncodeToString(raw[:])
	return plain, hashToken(plain), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func isConstraintErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "constraint")
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
