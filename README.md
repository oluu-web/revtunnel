# RevTunnel

RevTunnel exposes a local HTTP port to the public internet via a persistent TLS tunnel. It relays traffic only; your code runs entirely on your local machine.

---

## Table of Contents

- [Getting Started](#getting-started)
- [CLI Reference](#cli-reference)
- [Limits & Lifecycle](#limits--lifecycle)
- [Self-Hosting](#self-hosting)
- [Security](#security)

---

## Getting Started

### 1. Install the agent

Download the latest binary for your platform from the [GitHub Releases](https://github.com/oluu-web/revtunnel/releases) page.

### 2. Authenticate

Exchange your API key for a session token. This only needs to be done once — the token is saved to `~/.tunnel/config.yaml` and reused automatically.

```bash
tunnel login --api-key lnt_live_ab12cd34.yoursecrethere
```

### 3. Start a tunnel

```bash
tunnel http 3000
```

You'll see output like:

```
tunnel active
  url         https://abc123.revtunnel.xyz
  Forwarding localhost:3000 at https://abc.revtunnel.xyz
  press Ctrl+C to stop
```

To expose multiple ports, run the agent in separate terminals — each gets its own public URL.

---

## CLI Reference

### `tunnel login`

Authenticates with an API key and saves a session token to config.

```bash
tunnel login --api-key <key>
```

| Flag        | Required | Description                         |
| ----------- | -------- | ----------------------------------- |
| `--api-key` | ✅       | API key issued by the tunnel server |

---

### `tunnel http <port>`

Exposes a local port to the internet.

```bash
tunnel http 3000
```

Internally, this:

1. Registers the tunnel via `POST /v1/tunnels`
2. Dials the relay and sends a `HELLO` message with your JWT and tunnel ID
3. Prints the assigned public URL

| Flag       | Default     | Description                                            |
| ---------- | ----------- | ------------------------------------------------------ |
| `--server` | from config | Tunnel server address                                  |
| `--token`  | from config | Session token (set automatically after `tunnel login`) |

---

### `tunnel config`

Manages persistent configuration stored at `~/.tunnel/config.yaml`.

| Subcommand                         | Description                         |
| ---------------------------------- | ----------------------------------- |
| `tunnel config set-server <addr>`  | Save the tunnel server address      |
| `tunnel config set-token <token>`  | Manually save a session token       |
| `tunnel config set-tunnel-id <id>` | Save a tunnel ID (temporary helper) |

---

### Components

- **Agent**: CLI binary run by the developer. Dials the relay, multiplexes streams over yamux, and forwards traffic to the local service.
- **Relay server**: Accepts agent connections, maps public hostnames to live yamux sessions, and forwards HTTP traffic.
- **Control plane**: REST API (co-located with the relay server) that manages auth, API keys, and tunnel registration.
- **Database**: PostgreSQL for persistent state (users, API keys, tunnels). Active sessions are tracked in an in-memory registry for fast hostname lookup.

### Control Plane API

| Method   | Endpoint           | Description                               |
| -------- | ------------------ | ----------------------------------------- |
| `POST`   | `/auth/token`      | Exchange an API key for a 1-hour JWT      |
| `GET`    | `/v1/me`           | Return current user info                  |
| `POST`   | `/v1/tunnels`      | Register a new tunnel, reserve a hostname |
| `GET`    | `/v1/tunnels`      | List your active tunnels                  |
| `DELETE` | `/v1/tunnels/:id`  | Close a tunnel                            |
| `POST`   | `/v1/api-keys`     | Create a new API key                      |
| `GET`    | `/v1/api-keys`     | List active API keys                      |
| `DELETE` | `/v1/api-keys/:id` | Revoke an API key                         |
| `GET`    | `/health`          | Liveness check                            |

---

## Limits & Lifecycle

### Tunnel cap

Each user may have a maximum of **2 active tunnels** at a time. A third `tunnel http` call will fail with:

```
error: tunnel limit reached — maximum 2 active tunnels per user
```

Close an existing tunnel (Ctrl+C) to free up a slot.

### Tunnel statuses

| Status    | Description                                                |
| --------- | ---------------------------------------------------------- |
| `pending` | Registered via control plane, waiting for agent to connect |
| `active`  | Agent connected and relay is live                          |
| `closed`  | Disconnected, deleted, or expired                          |

Only `pending` and `active` tunnels count toward your limit.

### Token expiry

Session tokens expire after **1 hour**. If your tunnel disconnects with an `invalid token` error, re-authenticate:

```bash
tunnel login --api-key <your-key>
```

### Automatic cleanup

- `pending` tunnels that never connect are expired automatically after 60 seconds.
- `active` tunnels that miss heartbeats are reaped and marked `closed`.
- Closed tunnels free your slot immediately.

---

## Security

| Concern             | Approach                                                           |
| ------------------- | ------------------------------------------------------------------ |
| API key storage     | Hashed at rest with bcrypt/Argon2 — plaintext is never stored      |
| Session tokens      | Short-lived JWTs (1 hour TTL) to limit blast radius                |
| Transport           | All agent-to-relay traffic is TLS-encrypted                        |
| Request logs        | `Authorization` and `Cookie` headers are redacted by default       |
| Tunnel cap          | 2-tunnel limit enforced transactionally to prevent race conditions |
| Hostname collisions | Unique random subdomains assigned by the control plane             |
