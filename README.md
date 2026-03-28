# nerdhand-tunnel

Lightweight P2P WireGuard signaling system for the NerdHand companion app.
The VPS only brokers key exchange — all app traffic flows directly between the phone and PC over an encrypted WireGuard tunnel.

## Architecture

```
[Flutter App] <-- WireGuard P2P --> [PC Daemon :8000]
                    ^
        only key-exchange via VPS
                    ^
              [Signaling Server]
```

- The VPS signaling server exchanges public keys and pairing codes only. It never routes traffic.
- After pairing, the Flutter app connects directly to the PC over WireGuard.
- Only requests bearing the correct `X-App-Secret` header are accepted — no other client can use the API.

## Directory layout

```
nerdhand-tunnel/
├── cmd/
│   ├── server/main.go     ← signaling server (runs on VPS in Docker)
│   └── client/main.go     ← PC WireGuard manager + signaling client
├── go.mod
├── Dockerfile
├── docker-compose.yml
└── .env.example
```

---

## Running the VPS signaling server

### Prerequisites
- Docker and Docker Compose installed on the VPS
- Port 8080 (or your chosen PORT) open in the firewall

### Steps

```bash
git clone https://github.com/rabeeqiblawi/nerd-hand-tunnel /opt/nerdhand-tunnel
cd /opt/nerdhand-tunnel

cp .env.example .env
# Edit .env and set a strong APP_SECRET
nano .env

docker compose up -d
```

Check it is running:

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

---

## Running the PC client

### Prerequisites
- Go 1.22+ installed (to build), or use a pre-built binary
- `wireguard-tools` installed:
  - macOS: `brew install wireguard-tools`
  - Ubuntu/Debian: `sudo apt install wireguard-tools`
  - Fedora: `sudo dnf install wireguard-tools`

### Build

```bash
cd /opt/nerdhand-tunnel
go build -o client ./cmd/client
```

### Start the tunnel

```bash
./client start \
  --server https://signal.your-domain.com \
  --secret your-app-secret
```

This will:
1. Generate (or load) a WireGuard keypair from `~/.nerdhand/wg_key.json`
2. Discover your public IP via ipify
3. Register with the signaling server and get a tunnel IP (e.g. `10.99.0.1`)
4. Write a WireGuard config to `/tmp/wg0.conf` and bring up the `wg0` interface
5. Poll every 5 seconds for new clients, adding them as WireGuard peers automatically

### Generate a pairing code

In a second terminal (while `client start` is still running):

```bash
./client pair \
  --server https://signal.your-domain.com \
  --secret your-app-secret \
  --token your-daemon-bearer-token
```

Output example:

```
┌─────────────────────────────────┐
│  Pairing Code: K7M-4XQ          │
│  Expires in:   5    minutes    │
└─────────────────────────────────┘

Enter this code in the Terminator app → Add Computer → Online
```

The daemon token can also be stored in `~/.nerdhand/.env` as `DAEMON_TOKEN=...` or `API_TOKEN=...` and it will be read automatically.

### Stop the tunnel

```bash
./client stop
```

---

## How pairing works

1. **PC runs `client start`** — registers its WireGuard public key and public IP with the signaling server. Gets assigned a tunnel IP (e.g. `10.99.0.1`).
2. **PC runs `client pair`** — generates a short-lived code (5 minutes) like `K7M-4XQ`.
3. **User enters code in the Flutter app** — the app calls `POST /v1/pair/redeem` with the code and its own WireGuard public key.
4. **Server responds** with the PC's public key, endpoint, tunnel IP, and a full WireGuard `.conf` file for the app to import. The app gets its own tunnel IP (e.g. `10.99.0.3`).
5. **PC polls `GET /v1/peer/clients`** — sees the new client, runs `wg set wg0 peer ...` to add it as a WireGuard peer.
6. **Direct P2P link established** — the app can now reach the PC daemon at `10.99.0.1:8000` over WireGuard.

---

## Security model

- All API endpoints (except `/health`) require the `X-App-Secret` header matching the `APP_SECRET` environment variable.
- Missing or wrong secret returns `403 Forbidden`.
- Pairing codes expire after 5 minutes and can only be redeemed once.
- The server is stateless — it holds no private keys, no traffic, no credentials beyond the daemon token passed through during pairing.

---

## API reference

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/health` | Health check (no auth) |
| `POST` | `/v1/peer/register` | PC registers its WireGuard key + endpoint |
| `POST` | `/v1/pair/generate` | PC generates a pairing code |
| `POST` | `/v1/pair/redeem` | App redeems pairing code, gets WireGuard config |
| `GET` | `/v1/peer/clients` | PC polls for newly paired clients |

All authenticated endpoints require: `X-App-Secret: <APP_SECRET>`

---

## Useful commands

```bash
# VPS — view server logs
docker compose logs -f signaling

# VPS — restart server
docker compose restart signaling

# PC — check WireGuard status
sudo wg show

# PC — manually add a peer
sudo wg set wg0 peer <public_key> allowed-ips 10.99.0.X/32
```
