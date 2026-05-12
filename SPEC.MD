# rqstdev Specification

## 1. Purpose

`rqstdev` is a VM management system composed of:

- `./api`: a single Go server running on one Linux machine
- `./cli`: a cross-platform Go CLI for macOS, Linux, and Windows

The system allows users to:

- create user accounts
- create and manage account-scoped VMs
- invite other users into accounts
- access VMs over SSH through the CLI
- expose VM web traffic on per-VM subdomains
- optionally publish additional explicit TCP ports per VM

This specification defines the initial single-host version of `rqstdev`.

## 2. Project Scope

### In scope

- single-host VM management
- email/password authentication
- optional system-wide email 2FA
- optional password reset by email code
- multiple accounts per user
- account invites and ownership transfer
- QEMU VM lifecycle control
- web exposure per VM through wildcard DNS and host-managed reverse proxying
- explicit published TCP ports per VM
- SSH access through the CLI
- SQLite-backed API state
- host-side install scripts for server and CLI

### Out of scope for v1

- account deletion
- user deletion
- template management through the end-user CLI
- backup/export tooling

## 3. High-Level Architecture

### Components

#### `./api`

Responsibilities:

- user signup, login, auth token validation
- optional email 2FA challenge flow
- optional password reset flow
- account membership and invite management
- VM template lookup
- VM creation and deletion
- VM power operations
- published port management
- local QEMU process management
- local QMP communication
- local networking setup required for guests
- generating and removing `nginx` config
- reloading `nginx` when routing changes

Runtime assumptions:

- runs on Linux
- has direct local access to the VM disk image directory
- has direct local access to QEMU/QMP resources
- has permission to write required network and `nginx` configuration

#### `./cli`

Responsibilities:

- interactive login/signup
- token storage
- account selection and switching
- VM management commands
- invite management
- SSH helper command
- Bubble Tea-based interactive UX for lists, prompts, and selections

Runtime assumptions:

- supported on macOS, Linux, and Windows
- talks to one configured API base URL

## 4. Deployment Model

### Single-host model

All VMs run on the same Linux machine as `./api`.

### DNS and ingress

- wildcard DNS such as `*.rqst.dev` points to the single public host running `./api` and `nginx`
- the API base URL defaults to `https://api.rqst.dev`
- the CLI must allow a different explicit base URL, including `http://` or `https://`
- public DNS is not managed by the product

## 5. Networking Model

### VM network

- each VM is attached to a private host-managed network
- the exact Linux implementation may use bridge/TAP networking
- each VM receives a stable private address reachable from the host

### Public web exposure

- each VM name is globally unique across the system
- each VM may be reached publicly at `https://<vm-name>.<base-domain>`
- the host uses `nginx` to route `80/443` traffic to the VM based on hostname
- the default guest web port is `80`
- the guest web port may be configured per VM
- wildcard TLS is assumed to be handled externally by the operator

### Public extra ports

The system supports explicit additional published TCP ports on a per-VM basis.

Rules:

- only explicitly published ports are exposed
- published ports are TCP-only in v1
- public host ports must be globally unique on the machine
- port collisions must be rejected
- only `owner` and `admin` users may add or remove published ports

Published port behavior:

- a published port maps one host/public TCP port to one guest TCP port on one VM
- no attempt is made to route arbitrary TCP traffic by subdomain
- users may still connect through the host IP or host domain plus the published port

CLI shape:

- `rqstdev port add <vm> <public-port>:<guest-port>`
- `rqstdev port remove <vm> <public-port>`
- `rqstdev port list [vm]`

### SSH

- SSH access is provided through the CLI only
- the CLI command is `rqstdev ssh <vm>`
- the user may specify a guest username even if the template defaults to `root`
- the implementation should use the system `ssh` client where practical rather than reimplementing SSH

## 6. Authentication and Security

### User authentication

- users authenticate with email and password
- passwords are stored hashed
- signup creates both the user and an initial owned account
- the account name is required in the signup payload

### Email 2FA

2FA is optional at the system level.

Behavior:

- if no email script is configured, login is email/password only
- if an email script is configured, successful password validation triggers an email code challenge
- the challenge is system-wide, not per-user configurable

Script contract:

- one configured script path is used
- the script receives:
  - `--email`
  - `--code`
  - `--purpose`
- valid purposes are at least:
  - `login`
  - `reset`

### Password reset

