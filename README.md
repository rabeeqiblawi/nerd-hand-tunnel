# nerdhand-tunnel

Self-hosted [NetBird](https://netbird.io) P2P stack for the NerdHand companion app.
The VPS only brokers the initial handshake — all traffic flows directly between devices over an encrypted WireGuard tunnel.

## Architecture

```
  ┌──────────────┐       WireGuard (direct P2P)        ┌──────────────────┐
  │  Phone       │ ◄─────────────────────────────────► │  Host Mac/PC     │
  │  Flutter app │                                      │  daemon.py :8000 │
  └──────┬───────┘                                      └────────┬─────────┘
         │  handshake only                                       │
         │  (key exchange + ICE)                                 │
         └───────────────────┬───────────────────────────────────┘
                             ▼
                    ┌─────────────────┐
                    │  VPS            │
                    │  NetBird stack  │
                    │  (signaling)    │
                    └─────────────────┘
```

**The VPS only handles the handshake.** Once the two peers exchange keys and negotiate a direct path, all traffic flows peer-to-peer over an encrypted WireGuard tunnel — the VPS sees nothing. Coturn (TURN) only kicks in as a fallback when direct P2P is blocked by symmetric NAT.

## What runs on the VPS

| Container | Purpose |
|-----------|---------|
| `management` | NetBird management server — enrollment, ACLs, key registry |
| `signal` | ICE/STUN signal server — helps peers find each other |
| `coturn` | TURN relay — fallback only, used when direct P2P fails |
| `caddy` | Reverse proxy — auto TLS via Let's Encrypt |

## Directory layout

```
nerdhand-tunnel/
├── docker-compose.yml     ← full NetBird self-hosted stack
├── .env.example           ← required environment variables
├── Caddyfile              ← Caddy reverse-proxy + TLS config
├── management.json        ← NetBird management server config
├── turnserver.conf        ← Coturn STUN/TURN config
└── scripts/
    ├── setup_vps.sh       ← one-shot VPS bootstrap (Docker + firewall + start)
    └── setup_host.sh      ← enroll the host Mac/PC into the NetBird network
```

---

## Setup

### Step 1 — VPS

Point a subdomain (e.g. `netbird.your-domain.com`) at your VPS, then run the one-liner:

```bash
curl -fsSL https://raw.githubusercontent.com/rabeeqiblawi/nerd-hand-tunnel/main/scripts/setup_vps.sh | bash
```

Or clone and run manually:

```bash
git clone https://github.com/rabeeqiblawi/nerd-hand-tunnel /opt/nerdhand-tunnel
bash /opt/nerdhand-tunnel/scripts/setup_vps.sh
```

The script installs Docker, opens firewall ports, fills in your domain, generates secrets, and starts the stack.

**Ports to open on the VPS firewall:**

| Port | Protocol | Purpose |
|------|----------|---------|
| 80, 443 | TCP | Caddy (HTTP + HTTPS) |
| 443 | UDP | HTTP/3 |
| 3478 | UDP | STUN |
| 5349 | TCP/UDP | TURN over TLS |
| 33073 | TCP | NetBird management gRPC |
| 49152–65535 | UDP | TURN relay range |

### Step 2 — Host machine (Mac/PC)

```bash
curl -fsSL https://raw.githubusercontent.com/rabeeqiblawi/nerd-hand-tunnel/main/scripts/setup_host.sh | bash
```

Installs the NetBird client, prompts for your management URL and a setup key (generated in the dashboard), and enrolls the machine. The host gets a stable NetBird IP (e.g. `100.64.0.1`).

### Step 3 — Phone

Install the NetBird app and join the same network using a setup key:

- **iOS**: [App Store](https://apps.apple.com/app/id1557638990)
- **Android**: [Play Store](https://play.google.com/store/apps/details?id=io.netbird.client)

In the app: **Settings → Management URL** → enter `https://netbird.your-domain.com:33073`, then paste a setup key.

### Step 4 — Flutter app pairing

Once both devices are on the NetBird network, pair the NerdHand Flutter app using the **host's NetBird IP** instead of the LAN IP. The host's NetBird IP is visible in `netbird status` or on the dashboard.

---

## Useful commands

```bash
# Check stack status on VPS
cd /opt/nerdhand-tunnel && docker compose ps

# View logs
docker compose logs -f management
docker compose logs -f signal

# Check host enrollment
netbird status

# Stop / restart
docker compose down
docker compose up -d
```
