package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ─── Data models ────────────────────────────────────────────────────────────

type Peer struct {
	ID          string    `json:"peer_id"`
	WGPublicKey string    `json:"wg_public_key"`
	ListenPort  int       `json:"listen_port"`
	EndpointIP  string    `json:"endpoint_ip"`
	TunnelIP    string    `json:"tunnel_ip"`
	CreatedAt   time.Time `json:"created_at"`
}

type PairingCode struct {
	Code        string    `json:"code"`
	PeerID      string    `json:"peer_id"`
	DaemonToken string    `json:"daemon_token"`
	DaemonPort  int       `json:"daemon_port"`
	ExpiresAt   time.Time `json:"expires_at"`
	Redeemed    bool      `json:"redeemed"`
}

type Client struct {
	WGPublicKey string    `json:"wg_public_key"`
	TunnelIP    string    `json:"tunnel_ip"`
	AddedAt     time.Time `json:"added_at"`
	Notified    bool      `json:"-"`
}

// ─── In-memory store ────────────────────────────────────────────────────────

type Store struct {
	mu        sync.RWMutex
	peers     map[string]*Peer   // peer_id → Peer
	codes     map[string]*PairingCode // code → PairingCode
	clients   map[string][]*Client    // peer_id → []Client
	ipCounter int
}

func NewStore() *Store {
	return &Store{
		peers:     make(map[string]*Peer),
		codes:     make(map[string]*PairingCode),
		clients:   make(map[string][]*Client),
		ipCounter: 0,
	}
}

// nextTunnelIP assigns the next sequential IP in the 10.99.0.0/24 range.
// Counter starts at 1 → 10.99.0.1
func (s *Store) nextTunnelIP() string {
	s.ipCounter++
	return fmt.Sprintf("10.99.0.%d", s.ipCounter)
}

func (s *Store) cleanExpiredCodes() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for code, pc := range s.codes {
		if now.After(pc.ExpiresAt) && !pc.Redeemed {
			delete(s.codes, code)
		}
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func randomHex(n int) string {
	b := make([]byte, n/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// generatePairingCode creates a code like "K7M-4XQ".
// Character set: uppercase letters + digits, excluding visually ambiguous chars.
func generatePairingCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	pick := func(n int) string {
		out := make([]byte, n)
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		for i := 0; i < n; i++ {
			out[i] = charset[int(buf[i])%len(charset)]
		}
		return string(out)
	}
	return pick(3) + "-" + pick(3)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// ─── Auth middleware ─────────────────────────────────────────────────────────

func authMiddleware(secret string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-App-Secret") != secret {
			writeError(w, http.StatusForbidden, "forbidden: invalid or missing X-App-Secret")
			return
		}
		next(w, r)
	}
}

// ─── Handlers ────────────────────────────────────────────────────────────────

// POST /v1/peer/register
func (s *Store) handlePeerRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		WGPublicKey string `json:"wg_public_key"`
		ListenPort  int    `json:"listen_port"`
		EndpointIP  string `json:"endpoint_ip"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.WGPublicKey == "" {
		writeError(w, http.StatusBadRequest, "wg_public_key is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if a peer with this public key already exists, return existing record.
	for _, p := range s.peers {
		if p.WGPublicKey == req.WGPublicKey {
			writeJSON(w, http.StatusOK, map[string]string{
				"peer_id":   p.ID,
				"tunnel_ip": p.TunnelIP,
			})
			return
		}
	}

	peerID := randomHex(16)
	tunnelIP := s.nextTunnelIP()

	peer := &Peer{
		ID:          peerID,
		WGPublicKey: req.WGPublicKey,
		ListenPort:  req.ListenPort,
		EndpointIP:  req.EndpointIP,
		TunnelIP:    tunnelIP,
		CreatedAt:   time.Now(),
	}
	s.peers[peerID] = peer
	s.clients[peerID] = []*Client{}

	log.Printf("[register] peer_id=%s tunnel_ip=%s endpoint=%s", peerID, tunnelIP, req.EndpointIP)

	writeJSON(w, http.StatusCreated, map[string]string{
		"peer_id":   peerID,
		"tunnel_ip": tunnelIP,
	})
}

// POST /v1/pair/generate
func (s *Store) handlePairGenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		PeerID      string `json:"peer_id"`
		DaemonToken string `json:"daemon_token"`
		DaemonPort  int    `json:"daemon_port"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.PeerID == "" {
		writeError(w, http.StatusBadRequest, "peer_id is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.peers[req.PeerID]; !ok {
		writeError(w, http.StatusNotFound, "peer not found")
		return
	}

	// Remove any existing (unredeemed) code for this peer.
	for code, pc := range s.codes {
		if pc.PeerID == req.PeerID && !pc.Redeemed {
			delete(s.codes, code)
		}
	}

	code := generatePairingCode()
	// Ensure uniqueness (extremely unlikely collision but be safe).
	for {
		if _, exists := s.codes[code]; !exists {
			break
		}
		code = generatePairingCode()
	}

	expiresAt := time.Now().Add(5 * time.Minute)
	s.codes[code] = &PairingCode{
		Code:        code,
		PeerID:      req.PeerID,
		DaemonToken: req.DaemonToken,
		DaemonPort:  req.DaemonPort,
		ExpiresAt:   expiresAt,
	}

	log.Printf("[pair/generate] peer_id=%s code=%s", req.PeerID, code)

	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"expires_in": 300,
	})
}