- password reset is available only when the email script is configured
- CLI command: `rqstdev account forgot <email>`
- flow:
  - request reset
  - send numeric code by script with `purpose=reset`
  - prompt for code
  - prompt for new password twice
  - force the user to log in again afterward

### Auth/reset codes

Rules:

- numeric only
- fixed length 6
- single use
- expiration 1 hour
- stored hashed in the database
- deleted after successful use
- expired codes may be deleted opportunistically during new code creation or validation

### API tokens

- the CLI stores auth tokens and sends them optimistically
- when a token is present, the CLI assumes it is valid
- if the API rejects the token:
  - show an auth-expired/re-authentication-required message
  - delete the stored token
  - exit immediately

### Token storage

Preferred storage:

- OS-native keyring using `github.com/zalando/go-keyring` or equivalent

Fallback:

- encrypted credential file in the OS app config directory

## 7. Account Model

### Account semantics

- a user may belong to multiple accounts
- a user may own multiple accounts
- account names are cosmetic only
- stable identity should use UUIDs, not names

### Roles

The system has exactly three roles:

- `owner`
- `admin`
- `user`

Permissions:

- `owner`: all admin permissions, account-level changes, ownership transfer, cannot be stripped of owner status except by transfer
- `admin`: create/delete VMs, add/remove published ports, invite users, revoke users except owner, perform VM power actions, list/status operations
- `user`: power on/off/kill, list/status, SSH access

### Account creation

Signup:

- creates the user
- creates the initial owned account

Additional accounts:

- users may create additional accounts with `rqstdev account create <name>`

### Invites

Rules:

- a user must already exist before they can be invited
- invites target an existing user email
- accepting an invite adds the account to the user
- accepting an invite does not automatically switch the current account

CLI behavior:

- `rqstdev invites` shows pending invites
- interactive accept/refuse behavior may use Bubble Tea

### Ownership transfer

Rules:

- only the owner may transfer ownership
- transfer promotes the target user to owner
- original owner is demoted to admin
- after transfer, the CLI prompts the original owner to create a new owned account
- the prompt should be prepopulated with `My Account`
- the original owner may decline and continue without owning any account

### Non-deletable entities

Accounts and users are not deletable in v1.

## 8. Account Selection and Session Behavior

### Persisted default account

- one account may be stored as the persistent default account
- new terminal sessions use the persisted default account unless overridden

### Terminal-local active account

`rqstdev account use <name>` applies only to the current terminal session.

Implementation model:

- the CLI stores terminal-local session state in a temp/session file
- session identity is derived from terminal context
- later invocations from the same terminal reuse that temporary active account
- other terminal windows remain unaffected

### Single-command override

- commands may also accept `--account <name>`
- `--account` overrides both terminal-local and default account selection

### Account commands

Supported command surface:

- `rqstdev account create <name>`
- `rqstdev account add`
- `rqstdev account remove`
- `rqstdev account default <name>`
- `rqstdev account use <name>`
- `rqstdev account`
- `rqstdev account user invite <email>`
- `rqstdev account user revoke <email>`
- `rqstdev account transfer <email>`

Note:

- exact semantics of `account add/remove` should match the stored local CLI account/base-URL configuration model
- these are local CLI configuration actions, not remote API account membership actions

## 9. VM Model

### VM identity

- VM names are globally unique across the whole system
- VM names must be URL-safe
- allowed characters: lowercase alphanumeric and dash

### VM source

- VMs are created by cloning a template disk image
- no `cloud-init` dependency is required in v1
- no SSH key injection is required in v1
- templates are assumed to already contain a bootable OS image
- templates may include a default root user with password authentication
- templates are not required to include a running web service
- a cloned VM may be SSH-ready before it is web-ready

### VM templates

Public templates:

- available to all accounts
- represented by `accountId = NULL`

Private templates:

- available only to the owning account
- represented by `accountId = <account>`

Template management:

- stored in the database
- seeded and maintained manually by the operator in v1
- not exposed as end-user CRUD commands in v1

### VM creation UX

`rqstdev add` should prompt for:

- VM name
- template
- optional guest web port, defaulting to `80`

If VM name is omitted in commands where it is optional, the CLI may present a selection list.

### VM lifecycle

Supported states:

- `creating`
- `running`
- `stopped`
- `deleting`
- `error`

Readiness expectations:

