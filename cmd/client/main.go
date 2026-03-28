package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ─── Config paths ────────────────────────────────────────────────────────────

func nerdhandDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".nerdhand")
}

func wgKeyPath() string    { return filepath.Join(nerdhandDir(), "wg_key.json") }
func tunnelPath() string   { return filepath.Join(nerdhandDir(), "tunnel.json") }
func envFilePath() string  { return filepath.Join(nerdhandDir(), ".env") }
func wgConfPath() string   { return "/tmp/wg0.conf" }

// ─── Key management ──────────────────────────────────────────────────────────

type WGKeyPair struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
}

func loadOrGenerateKeyPair() (*WGKeyPair, error) {
	if data, err := os.ReadFile(wgKeyPath()); err == nil {
		var kp WGKeyPair
		if err := json.Unmarshal(data, &kp); err == nil && kp.PrivateKey != "" {
			return &kp, nil
		}
	}

	// Generate new keypair using wg genkey / wg pubkey.
	privOut, err := exec.Command("wg", "genkey").Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error: 'wg' command not found.")
		fmt.Fprintln(os.Stderr, "Install WireGuard:")
		fmt.Fprintln(os.Stderr, "  macOS:   brew install wireguard-tools")
		fmt.Fprintln(os.Stderr, "  Ubuntu:  sudo apt install wireguard-tools")
		fmt.Fprintln(os.Stderr, "  Fedora:  sudo dnf install wireguard-tools")
		os.Exit(1)
	}

	privateKey := strings.TrimSpace(string(privOut))

	pubCmd := exec.Command("wg", "pubkey")
	pubCmd.Stdin = strings.NewReader(privateKey)
	pubOut, err := pubCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("wg pubkey failed: %w", err)
	}
	publicKey := strings.TrimSpace(string(pubOut))

	kp := &WGKeyPair{PrivateKey: privateKey, PublicKey: publicKey}

	if err := os.MkdirAll(nerdhandDir(), 0700); err != nil {
		return nil, fmt.Errorf("creating ~/.nerdhand: %w", err)
	}
	data, _ := json.MarshalIndent(kp, "", "  ")
	if err := os.WriteFile(wgKeyPath(), data, 0600); err != nil {
		return nil, fmt.Errorf("saving wg keys: %w", err)
	}

	return kp, nil
}

// ─── Tunnel state ─────────────────────────────────────────────────────────────

type TunnelState struct {
	PeerID   string `json:"peer_id"`
	TunnelIP string `json:"tunnel_ip"`
}

func saveTunnelState(state *TunnelState) error {
	if err := os.MkdirAll(nerdhandDir(), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	return os.WriteFile(tunnelPath(), data, 0600)
}

func loadTunnelState() (*TunnelState, error) {
	data, err := os.ReadFile(tunnelPath())
	if err != nil {
		return nil, fmt.Errorf("tunnel state not found — run 'client start' first: %w", err)
	}
	var state TunnelState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("corrupt tunnel.json: %w", err)
	}
	return &state, nil
}

// ─── .env file reader ─────────────────────────────────────────────────────────

// readEnvFile reads key=value lines from a file.
func readEnvFile(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func apiPost(url, secret string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-App-Secret", secret)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

func apiGet(url, secret string, out any) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-App-Secret", secret)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		return json.Unmarshal(respBody, out)
	}
	return nil
}

// ─── Public IP discovery ──────────────────────────────────────────────────────