// POST /v1/pair/redeem
func (s *Store) handlePairRedeem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req struct {
		Code              string `json:"code"`
		ClientWGPublicKey string `json:"client_wg_public_key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Code == "" || req.ClientWGPublicKey == "" {
		writeError(w, http.StatusBadRequest, "code and client_wg_public_key are required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pc, ok := s.codes[req.Code]
	if !ok {
		writeError(w, http.StatusNotFound, "pairing code not found")
		return
	}
	if pc.Redeemed {
		writeError(w, http.StatusGone, "pairing code already redeemed")
		return
	}
	if time.Now().After(pc.ExpiresAt) {
		delete(s.codes, req.Code)
		writeError(w, http.StatusGone, "pairing code expired")
		return
	}

	peer, ok := s.peers[pc.PeerID]
	if !ok {
		writeError(w, http.StatusInternalServerError, "associated peer not found")
		return
	}

	// Assign a tunnel IP to this client.
	clientTunnelIP := s.nextTunnelIP()

	// Store client record for PC to poll.
	client := &Client{
		WGPublicKey: req.ClientWGPublicKey,
		TunnelIP:    clientTunnelIP,
		AddedAt:     time.Now(),
		Notified:    false,
	}
	s.clients[pc.PeerID] = append(s.clients[pc.PeerID], client)

	// Mark code redeemed.
	pc.Redeemed = true

	pcEndpoint := fmt.Sprintf("%s:%d", peer.EndpointIP, peer.ListenPort)

	wgConfig := fmt.Sprintf(`[Interface]
PrivateKey = REPLACE_WITH_YOUR_PRIVATE_KEY
Address = %s/32
DNS = 1.1.1.1

[Peer]
PublicKey = %s
Endpoint = %s
AllowedIPs = %s/32
PersistentKeepalive = 25
`, clientTunnelIP, peer.WGPublicKey, pcEndpoint, peer.TunnelIP)

	log.Printf("[pair/redeem] code=%s peer_id=%s client_tunnel_ip=%s", req.Code, pc.PeerID, clientTunnelIP)

	writeJSON(w, http.StatusOK, map[string]any{
		"pc_wg_public_key":  peer.WGPublicKey,
		"pc_endpoint":       pcEndpoint,
		"pc_tunnel_ip":      peer.TunnelIP,
		"client_tunnel_ip":  clientTunnelIP,
		"daemon_token":      pc.DaemonToken,
		"daemon_port":       pc.DaemonPort,
		"wg_config":         wgConfig,
	})
}

// GET /v1/peer/clients?peer_id=...
func (s *Store) handlePeerClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	peerID := r.URL.Query().Get("peer_id")
	if peerID == "" {
		writeError(w, http.StatusBadRequest, "peer_id query param is required")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	clients, ok := s.clients[peerID]
	if !ok {
		writeError(w, http.StatusNotFound, "peer not found")
		return
	}

	type clientResp struct {
		WGPublicKey string    `json:"wg_public_key"`
		TunnelIP    string    `json:"tunnel_ip"`
		AddedAt     time.Time `json:"added_at"`
	}

	resp := []clientResp{}
	for _, c := range clients {
		if !c.Notified {
			resp = append(resp, clientResp{
				WGPublicKey: c.WGPublicKey,
				TunnelIP:    c.TunnelIP,
				AddedAt:     c.AddedAt,
			})
			c.Notified = true
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// GET /health
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ─── Main ────────────────────────────────────────────────────────────────────

func main() {
	appSecret := os.Getenv("APP_SECRET")
	if appSecret == "" {
		log.Fatal("APP_SECRET environment variable is required")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	store := NewStore()

	// Background cleanup of expired codes every minute.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			store.cleanExpiredCodes()
		}
	}()

	mux := http.NewServeMux()

	// Public endpoint.
	mux.HandleFunc("/health", handleHealth)

	// Authenticated endpoints.
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return authMiddleware(appSecret, h)
	}

	mux.HandleFunc("/v1/peer/register", auth(store.handlePeerRegister))
	mux.HandleFunc("/v1/pair/generate", auth(store.handlePairGenerate))
	mux.HandleFunc("/v1/pair/redeem", auth(store.handlePairRedeem))
	mux.HandleFunc("/v1/peer/clients", auth(store.handlePeerClients))

	// Log all unmatched routes.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeJSON(w, http.StatusOK, map[string]string{
				"service": "nerdhand-tunnel signaling server",
				"version": "1.0.0",
			})
			return
		}
		writeError(w, http.StatusNotFound, "not found")
	})

	addr := ":" + port

	// Apply a simple request logger around the mux.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		// Mask the secret in logs.
		secret := r.Header.Get("X-App-Secret")
		masked := "(none)"
		if secret != "" {
			masked = strings.Repeat("*", len(secret))
		}
		log.Printf("%s %s X-App-Secret=%s", r.Method, r.URL.Path, masked)
		mux.ServeHTTP(w, r)
		log.Printf("  -> completed in %s", time.Since(start))
	})

	log.Printf("nerdhand-tunnel signaling server listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