- `creating` means the VM record exists and QEMU start has been attempted, but guest SSH is not yet confirmed
- `running` means the guest SSH port is reachable through the host forwarding path
- web readiness is tracked separately from the main VM state
- a VM may be `running` while `webReady = false`

### Power behavior

Supported actions:

- power on
- graceful power off
- kill

Power-off behavior:

- normal `poweroff` is graceful
- a VM should only be marked `stopped` after the forwarded SSH path is no longer reachable
- `kill` is immediate

### Deletion

- delete is a hard delete
- VM disk and associated runtime/networking state are removed
- confirmation requires typing `DELETE`
- `--force` bypasses the confirmation prompt

## 10. CLI UX

### Framework

- Bubble Tea should be used for interactive lists, prompts, and selection UIs

### No-auth startup

When the CLI runs without a valid token:

- prompt the user to log in or sign up

After successful auth:

- show quick actions/manual info
- show pending invites

### VM commands

Supported commands:

- `rqstdev add`
- `rqstdev delete <name>`
- `rqstdev poweroff <name>`
- `rqstdev poweron <name>`
- `rqstdev kill <name>`
- `rqstdev list`
- `rqstdev`
- `rqstdev ssh <vm>`
- `rqstdev port add <vm> <public-port>:<guest-port>`
- `rqstdev port remove <vm> <public-port>`
- `rqstdev port list [vm]`

If `<name>` is omitted where supported, the CLI may present an interactive selection list for the current account.

## 11. API and Server Configuration

### API base URL

- CLI default suggestion: `https://api.rqst.dev`
- CLI must support operator-specified alternative URLs
- the URL must be explicit, including `http://` or `https://`

### API config file

The API uses a config file in the OS-preferred location.

Requirements:

- JSON format
- server-side config path may be overridden with `--config <path>`

Suggested contents:

- listen address/port
- base domain
- SQLite path
- VM storage paths
- QEMU binary path
- default template/image paths if not fully DB-driven
- `nginx` config/snippet paths
- optional email script path
- networking settings required by the host integration

### CLI config

The CLI uses the Go/OS standard config directory resolution.

Suggested contents:

- API base URL
- known local account references
- default account

Tokens should not be stored in plain CLI config when keyring/fallback secret storage is available.

## 12. Persistence Model

### Database

- SQLite only in v1
- one server, one SQLite file
- `PRAGMA foreign_keys = ON` should be enabled
- store timestamps in UTC ISO-8601 text or Unix seconds consistently across all tables

### Entity guidance

Use:

- integer primary keys internally
- UUIDs for externally visible identifiers where appropriate
- foreign keys for referential integrity
- `CHECK` constraints for enum-like fields

### Detailed schema

The following is the intended v1 logical schema. Exact column names may vary slightly in implementation, but behavior and constraints should match.

#### `users`

Purpose:

- identity and login credentials

Columns:

- `id INTEGER PRIMARY KEY`
- `uuid TEXT NOT NULL UNIQUE`
- `email TEXT NOT NULL UNIQUE`
- `password_hash TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Rules:

- `email` should be normalized to lowercase and trimmed before insert

Suggested indexes:

- unique index on `email`
- unique index on `uuid`

#### `accounts`

Purpose:

- user-visible account containers for VMs and membership

Columns:

- `id INTEGER PRIMARY KEY`
- `uuid TEXT NOT NULL UNIQUE`
- `name TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Rules:

- account names are not required to be unique

Suggested indexes:

- unique index on `uuid`

#### `account_users`

Purpose:

- account membership and role assignment

Columns:

- `id INTEGER PRIMARY KEY`
- `account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT`
- `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT`
- `role TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Rules:

- `role` must be one of `owner`, `admin`, `user`
- one membership row per `(account_id, user_id)`
- each account must have exactly one `owner` in application logic

Suggested indexes:

- unique index on `(account_id, user_id)`
- index on `user_id`
- index on `(account_id, role)`

#### `account_invites`

Purpose:

- pending account invitations for existing users

Columns:

- `id INTEGER PRIMARY KEY`
- `uuid TEXT NOT NULL UNIQUE`
- `account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT`
- `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT`
- `role TEXT NOT NULL`
- `status TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `responded_at TEXT`

Rules:

- `role` must be one of `admin`, `user`
- `status` must be one of `pending`, `accepted`, `refused`, `revoked`
- there should be at most one pending invite for a given `(account_id, user_id)`
- inviting a user already in the account must fail