func getPublicIP() (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		return "", fmt.Errorf("could not get public IP: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(body))
	if ip == "" {
		return "", fmt.Errorf("empty response from ipify")
	}
	return ip, nil
}

// ─── WireGuard setup ──────────────────────────────────────────────────────────

const wgListenPort = 51820

func writeWGConfig(privateKey, tunnelIP string) error {
	config := fmt.Sprintf(`[Interface]
PrivateKey = %s
Address = %s/24
ListenPort = %d
`, privateKey, tunnelIP, wgListenPort)
	return os.WriteFile(wgConfPath(), []byte(config), 0600)
}

func runSudo(args ...string) error {
	cmd := exec.Command("sudo", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func bringUpWireGuard() error {
	return runSudo("wg-quick", "up", wgConfPath())
}

func bringDownWireGuard() error {
	return runSudo("wg-quick", "down", wgConfPath())
}

func addWGPeer(publicKey, allowedIP string) error {
	return runSudo("wg", "set", "wg0", "peer", publicKey, "allowed-ips", allowedIP+"/32")
}

func checkWireGuardInstalled() {
	if _, err := exec.LookPath("wg-quick"); err != nil {
		fmt.Fprintln(os.Stderr, "Error: 'wg-quick' not found.")
		fmt.Fprintln(os.Stderr, "Install WireGuard tools:")
		switch runtime.GOOS {
		case "darwin":
			fmt.Fprintln(os.Stderr, "  brew install wireguard-tools")
		case "linux":
			fmt.Fprintln(os.Stderr, "  sudo apt install wireguard-tools   # Debian/Ubuntu")
			fmt.Fprintln(os.Stderr, "  sudo dnf install wireguard-tools   # Fedora")
		default:
			fmt.Fprintln(os.Stderr, "  See https://www.wireguard.com/install/")
		}
		os.Exit(1)
	}
}

// ─── Subcommands ─────────────────────────────────────────────────────────────

// parseFlag reads a named flag from os.Args, e.g. --server https://...
// Returns the value or "" if not found.
func parseFlag(name string) string {
	flag := "--" + name
	args := os.Args[2:] // skip binary name + subcommand
	for i, arg := range args {
		if arg == flag && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, flag+"=") {
			return strings.SplitN(arg, "=", 2)[1]
		}
	}
	return ""
}

func requireFlag(name string) string {
	v := parseFlag(name)
	if v == "" {
		fmt.Fprintf(os.Stderr, "Error: --%s is required\n", name)
		os.Exit(1)
	}
	return v
}

// cmdStart implements `client start`
func cmdStart() {
	serverURL := requireFlag("server")
	appSecret := requireFlag("secret")

	checkWireGuardInstalled()

	fmt.Println("Loading WireGuard keypair...")
	kp, err := loadOrGenerateKeyPair()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading keys: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  Key ready:", kp.PublicKey[:12]+"...")

	fmt.Println("Discovering public IP...")
	publicIP, err := getPublicIP()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  Public IP:", publicIP)

	fmt.Println("Registering with signaling server...")
	var regResp struct {
		PeerID   string `json:"peer_id"`
		TunnelIP string `json:"tunnel_ip"`
	}
	err = apiPost(serverURL+"/v1/peer/register", appSecret, map[string]any{
		"wg_public_key": kp.PublicKey,
		"listen_port":   wgListenPort,
		"endpoint_ip":   publicIP,
	}, &regResp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Registration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  Registered — peer_id: %s  tunnel_ip: %s\n", regResp.PeerID, regResp.TunnelIP)

	if err := saveTunnelState(&TunnelState{
		PeerID:   regResp.PeerID,
		TunnelIP: regResp.TunnelIP,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving tunnel state: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Writing WireGuard config to", wgConfPath())
	if err := writeWGConfig(kp.PrivateKey, regResp.TunnelIP); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing WireGuard config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Bringing up WireGuard interface (requires sudo)...")
	if err := bringUpWireGuard(); err != nil {
		fmt.Fprintf(os.Stderr, "wg-quick up failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "If the interface already exists, run 'client stop' first.\n")
		os.Exit(1)
	}

	fmt.Printf("\n  Tunnel active — tunnel IP: %s\n\n", regResp.TunnelIP)
	fmt.Println("Polling for new clients every 5 seconds. Press Ctrl+C to stop.")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	type clientEntry struct {
		WGPublicKey string    `json:"wg_public_key"`
		TunnelIP    string    `json:"tunnel_ip"`
		AddedAt     time.Time `json:"added_at"`
	}

	for range ticker.C {
		var clients []clientEntry
		pollURL := fmt.Sprintf("%s/v1/peer/clients?peer_id=%s", serverURL, regResp.PeerID)
		if err := apiGet(pollURL, appSecret, &clients); err != nil {
			fmt.Fprintf(os.Stderr, "Poll error: %v\n", err)
			continue
		}

		for _, c := range clients {
			fmt.Printf("  New client: %s (tunnel %s) — adding WireGuard peer\n", c.WGPublicKey[:12]+"...", c.TunnelIP)
			if err := addWGPeer(c.WGPublicKey, c.TunnelIP); err != nil {
				fmt.Fprintf(os.Stderr, "  Warning: failed to add peer %s: %v\n", c.WGPublicKey[:12]+"...", err)
			} else {
				fmt.Printf("  Peer added successfully.\n")
			}
		}
	}
}

// cmdPair implements `client pair`
func cmdPair() {
	serverURL := requireFlag("server")
	appSecret := requireFlag("secret")

	// Daemon token: prefer --token flag, then ~/.nerdhand/.env
	daemonToken := parseFlag("token")
	if daemonToken == "" {
		env := readEnvFile(envFilePath())
		if env != nil {
			if v, ok := env["DAEMON_TOKEN"]; ok {
				daemonToken = v
			} else if v, ok := env["API_TOKEN"]; ok {
				daemonToken = v
			}
		}
	}

	state, err := loadTunnelState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	var resp struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
	}
	err = apiPost(serverURL+"/v1/pair/generate", appSecret, map[string]any{
		"peer_id":      state.PeerID,
		"daemon_token": daemonToken,
		"daemon_port":  8000,
	}, &resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Pair generate failed: %v\n", err)
		os.Exit(1)
	}

	minutes := resp.ExpiresIn / 60

	fmt.Println()
	fmt.Println("┌─────────────────────────────────┐")
	fmt.Printf("│  Pairing Code: %-17s│\n", resp.Code)
	fmt.Printf("│  Expires in:   %-4d minutes    │\n", minutes)
	fmt.Println("└─────────────────────────────────┘")
	fmt.Println()
	fmt.Println("Enter this code in the Terminator app → Add Computer → Online")
	fmt.Println()
}

// cmdStop implements `client stop`
func cmdStop() {
	fmt.Println("Bringing down WireGuard interface...")
	if err := bringDownWireGuard(); err != nil {
		fmt.Fprintf(os.Stderr, "wg-quick down failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Tunnel stopped.")
}

// ─── Usage ────────────────────────────────────────────────────────────────────

func usage() {
	fmt.Println(`nerdhand-tunnel client — WireGuard P2P manager

Usage:
  client start  --server <url> --secret <app-secret>
  client pair   --server <url> --secret <app-secret> [--token <daemon-token>]
  client stop

Commands:
  start   Register this PC with the signaling server and bring up WireGuard.
          Polls continuously for new clients to add as WireGuard peers.

  pair    Generate a pairing code to show in the Terminator app.
          Reads daemon token from --token flag or ~/.nerdhand/.env
          (DAEMON_TOKEN or API_TOKEN key).

  stop    Tear down the WireGuard tunnel.

Examples:
  client start --server https://signal.example.com --secret mysecret
  client pair  --server https://signal.example.com --secret mysecret
  client pair  --server https://signal.example.com --secret mysecret --token mytoken
  client stop
`)
}

// ─── Main ─────────────────────────────────────────────────────────────────────

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart()
	case "pair":
		cmdPair()
	case "stop":
		cmdStop()
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", os.Args[1])
		usage()
		os.Exit(1)
	}
}
