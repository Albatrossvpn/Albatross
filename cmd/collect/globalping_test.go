package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGlobalping answers measurements with the result set for each target
// and records the limit and locations each one was asked with.
func fakeGlobalping(t *testing.T, results map[string]string) (asked *[]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var calls []map[string]any
	targets := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			calls = append(calls, body)
			if locs, _ := json.Marshal(body["locations"]); strings.Contains(string(locs), "tags") {
				w.WriteHeader(http.StatusUnprocessableEntity) // no own probe online
				return
			}
			id := fmt.Sprintf("m%d", len(calls))
			targets[id] = body["target"].(string)
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"id":%q}`, id)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v1/measurements/")
		fmt.Fprintf(w, `{"status":"finished","results":[%s]}`, results[targets[id]])
	}))
	t.Cleanup(srv.Close)
	old := globalpingAPI
	t.Cleanup(func() { globalpingAPI = old })
	globalpingAPI = srv.URL + "/v1/measurements"
	return &calls
}

// Only a probe that ran the test speaks for an address: one that failed on
// its own side leaves the address unchecked rather than closed.
func TestIranReachCountsOnlyProbesThatRan(t *testing.T) {
	t.Setenv("GLOBALPING_TAGS", "")
	fakeGlobalping(t, map[string]string{
		"192.0.2.1": `{"result":{"status":"finished","stats":{"rcv":2}}}`,
		"192.0.2.2": `{"result":{"status":"finished","stats":{"rcv":0}}}`,
		"192.0.2.3": `{"result":{"status":"failed","failureSource":"internal"}}`,
		"192.0.2.4": `{"result":{"status":"failed","failureSource":"target"}}`,
		"192.0.2.5": `{"result":{"status":"failed","failureSource":"internal"}},{"result":{"status":"finished","stats":{"rcv":1}}}`,
	})
	got := iranReach(context.Background(), []string{"192.0.2.1:443", "192.0.2.2:443", "192.0.2.3:443", "192.0.2.4:443", "192.0.2.5:443"}, gpProbes())
	want := map[string]bool{"192.0.2.1:443": true, "192.0.2.2:443": false, "192.0.2.4:443": false, "192.0.2.5:443": true}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for ep, open := range want {
		if o, ok := got[ep]; !ok || o != open {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// With no own probe online, every address is tested by two of the
// volunteers' probes, not only the one that found the own probes gone.
func TestIranReachFallsBackToTwoVolunteerProbes(t *testing.T) {
	t.Setenv("GLOBALPING_TAGS", "u-test")
	// More addresses than are asked about at once: the later ones start
	// after the fallback.
	results := map[string]string{}
	var eps []string
	for i := 1; i <= 20; i++ {
		ip := fmt.Sprintf("192.0.2.%d", i)
		results[ip] = `{"result":{"status":"finished","stats":{"rcv":2}}}`
		eps = append(eps, ip+":443")
	}
	calls := fakeGlobalping(t, results)
	if got := iranReach(context.Background(), eps, gpProbes()); len(got) != len(eps) {
		t.Fatalf("got %v", got)
	}
	for _, c := range *calls {
		if locs, _ := json.Marshal(c["locations"]); !strings.Contains(string(locs), "tags") && c["limit"] != float64(2) {
			t.Fatalf("a volunteers' test asked %v probes", c["limit"])
		}
	}
}