Suggested indexes:

- unique partial index on `(account_id, user_id)` where `status = 'pending'`
- index on `(user_id, status)`
- index on `(account_id, status)`

#### `session_tokens`

Purpose:

- bearer tokens for authenticated CLI sessions

Columns:

- `id INTEGER PRIMARY KEY`
- `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE`
- `token_hash TEXT NOT NULL UNIQUE`
- `created_at TEXT NOT NULL`
- `last_used_at TEXT NOT NULL`
- `expires_at TEXT`
- `revoked_at TEXT`

Rules:

- tokens presented by the CLI should be stored hashed server-side
- revoked tokens must not authenticate
- non-revoked tokens with `expires_at IS NULL` are valid until explicitly revoked

Suggested indexes:

- unique index on `token_hash`
- index on `user_id`
- index on `revoked_at`

#### `auth_attempts`

Purpose:

- email code challenges for login 2FA and password reset

Columns:

- `id INTEGER PRIMARY KEY`
- `user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE`
- `purpose TEXT NOT NULL`
- `code_hash TEXT NOT NULL`
- `created_at TEXT NOT NULL`
- `used_at TEXT`

Rules:

- `purpose` must be one of `login`, `reset`
- codes expire after 1 hour in application logic
- codes are single-use

Suggested indexes:

- index on `(user_id, purpose, created_at)`
- index on `used_at`

#### `vm_templates`

Purpose:

- operator-managed VM templates

Columns:

- `id INTEGER PRIMARY KEY`
- `uuid TEXT NOT NULL UNIQUE`
- `name TEXT NOT NULL`
- `account_id INTEGER REFERENCES accounts(id) ON DELETE CASCADE`
- `disk_image_ref TEXT NOT NULL`
- `default_cpu INTEGER NOT NULL`
- `default_memory_mb INTEGER NOT NULL`
- `active INTEGER NOT NULL DEFAULT 1`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Rules:

- `account_id IS NULL` means public template
- `account_id IS NOT NULL` means private account-owned template
- `active` must be `0` or `1`

Suggested indexes:

- unique index on `uuid`
- index on `(account_id, active)`
- index on `active`

#### `vms`

Purpose:

- VM ownership, state, and runtime metadata

Columns:

- `id INTEGER PRIMARY KEY`
- `uuid TEXT NOT NULL UNIQUE`
- `name TEXT NOT NULL UNIQUE`
- `account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT`
- `template_id INTEGER NOT NULL REFERENCES vm_templates(id) ON DELETE RESTRICT`
- `state TEXT NOT NULL`
- `ssh_ready INTEGER NOT NULL DEFAULT 0`
- `web_ready INTEGER NOT NULL DEFAULT 0`
- `guest_ip_address TEXT`
- `guest_web_port INTEGER NOT NULL DEFAULT 80`
- `cpu_count INTEGER NOT NULL`
- `memory_mb INTEGER NOT NULL`
- `runtime_dir TEXT NOT NULL`
- `disk_image_path TEXT NOT NULL`
- `pid_file_path TEXT NOT NULL`
- `qmp_socket_path TEXT NOT NULL`
- `serial_log_path TEXT NOT NULL`
- `last_error TEXT`
- `created_at TEXT NOT NULL`
- `updated_at TEXT NOT NULL`

Rules:

- `state` must be one of `creating`, `running`, `stopped`, `deleting`, `error`
- `ssh_ready` and `web_ready` are boolean flags stored as `0/1`
- `name` is globally unique and URL-safe

Suggested indexes:

- unique index on `uuid`
- unique index on `name`
- index on `account_id`
- index on `state`

The VM record or associated runtime state must be sufficient to derive and validate:

- VM runtime directory
- disk image path
- PID file path
- QMP socket path
- serial log path

#### `published_ports`

Purpose:

- explicit public TCP port mappings for VMs

Columns:

- `id INTEGER PRIMARY KEY`
- `vm_id INTEGER NOT NULL REFERENCES vms(id) ON DELETE CASCADE`
- `public_port INTEGER NOT NULL`
- `guest_port INTEGER NOT NULL`
- `protocol TEXT NOT NULL DEFAULT 'tcp'`
- `created_at TEXT NOT NULL`

Rules:

- `protocol` must be `tcp` in v1
- `public_port` must be globally unique on the host

Suggested indexes:

- unique index on `public_port`
- index on `vm_id`

### Transaction boundaries

