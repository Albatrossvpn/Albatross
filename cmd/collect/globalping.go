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
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// globalpingAPI runs measurements from volunteer probes around the world,
// free and without an account (GLOBALPING_TOKEN, when set, raises the
// hourly quota).
var globalpingAPI = "https://api.globalping.io/v1/measurements"

// iranReach asks probes inside Iran whether each address answers a TCP
// connection. The collector's own test runs from the US and cannot see
// Iran's filtering; this can. Addresses missing from the result were not
// checked (no probe free, quota used up). The project's own probes (GLOBALPING_TAGS) are asked first;
// when none of them is online, the volunteers' probes in the country take
// over, so the check never hangs on one machine being off.
func iranReach(ctx context.Context, endpoints []string, probes int) map[string]bool {
	out := map[string]bool{}
	var mu sync.Mutex
	stop := make(chan struct{})
	var once, fell sync.Once
	var own atomic.Bool
	own.Store(gpOwnTags() != nil)
	sem := make(chan struct{}, 12)
	var wg sync.WaitGroup
	for _, ep := range endpoints {
		select {
		case <-stop:
		case sem <- struct{}{}:
			wg.Add(1)
			go func(ep string) {
				defer func() { <-sem; wg.Done() }()
				useOwn := own.Load()
				open, err := tcpFromIran(ctx, ep, probes, useOwn)
				if err == errNoProbes && useOwn {
					fell.Do(func() { log.Print("globalping: no own probe online; using the volunteers' probes in Iran") })
					own.Store(false)
					open, err = tcpFromIran(ctx, ep, 2, false)
				}
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

// gpOwnTags returns the tags of the project's own adopted probes inside
// the country (GLOBALPING_TAGS, comma separated, as the dashboard shows
// them, like u-albatrossvpn), or nil when there are none.
func gpOwnTags() []string {
	var list []string
	for _, t := range strings.Split(os.Getenv("GLOBALPING_TAGS"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			list = append(list, t)
		}
	}
	return list
}

// gpLocations picks the probes: the project's own when own is set (they
// earn the credits the checks spend), else any probe in Iran.
func gpLocations(own bool) []any {
	if tags := gpOwnTags(); own && tags != nil {
		return []any{map[string]any{"tags": tags}}
	}
	return []any{map[string]any{"country": "IR"}}
}

// gpProbes is how many probes test each address: one own probe is
// enough; among volunteers' probes two make up for a flaky one.
func gpProbes() int {
	if gpOwnTags() != nil {
		return 1
	}
	return 2
}

var (
	errNoProbes = fmt.Errorf("no probes in Iran right now")
	errQuota    = fmt.Errorf("hourly quota used up")
)

func tcpFromIran(ctx context.Context, endpoint string, probes int, own bool) (bool, error) {
	host, portS, err := net.SplitHostPort(endpoint)
	if err != nil {
		return false, err
	}
	port, _ := strconv.Atoi(portS)
	body, _ := json.Marshal(map[string]any{
		"type":               "ping",
		"target":             host,
		"locations":          gpLocations(own),
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
	for i := 0; i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
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
