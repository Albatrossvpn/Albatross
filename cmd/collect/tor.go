package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// moat is the Tor Project's bridge API, the one Tor Browser's connection
// assist uses. The collector reaches it directly, outside the filtered
// network, and publishes the result so the apps never need to.
const moat = "https://bridges.torproject.org/moat/circumvention/"

// torBridges returns Tor's current built-in bridges plus the ones the Tor
// Project recommends for Iran, as transport → bridge lines.
func torBridges(ctx context.Context, c *http.Client) (map[string][]string, error) {
	out := map[string][]string{}
	var builtin map[string][]string
	if err := moatPost(ctx, c, "builtin", map[string]any{}, &builtin); err != nil {
		return nil, err
	}
	for k, v := range builtin {
		out[k] = append(out[k], v...)
	}
	var ir struct {
		Settings []struct {
			Bridges struct {
				Type    string   `json:"type"`
				Strings []string `json:"bridge_strings"`
			} `json:"bridges"`
		} `json:"settings"`
	}
	// Country settings are a bonus; the built-in list alone is enough.
	if moatPost(ctx, c, "settings", map[string]any{"country": "ir", "transports": []string{"obfs4", "snowflake", "webtunnel", "meek", "meek_lite"}}, &ir) == nil {
		for _, s := range ir.Settings {
			if s.Bridges.Type != "" {
				out[s.Bridges.Type] = append(s.Bridges.Strings, out[s.Bridges.Type]...)
			}
		}
	}
	for k, v := range out {
		seen := map[string]bool{}
		keep := v[:0]
		for _, l := range v {
			if l != "" && !seen[l] {
				seen[l] = true
				keep = append(keep, l)
			}
		}
		out[k] = keep
	}
	return out, nil
}

func moatPost(ctx context.Context, c *http.Client, path string, body, v any) error {
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", moat+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/vnd.api+json")
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("moat %s: %s", path, resp.Status)
	}
	return json.Unmarshal(data, v)
}
