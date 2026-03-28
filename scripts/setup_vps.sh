#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# NerdHand — VPS bootstrap for NetBird self-hosted
#
# One-liner install (run on a fresh Ubuntu/Debian VPS):
#   curl -fsSL https://raw.githubusercontent.com/rabeeqiblawi/nerdhand-tunnel/main/scripts/setup_vps.sh | bash
#
# Or clone and run:
#   git clone https://github.com/rabeeqiblawi/nerdhand-tunnel /opt/nerdhand-tunnel
#   bash /opt/nerdhand-tunnel/scripts/setup_vps.sh
#
# What this does:
#   1. Installs Docker + Docker Compose
#   2. Opens required firewall ports
#   3. Downloads tunnel config files to /opt/nerdhand-tunnel
#   4. Prompts for your domain + generates secrets
#   5. Starts the NetBird stack
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

INSTALL_DIR="/opt/nerdhand-tunnel"
REPO_RAW="https://raw.githubusercontent.com/rabeeqiblawi/nerdhand-tunnel/main"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || echo "")"
REPO_DIR="$(dirname "$SCRIPT_DIR" 2>/dev/null || echo "")"

echo "════════════════════════════════════════════"
echo "  NerdHand — NetBird VPS Setup"
echo "════════════════════════════════════════════"

# ── 1. Docker ────────────────────────────────────────────────────────────────
if ! command -v docker &>/dev/null; then
    echo "→ Installing Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable --now docker
else
    echo "→ Docker already installed"
fi

if ! docker compose version &>/dev/null; then
    echo "→ Installing Docker Compose plugin..."
    apt-get install -y docker-compose-plugin
fi

# ── 2. Firewall ───────────────────────────────────────────────────────────────
echo "→ Opening firewall ports..."
if command -v ufw &>/dev/null; then
    ufw allow 80/tcp
    ufw allow 443/tcp
    ufw allow 443/udp       # HTTP/3
    ufw allow 3478/udp      # STUN/TURN
    ufw allow 5349/tcp      # TURN over TLS
    ufw allow 5349/udp
    ufw allow 33073/tcp     # NetBird management gRPC
    ufw allow 49152:65535/udp  # TURN relay port range
    ufw --force enable
fi

# ── 3. Download / copy config files ───────────────────────────────────────────
echo "→ Installing tunnel config to $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR"

if [[ -f "$REPO_DIR/docker-compose.yml" ]]; then
    # Running from a local clone
    cp "$REPO_DIR/docker-compose.yml" "$INSTALL_DIR/"
    cp "$REPO_DIR/Caddyfile"          "$INSTALL_DIR/"
    cp "$REPO_DIR/management.json"    "$INSTALL_DIR/"
    cp "$REPO_DIR/turnserver.conf"    "$INSTALL_DIR/"
else
    # Running via curl pipe — download from GitHub
    curl -fsSL "$REPO_RAW/docker-compose.yml" -o "$INSTALL_DIR/docker-compose.yml"
    curl -fsSL "$REPO_RAW/Caddyfile"          -o "$INSTALL_DIR/Caddyfile"
    curl -fsSL "$REPO_RAW/management.json"    -o "$INSTALL_DIR/management.json"
    curl -fsSL "$REPO_RAW/turnserver.conf"    -o "$INSTALL_DIR/turnserver.conf"
fi

# ── 4. Configure ─────────────────────────────────────────────────────────────
echo ""
read -rp "Enter your domain (e.g. netbird.example.com): " DOMAIN
TURN_SECRET=$(openssl rand -hex 32)

# Write .env
cat > "$INSTALL_DIR/.env" <<EOF
NETBIRD_DOMAIN=${DOMAIN}
TURN_SECRET=${TURN_SECRET}
EOF

# Inject domain + secret into config files
sed -i "s/netbird\.your-domain\.com/${DOMAIN}/g" \
    "$INSTALL_DIR/Caddyfile" \
    "$INSTALL_DIR/management.json"

sed -i "s/REPLACE_WITH_TURN_SECRET/${TURN_SECRET}/g" \
    "$INSTALL_DIR/management.json"

# Coturn needs the literal value (not shell var) — replace placeholder
sed -i "s/\${TURN_SECRET}/${TURN_SECRET}/g" \
    "$INSTALL_DIR/turnserver.conf"

# ── 5. Start ──────────────────────────────────────────────────────────────────
echo ""
echo "→ Starting NetBird stack..."
cd "$INSTALL_DIR"
docker compose pull
docker compose up -d

echo ""
echo "════════════════════════════════════════════"
echo "  Done!  NetBird is starting up."
echo ""
echo "  Dashboard: https://${DOMAIN}"
echo "  Management: https://${DOMAIN}:33073"
echo ""
echo "  Next steps:"
echo "  1. Wait ~30s for Caddy to obtain TLS certs"
echo "  2. Open https://${DOMAIN} and create your network"
echo "  3. Run setup_host.sh on your Mac/PC:"
echo "     curl -fsSL ${REPO_RAW}/scripts/setup_host.sh | bash"
echo "  4. Install the NetBird app on your phone"
echo "════════════════════════════════════════════"
