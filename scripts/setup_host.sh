#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# NerdHand — Enroll the host machine (Mac/PC running daemon.py) into NetBird
#
# Run on your Mac/PC after the VPS is up:
#   bash tunnel/scripts/setup_host.sh
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

echo "════════════════════════════════════════════"
echo "  NerdHand — Host machine NetBird setup"
echo "════════════════════════════════════════════"

read -rp "Enter your NetBird management URL (e.g. https://netbird.example.com:33073): " MGMT_URL
read -rp "Enter your setup key (from the NetBird dashboard): " SETUP_KEY

OS="$(uname -s)"

# ── Install NetBird client ────────────────────────────────────────────────────
if command -v netbird &>/dev/null; then
    echo "→ NetBird already installed"
else
    echo "→ Installing NetBird client..."
    case "$OS" in
        Darwin)
            if command -v brew &>/dev/null; then
                brew install netbirdio/tap/netbird
            else
                echo "Homebrew not found. Install from: https://netbird.io/docs/installation/macos"
                exit 1
            fi
            ;;
        Linux)
            curl -fsSL https://pkgs.netbird.io/install.sh | sh
            ;;
        *)
            echo "Unsupported OS: $OS. Install NetBird manually: https://netbird.io/docs/installation"
            exit 1
            ;;
    esac
fi

# ── Connect to your self-hosted management server ────────────────────────────
echo "→ Connecting to $MGMT_URL ..."
netbird up \
    --management-url "$MGMT_URL" \
    --setup-key "$SETUP_KEY"

echo ""
echo "════════════════════════════════════════════"
echo "  Host enrolled!  NetBird IP:"
netbird status | grep -E "NetBird IP|IP:" | head -1
echo ""
echo "  Your phone can now reach this machine"
echo "  via the NetBird IP once you install the"
echo "  NetBird app on your phone and join the"
echo "  same network."
echo ""
echo "  iOS:     https://apps.apple.com/app/id1557638990"
echo "  Android: https://play.google.com/store/apps/details?id=io.netbird.client"
echo "════════════════════════════════════════════"
