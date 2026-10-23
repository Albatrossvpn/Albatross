package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

// globalpingAPI runs measurements from volunteer probes around the world,
// free and without an account (GLOBALPING_TOKEN, when set, raises the
// hourly quota).
var globalpingAPI = "https://api.globalping.io/v1/measurements"

// iranReach asks probes inside Iran whether each address answers a TCP
// connection. The collector's own test runs from the US and cannot see
// Iran's filtering; this can. Addresses missing from the result were not
// checked (no probe free, quota used up).
func iranReach(ctx context.Context, endpoints []string, probes int) map[string]bool {
	out := map[string]bool{}
	var mu sync.Mutex
	stop := make(chan struct{})
	var once sync.Once
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		select {
		case <-stop:
		case sem <- struct{}{}:
			wg.Add(1)
			go func(ep string) {
				defer func() { <-sem; wg.Done() }()
				open, err := tcpFromIran(ctx, ep, probes)
				if err != nil {
					if err == errNoProbes || err == errQuota {
						once.Do(func() { log.Printf("globalping: %v", err); close(stop) })
					}
					return
				}
				mu.Lock()
				out[ep] = open
				mu.Unlock()
			}(ep)
			continue
		}
		break
	}
	wg.Wait()
	return out
}

var (
	errNoProbes = fmt.Errorf("no probes in Iran right now")
	errQuota    = fmt.Errorf("hourly quota used up")
)

func tcpFromIran(ctx context.Context, endpoint string, probes int) (bool, error) {
	host, portS, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false, err
	}
	port, _ := strconv.Atoi(portS)
	body, _ := json.Marshal(map[string]any{
		"type":               "ping",
		"target":             host,
		"locations":          []any{map[string]any{"country": "IR"}},
		"limit":              probes,
		"measurementOptions": map[string]any{"packets": 2, "protocol": "TCP", "port": port},
	})
	var created struct {
		ID string `json:"id"`
	}
	status, err := gpDo(ctx, http.MethodPost, globalpingAPI, body, &created)
	switch {
	case status == http.StatusUnprocessableEntity:
		return false, errNoProbes
	case status == http.StatusTooManyRequests:
		return false, errQuota
	case err != nil:
		return false, err
	}
	var m struct {
		Status  string `json:"status"`
		Results []struct {
			Result struct {
				Stats struct {
					Rcv int `json:"rcv"`
				} `json:"stats"`
			} `json:"result"`
		} `json:"results"`
	}
	for i := 0; i < 30; i++ {
		time.Sleep(time.Second)
		if _, err := gpDo(ctx, http.MethodGet, globalpingAPI+"/"+created.ID, nil, &m); err != nil {
			return false, err
		}
		if m.Status != "in-progress" {
			break
		}
	}
	if m.Status != "finished" || len(m.Results) == 0 {
		return false, fmt.Errorf("measurement %s", m.Status)
	}
	for _, r := range m.Results {
		if r.Result.Stats.Rcv > 0 {
			return true, nil
		}
	}
	return false, nil
}

func gpDo(ctx context.Context, method, url string, body []byte, out any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t := os.Getenv("GLOBALPING_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
}

// iranFirst orders hot servers by what the Iranian probes saw: open there
// first, then unchecked, then those that did not answer. Nothing is
// dropped, since a few probes cannot speak for every operator.
func iranFirst(hot []*entry, seen map[string]bool) []*entry {
	rank := func(e *entry) int {
		open, ok := seen[e.endpoint]
		switch {
		case ok && open:
			return 0
		case !ok:
			return 1
		}
		return 2
	}
	sort.SliceStable(hot, func(i, j int) bool { return rank(hot[i]) < rank(hot[j]) })
	return hot
}