The API should use explicit transactions for:

- signup and initial account creation
- invite acceptance
- ownership transfer
- VM creation record initialization
- VM deletion record cleanup
- published port add/remove

### Cleanup expectations

The API may opportunistically clean expired or used auth records during:

- login challenge creation
- password reset challenge creation
- code verification

Token cleanup may be periodic or opportunistic, but revoked tokens must not remain valid.

## 13. HTTP API

### General rules

- JSON request/response bodies unless otherwise stated
- authenticated endpoints require `Authorization: Bearer <token>`
- all timestamps are UTC
- endpoints should prefer UUIDs or stable names externally, not integer IDs
- API paths should be versioned under `/v1`
- local CLI config commands such as `account add/remove` do not require matching remote HTTP endpoints

### Error envelope

Suggested error shape:

```json
{
  "error": {
    "code": "invalid_credentials",
    "message": "Invalid email or password."
  }
}
```

Suggested common codes:

- `invalid_credentials`
- `auth_required`
- `token_invalid`
- `forbidden`
- `not_found`
- `validation_error`
- `conflict`
- `two_factor_required`
- `reset_not_enabled`
- `internal_error`

### Authentication flow

#### `POST /v1/auth/signup`

Purpose:

- create a user and their initial owned account

Request:

```json
{
  "email": "user@example.com",
  "password": "secret",
  "accountName": "My Account"
}
```

Behavior:

- create `users`, `accounts`, and `account_users(role=owner)` in one transaction
- if email 2FA is disabled, return a token immediately
- if email 2FA is enabled, create a login challenge and return challenge metadata instead of a token

Success response when 2FA is disabled:

```json
{
  "token": "plain-token",
  "user": {
    "uuid": "user-uuid",
    "email": "user@example.com"
  }
}
```

Challenge response when 2FA is enabled:

```json
{
  "challenge": {
    "type": "email_code",
    "purpose": "login",
    "email": "user@example.com"
  }
}
```

#### `POST /v1/auth/login`

Purpose:

- authenticate an existing user

Request:

```json
{
  "email": "user@example.com",
  "password": "secret"
}
```

Behavior:

- verify password
- if 2FA is disabled, issue token
- if 2FA is enabled, create `auth_attempts(purpose=login)` and invoke the configured script

Responses:

- same token response as signup when 2FA is disabled
- same challenge response as signup when 2FA is enabled

#### `POST /v1/auth/challenge/verify`

Purpose:

- verify a pending email code for login

Request:

```json
{
  "email": "user@example.com",
  "purpose": "login",
  "code": "123456"
}
```

Behavior:

- match the newest valid unused code within 1 hour
- delete or mark prior expired codes during verification
- issue a new session token on success

Success response:

```json
{
  "token": "plain-token",
  "user": {
    "uuid": "user-uuid",
    "email": "user@example.com"
  }
}
```

#### `POST /v1/auth/forgot`

Purpose:

- start password reset if reset is enabled

Request:

```json
{
  "email": "user@example.com"
}
```

Behavior:

- if no email script is configured, return `reset_not_enabled`
- create `auth_attempts(purpose=reset)` and invoke the configured script

Success response:

```json
{
  "ok": true
}
```

#### `POST /v1/auth/reset`

Purpose:

- complete password reset using an email code

Request:

```json
{
  "email": "user@example.com",
  "code": "123456",
  "newPassword": "new-secret"
}
```

Behavior:

- validate the code
- replace the password hash
- revoke existing session tokens for the user

Success response:

```json
{
  "ok": true
}
```

#### `POST /v1/auth/logout`

Purpose:

- revoke the current token

Authenticated:

- yes

Success response:

```json
{
  "ok": true
}
```

#### `GET /v1/me`

Purpose:

- validate token and return current user context

Authenticated:

- yes

Response:

```json
{
  "user": {
    "uuid": "user-uuid",
    "email": "user@example.com"
  },
  "accounts": [
    {
      "uuid": "account-uuid",
      "name": "My Account",
      "role": "owner"
    }
  ]
}
```

### Account endpoints

#### `GET /v1/accounts`

Purpose:

- list accounts visible to the authenticated user

Authenticated:

- yes

#### `POST /v1/accounts`

Purpose:

- create an additional owned account

Authenticated:

- yes

Request:

```json
{
  "name": "Project Two"
}
```

Behavior:

- create account
- create `account_users(role=owner)` for current user

