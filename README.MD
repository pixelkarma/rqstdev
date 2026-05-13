# rqstdev

`rqstdev` is a two-part system:

- `api`: the single-host Linux server that manages accounts, QEMU VMs, and web routing
- `cli`: the user-facing CLI for account management, VM management, SSH access, and published web ports

This README is the operator/developer setup overview for the current implementation.

## Server Requirements Overview

The current server target is a single Linux host.

Required software:

- `qemu-system-x86_64`
- `qemu-img`
- a front proxy for `80/443`:
  - `nginx`, or
  - Caddy

Useful but not strictly required:

- `sqlite3` for inspecting the database manually
- `curl` for testing the local API and proxy

Runtime assumptions:

- the host can run QEMU guests
- the host has a base QCOW2 image available for cloning
- `rqstdev-api` can bind its configured listen address
- `rqstdev-api` can also bind additional published web ports on the host when users enable them

What is not required:

- Docker
- cloud-init
- a separate database server
- app-managed `nginx` snippets

## Build Commands

Workspace build:

```sh
go build ./...
```

Build the API:

```sh
cd api
go build ./cmd/rqstdev-api
```

Build the CLI:

```sh
cd cli
go build ./cmd/rqstdev
```

Cross-compile Linux binaries from macOS or another host:

```sh
cd api
GOOS=linux GOARCH=amd64 go build -o /tmp/rqstdev-api-linux ./cmd/rqstdev-api

cd ../cli
GOOS=linux GOARCH=amd64 go build -o /tmp/rqstdev-cli-linux ./cmd/rqstdev
```

## Server Configuration

The server binary reads a JSON config file. The current default flag is:

```sh
rqstdev-api --config /etc/rqstdev/config.json
```

Current required config fields:

- `listen_addr`
- `base_url`
- `base_domain`
- `db_path`
- `data_dir`
- `vms_dir`
- `qemu_binary_path`
- `default_template_name`
- `default_template_image_path`

Example:

```json
{
  "listen_addr": "0.0.0.0:18090",
  "base_url": "http://api.rqst.dev",
  "base_domain": "rqst.dev",
  "db_path": "/home/user/rqstdev/data/rqstdev.sqlite",
  "data_dir": "/home/user/rqstdev/data",
  "vms_dir": "/home/user/rqstdev/vms",
  "qemu_binary_path": "/usr/bin/qemu-system-x86_64",
  "default_template_name": "default",
  "default_template_image_path": "/home/user/rqstdev/assets/default-disk.qcow2"
}
```

Field notes:

- `listen_addr` is the API bind address, not the public domain
- `base_url` should be the URL the CLI uses, usually the front-proxy hostname such as `http://api.rqst.dev` or `https://api.rqst.dev`
- `base_domain` is the VM hostname suffix, for example `rqst.dev`
- `db_path` is the SQLite database file
- `data_dir` is the server data root
- `vms_dir` is where per-VM runtime directories are created
- `default_template_image_path` should point at the base QCOW2 image used for cloning new VMs

## Proxy Model

The current proxy model is:

- `80/443` traffic goes through a front proxy to `rqstdev-api`
- `rqstdev-api` reads the incoming hostname and routes web requests to the correct VM backend
- additional published web ports are opened directly by `rqstdev-api` on the host

Important implication:

- the front proxy is only needed for standard web entry on `80/443`
- published ports like `vm-name.rqst.dev:3456` are handled by `rqstdev-api` binding port `3456` itself

That means:

- your proxy must preserve the original `Host`
- your proxy should send `X-Forwarded-Proto`
- your proxy should send `X-Forwarded-Port`

## Nginx Example

Example `nginx` config for `80`:

```nginx
server {
    listen 80 default_server;
    listen [::]:80 default_server;
    server_name _;

    location / {
        proxy_pass http://127.0.0.1:18090;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

Minimal HTTPS shape with your own certificate paths:

```nginx
server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name api.rqst.dev *.rqst.dev;

    ssl_certificate     /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:18090;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-Port $server_port;
        proxy_set_header X-Real-IP $remote_addr;
    }
}
```

## Caddy Example

HTTP:

```caddy
:80 {
	reverse_proxy 127.0.0.1:18090 {
		header_up Host {host}
		header_up X-Forwarded-Proto {scheme}
		header_up X-Forwarded-Port {server_port}
	}
}
```

HTTPS with your own site names:

```caddy
api.rqst.dev, *.rqst.dev {
	reverse_proxy 127.0.0.1:18090 {
		header_up Host {host}
		header_up X-Forwarded-Proto {scheme}
		header_up X-Forwarded-Port {server_port}
	}
}
```

If you want Caddy to manage certificates automatically, use normal Caddy site addresses and DNS that point at the host.

## Additional Published Ports

Published ports are explicit and web-only in the current implementation.

Example CLI commands:

```sh
rqstdev port add myvm 3456:3456
rqstdev port list myvm
rqstdev port remove myvm 3456
```

Behavior:

- the mapping is stored in SQLite
- `rqstdev-api` opens the public port on the host
- requests to `vm-name.rqst.dev:3456` are routed to guest port `3456`

The front proxy does not manage these extra published ports.

## Runtime Layout

Each VM gets a runtime directory under `vms_dir`, for example:

```text
/home/user/rqstdev/vms/<vm-uuid>/
```

Current runtime files include:

- `disk.qcow2`
- `qemu.pid`
- `qmp.sock`
- `serial.log`

## Notes

- VM web services are user-managed inside the guest
- `rqstdev` does not automatically provision a web server inside the VM
- SSH access is through the CLI path, for example `rqstdev ssh myvm`
- the server is currently single-host only
