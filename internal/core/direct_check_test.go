package core

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Albatrossvpn/vp/internal/node"
)

// The serverless method's config must be one sing-box accepts: it refuses
// fragment fields on a direct outbound and both fragment options at once.
func TestDirectSetSingBoxAccepts(t *testing.T) {
	set, err := BuildSet([]*node.Node{node.Direct()}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Direct) != 1 || set.Direct[0] != set.Tags[0] {
		t.Fatalf("direct tags: %v", set.Direct)
	}
	rules := FragmentRules(set, func(tag string) map[string]any { return map[string]any{"auth_user": []string{tag}} })
	if len(rules) != 1 || rules[0].(map[string]any)["tls_fragment"] != true || rules[0].(map[string]any)["tls_record_fragment"] != nil {
		t.Fatalf("rules: %v", rules)
	}
	if all := FragmentRules(set, nil); len(all) != 1 || len(all[0].(map[string]any)) != 2 {
		t.Fatalf("live rule: %v", all)
	}
	sb := os.Getenv("SING_BOX")
	if sb == "" {
		if p, err := exec.LookPath("sing-box"); err == nil {
			sb = p
		}
	}
	if sb == "" {
		t.Skip("no sing-box to check the config with (set SING_BOX)")
	}
	cfg := map[string]any{
		"inbounds":  []any{map[string]any{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 18099, "users": []any{map[string]any{"username": set.Tags[0], "password": "p"}}}},
		"outbounds": set.Outbounds,
		"route":     map[string]any{"rules": append(rules, map[string]any{"auth_user": []string{set.Tags[0]}, "outbound": set.Tags[0]})},
	}
	b, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(path, b, 0o600)
	if out, err := exec.Command(sb, "check", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("sing-box refuses the serverless config: %v\n%s", err, out)
	}
}