#### `GET /v1/accounts/{accountUUID}`

Purpose:

- fetch account details and members

Authenticated:

- yes, must be a member

#### `POST /v1/accounts/{accountUUID}/invites`

Purpose:

- invite an existing user into an account

Authenticated:

- yes, role must be `owner` or `admin`

Request:

```json
{
  "email": "member@example.com",
  "role": "user"
}
```

Behavior:

- resolve user by email
- fail if no such user exists
- fail if already a member
- create or replace a pending invite

#### `GET /v1/invites`

Purpose:

- list pending invites for the authenticated user

Authenticated:

- yes

#### `POST /v1/invites/{inviteUUID}/accept`

Purpose:

- accept a pending invite

Authenticated:

- yes, invite must belong to current user

Behavior:

- in one transaction, mark invite accepted and create `account_users` membership

#### `POST /v1/invites/{inviteUUID}/refuse`

Purpose:

- refuse a pending invite

Authenticated:

- yes

#### `POST /v1/accounts/{accountUUID}/users/{userUUID}/revoke`

Purpose:

- remove a non-owner member from an account

Authenticated:

- yes, role must be `owner` or `admin`

Behavior:

- admins may not revoke owners
- account members cannot revoke themselves if that would orphan ownership rules

#### `POST /v1/accounts/{accountUUID}/transfer`

Purpose:

- transfer account ownership

Authenticated:

- yes, role must be `owner`

Request:

```json
{
  "email": "newowner@example.com"
}
```

Behavior:

- target must already be a member
- swap roles in one transaction:
  - old owner becomes `admin`
  - target becomes `owner`

### Template endpoints

#### `GET /v1/accounts/{accountUUID}/templates`

Purpose:

- list templates visible to the authenticated user for the selected account

Authenticated:

- yes

Behavior:

- return public templates plus private templates owned by the selected account

### VM endpoints

#### `GET /v1/accounts/{accountUUID}/vms`

Purpose:

- list VMs for an account

Authenticated:

- yes, must be account member

#### `POST /v1/accounts/{accountUUID}/vms`

Purpose:

- create a VM

Authenticated:

- yes, role must be `owner` or `admin`

Request:

```json
{
  "name": "my-vm",
  "templateUUID": "template-uuid",
  "guestWebPort": 80
}
```

Behavior:

- validate unique VM name
- validate template visibility
- create VM row in `creating`
- derive runtime paths
- clone disk image
- start QEMU
- configure/reload web proxy
- keep the VM in `creating` until guest SSH readiness is confirmed
- update VM to `running` only after guest SSH becomes reachable
- track guest web readiness separately from the main state
- update VM to `error` if boot never reaches SSH readiness

#### `GET /v1/accounts/{accountUUID}/vms/{vmName}`

Purpose:

- fetch one VM by name within account context

Authenticated:

- yes, must be account member

#### `DELETE /v1/accounts/{accountUUID}/vms/{vmName}`

Purpose:

- hard delete a VM

Authenticated:

- yes, role must be `owner` or `admin`

Behavior:

- stop or kill the VM if needed
- remove published ports
- remove nginx config
- delete runtime files
- delete DB row

#### `POST /v1/accounts/{accountUUID}/vms/{vmName}/poweron`

Purpose:

- start a stopped VM

Authenticated:

- yes, role must be `owner`, `admin`, or `user`

#### `POST /v1/accounts/{accountUUID}/vms/{vmName}/poweroff`

Purpose:

- graceful shutdown through QMP

Authenticated:

- yes, role must be `owner`, `admin`, or `user`

#### `POST /v1/accounts/{accountUUID}/vms/{vmName}/kill`

Purpose:

- force-stop a VM immediately

Authenticated:

- yes, role must be `owner`, `admin`, or `user`

#### `GET /v1/accounts/{accountUUID}/vms/{vmName}/ports`

Purpose:

- list published ports for a VM

Authenticated:

- yes, must be account member

#### `POST /v1/accounts/{accountUUID}/vms/{vmName}/ports`

Purpose:

- publish an additional TCP port

Authenticated:

- yes, role must be `owner` or `admin`

Request:

```json
{
  "publicPort": 8080,
  "guestPort": 8080
}
```

Behavior:

- validate host port uniqueness
- create DB row
- apply host port forwarding rule

#### `DELETE /v1/accounts/{accountUUID}/vms/{vmName}/ports/{publicPort}`

