package engine

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// warpID is a free Cloudflare WARP device. Every connection can run WARP's
// WireGuard tunnel through the server in use, so the server's owner sees
// only encrypted WARP traffic and sites see a clean Cloudflare address
// (fewer captchas, Google and streaming work better). watchWarpExit decides
// when it is worth it; there is no setting.
type warpID struct {
	PrivateKey string `json:"private_key"`
	PeerKey    string `json:"peer_key"`
	V4         string `json:"v4"`
	V6         string `json:"v6"`
	Reserved   []int  `json:"reserved"`
}

// warpEndpoint is WARP's own endpoint; any of its addresses works.
const warpEndpoint, warpPort = "162.159.192.1", 2408

func (e *Engine) warpPath() string { return filepath.Join(e.dir, "warp-exit.json") }

// warpExit returns the saved WARP device, or nil.
func (e *Engine) warpExit() *warpID {
	b, err := os.ReadFile(e.warpPath())
	if err != nil {
		return nil
	}
	var w warpID
	if json.Unmarshal(b, &w) != nil || w.PrivateKey == "" || w.PeerKey == "" || w.V4 == "" {
		return nil
	}
	return &w
}

// registerWarp signs a new device up with WARP's API (anonymous, free)
// through client and saves it.
func (e *Engine) registerWarp(ctx context.Context, client *http.Client) error {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{
		"key": base64.StdEncoding.EncodeToString(key.PublicKey().Bytes()), "install_id": "", "fcm_token": "",
		"tos": time.Now().UTC().Format(time.RFC3339), "model": "PC", "locale": "en_US",
	})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.cloudflareclient.com/v0a2158/reg", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "okhttp/3.12.1")
	req.Header.Set("CF-Client-Version", "a-6.30-3596")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("WARP sign-up: HTTP %d", resp.StatusCode)
	}
	var r struct {
		Config struct {
			ClientID  string `json:"client_id"`
			Interface struct {
				Addresses struct {
					V4 string `json:"v4"`
					V6 string `json:"v6"`
				} `json:"addresses"`
			} `json:"interface"`
			Peers []struct {
				PublicKey string `json:"public_key"`
			} `json:"peers"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return err
	}
	if len(r.Config.Peers) == 0 || r.Config.Interface.Addresses.V4 == "" {
		return fmt.Errorf("WARP sign-up: incomplete answer")
	}
	w := warpID{
		PrivateKey: base64.StdEncoding.EncodeToString(key.Bytes()),
		PeerKey:    r.Config.Peers[0].PublicKey,
		V4:         r.Config.Interface.Addresses.V4,
		V6:         r.Config.Interface.Addresses.V6,
	}
	if id, err := base64.StdEncoding.DecodeString(r.Config.ClientID); err == nil && len(id) == 3 {
		w.Reserved = []int{int(id[0]), int(id[1]), int(id[2])}
	}
	b, _ := json.Marshal(w)
	return os.WriteFile(e.warpPath(), b, 0o600)
}

// warpOutbound is the WARP endpoint dialled through the "proxy" selector.
func (w *warpID) endpoint() map[string]any {
	addr := []string{w.V4 + "/32"}
	if w.V6 != "" {
		addr = append(addr, w.V6+"/128")
	}
	peer := map[string]any{
		"address": warpEndpoint, "port": warpPort, "public_key": w.PeerKey,
		"allowed_ips": []string{"0.0.0.0/0", "::/0"},
	}
	if len(w.Reserved) == 3 {
		peer["reserved"] = w.Reserved
	}
	return map[string]any{
		"type": "wireguard", "tag": "warp-exit", "mtu": 1280, "address": addr,
		"private_key": w.PrivateKey, "peers": []any{peer}, "detour": "proxy",
	}
}

// warpMaxDelay is the slowest WARP answer worth sending traffic through.
const warpMaxDelay = 1500

// watchWarpExit moves traffic onto WARP when it answers through the server
// in use and is not much slower than the server alone, and back when it
// stops answering (servers that do not carry UDP cannot carry WARP) or
// falls well behind. The first connection signs a device up quietly; the
// next ones use it.
func (e *Engine) watchWarpExit(ctx context.Context, c *connection) {
	if !c.warp {
		// Test runs (Version "test") do not sign devices up.
		if e.warpExit() == nil && e.opt.Version != "test" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
			}
			if err := e.registerWarp(ctx, e.proxyClient()); err != nil {
				e.logf("info", "WARP is not available: %v", err)
				return
			}
			e.logf("info", "WARP is set up; connections from now on can use it.")
		}
		return
	}
	on, fails := false, 0
	for {
		r, ok := c.testTag(ctx, "warp-exit", 8*time.Second)
		c.mu.Lock()
		direct := c.delay
		c.mu.Unlock()
		// Worth it only while it costs next to nothing: at most a fifth or
		// 150 ms over the server's own delay, and never on a slow line,
		// where every added hop is felt. Unknown server delay means no.
		cheap := direct > 0 && r <= direct+max(150, direct/5) && r <= warpMaxDelay
		switch {
		case ok && !on && cheap:
			if _, err := c.api(ctx, http.MethodPut, "/proxies/exit", map[string]string{"name": "warp-exit"}, nil, 3*time.Second); err == nil {
				on, fails = true, 0
				e.logf("info", "Traffic now leaves through WARP (%d ms).", r)
			}
		case ok && on && cheap:
			fails = 0
		case on:
			if fails++; fails >= 2 {
				if _, err := c.api(ctx, http.MethodPut, "/proxies/exit", map[string]string{"name": "proxy"}, nil, 3*time.Second); err == nil {
					on = false
					e.logf("warn", "WARP does not answer through this server; going through the server alone.")
				}
			}
		}
		wait := time.Minute
		if !on {
			wait = 3 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}