Purpose:

- remove a published port

Authenticated:

- yes, role must be `owner` or `admin`

Behavior:

- remove host forwarding rule
- delete DB row

### CLI support endpoints

#### `GET /v1/accounts/{accountUUID}/resolve-vm/{vmName}`

Purpose:

- return connection details needed by `rqstdev ssh <vm>`

Authenticated:

- yes, must be account member

Response:

```json
{
  "vm": {
    "name": "my-vm",
    "state": "running",
    "sshReady": true,
    "webReady": false
  },
  "ssh": {
    "host": "api.rqst.dev",
    "port": 2222,
    "defaultUsername": "root",
    "ready": true
  }
}
```

Note:

- the API host and resolved SSH port are the source of truth for CLI SSH connections
- this endpoint exists so the CLI does not need to hardcode host-side SSH routing assumptions
## 14. Host Integration

### QEMU

The API directly manages local QEMU processes.

Responsibilities include:

- cloning template disk images
- starting VM processes
- stopping VM processes
- killing VM processes when requested
- monitoring status
- using QMP for control and monitoring where useful

### Per-VM runtime layout

Each VM should have a dedicated runtime directory on disk.

The runtime directory should contain at least:

- cloned VM disk image
- PID file
- QMP Unix socket
- serial log file

This runtime layout should be deterministic from VM identity so the API can recover state after restart.

### QEMU launch model

The v1 launch model should be explicit and file-based rather than hidden behind a heavyweight orchestrator.

The QEMU invocation should support at least:

- configured QEMU binary path
- configured memory
- configured CPU count
- QCOW2 disk attachment
- user-mode networking with explicit host port forwards
- QMP Unix socket creation
- headless operation

Expected command features include:

- `-m`
- `-smp`
- `-drive file=...,format=qcow2`
- `-nic user,hostfwd=...`
- `-boot d`
- `-qmp unix:...,server,nowait`
- `-monitor none`
- `-serial file:...`
- `-display none`

Daemonized launch is acceptable in v1 if process tracking remains reliable.

### QMP usage

QMP should be used for graceful VM power actions where practical.

At minimum, the graceful shutdown flow should:

- connect to the VM's Unix QMP socket
- read the QMP greeting
- send `qmp_capabilities`
- send `system_powerdown`

Forced termination remains a separate kill path and must not rely on QMP.

### Process tracking

The API should track each running VM using persisted runtime metadata plus OS process validation.

At minimum:

- write a PID file when launching a VM
- validate whether the PID still exists before reporting `running`
- tolerate stale PID and socket files on restart or cleanup

Process scanning may be used as a fallback, but PID file plus runtime-path validation should be the primary model.

### Reverse proxy

The API is responsible for:

- generating per-VM `nginx` config or snippets
- removing config when VMs or web exposure are removed
- reloading `nginx` after changes

### Networking

The API is responsible for the host-side changes needed for:

- QEMU user-mode guest networking
- host-visible port forwarding for VM SSH access used by `rqstdev ssh`
- localhost-only port forwarding for guest web targets behind `nginx`
- published TCP port forwarding
- cleanup on VM deletion or port removal

The exact implementation technology may be pinned during implementation, but the product may require specific host networking tools as prerequisites.

## 15. Installers

### Included scripts

The repo should include:

- `install.sh`
- `install-server.sh`

### `install.sh`

Purpose:

- install the CLI if the user built it locally instead of downloading a release binary

Behavior:

- detect OS where practical
- confirm target install path
- allow override of the target path
- on macOS/Linux, use `sudo` when copying if needed

If this proves too error-prone during implementation, a simpler fallback is acceptable:

- print the built binary path
- print the recommended manual install location
- do not perform automatic copy

### `install-server.sh`

Purpose:

- install the API server on the Linux host
- place the binary in the appropriate location
- assist with required directories and service setup as implementation matures

## 16. Deferred Items

The following are explicitly deferred beyond v1:

- automatic DNS management
- UDP published ports
- end-user template CRUD
- backup/export tooling
- cloud-init-based guest customization

## 17. Implementation Notes

This spec favors:

- small operational surface area
- one-machine correctness
- straightforward QEMU image cloning
- automatic web routing for each VM
- explicit published ports rather than magic routing promises

This is intentional. The goal for v1 is a buildable, predictable single-host system rather than a partially abstracted multi-host platform.
