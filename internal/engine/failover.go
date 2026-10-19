package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cezomagoanlace29861-jpg/vp/internal/tester"
)

// How a live connection is watched. The server in use is checked often,
// and at once when the core reports failed connections or traffic stalls;
// the whole group is checked less often so backups are known to work before
// they are needed. Both slow down while nothing is being sent.
const (
	checkBusy = 10 * time.Second
	checkIdle = 30 * time.Second
	sweepBusy = 90 * time.Second
	sweepIdle = 5 * time.Minute
	// idleAfter is how long without traffic counts as idle.
	idleAfter = 30 * time.Second
	// refreshAfter is the youngest a group can be refreshed at.
	refreshAfter = 5 * time.Minute
)

// healthURL is what the core fetches through a member to check it.
const healthURL = "https://www.gstatic.com/generate_204"

// clashClient talks to the cores' local Clash API. It never uses a proxy.
var clashClient = &http.Client{Transport: &http.Transport{
	Proxy:               nil,
	MaxIdleConnsPerHost: 8,
	IdleConnTimeout:     30 * time.Second,
}}

// api calls the core's Clash API. reached is false when the core itself did
// not answer (it is starting, stopping or gone), so a failure says nothing
// about the servers.
func (c *connection) api(ctx context.Context, method, path string, body, out any, timeout time.Duration) (status int, err error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+c.clashAddr+path, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.clashSecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := clashClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("clash api %s: HTTP %d", path, resp.StatusCode)
	}
	if out == nil {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, nil
	}
	return resp.StatusCode, json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func delayQuery(timeout time.Duration) string {
	return url.Values{"url": {healthURL}, "timeout": {strconv.Itoa(int(timeout / time.Millisecond))}}.Encode()
}

// test fetches healthURL through one member. reached is false when the
// core could not be asked, in which case ok means nothing.
func (c *connection) test(ctx context.Context, m *member, timeout time.Duration) (delay int, ok, reached bool) {
	var r struct {
		Delay int `json:"delay"`
	}
	status, err := c.api(ctx, http.MethodGet, "/proxies/"+url.PathEscape(m.tag)+"/delay?"+delayQuery(timeout), nil, &r, timeout+2*time.Second)
	if status == 0 {
		return 0, false, false
	}
	return r.Delay, err == nil && r.Delay > 0, true
}

// use makes m the member all new connections go through.
func (c *connection) use(ctx context.Context, m *member) error {
	if _, err := c.api(ctx, http.MethodPut, "/proxies/proxy", map[string]string{"name": m.tag}, nil, 3*time.Second); err != nil {
		return err
	}
	c.mu.Lock()
	c.active = m
	if m.delay > 0 {
		c.delay = m.delay
	}
	c.mu.Unlock()
	return nil
}

// closeOn closes the connections still going through tag, so apps waiting
// on a dead server reconnect through the new one right away instead of
// timing out.
func (c *connection) closeOn(ctx context.Context, tag string) {
	var list struct {
		Connections []struct {
			ID     string   `json:"id"`
			Chains []string `json:"chains"`
		} `json:"connections"`
	}
	if _, err := c.api(ctx, http.MethodGet, "/connections", nil, &list, 3*time.Second); err != nil {
		return
	}
	for _, conn := range list.Connections {
		for _, t := range conn.Chains {
			if t == tag {
				c.api(ctx, http.MethodDelete, "/connections/"+url.PathEscape(conn.ID), nil, nil, 2*time.Second)
				break
			}
		}
	}
}

// poke asks the monitor to check the active server now.
func (c *connection) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// watchLog reacts to the core reporting failed connections: the server in
// use is checked right away instead of at the next tick.
func (c *connection) watchLog(core, line string) {
	if core == "sing-box" && strings.HasPrefix(line, "ERROR") && strings.Contains(line, "connection") &&
		!strings.Contains(line, "outbound/direct") {
		c.poke()
	}
}

// idle reports whether no traffic has passed for a while.
func (c *connection) idle() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.lastBusy) > idleAfter
}

func (c *connection) every(busy, quiet time.Duration) time.Duration {
	if c.idle() {
		return quiet
	}
	return busy
}

// backups lists the members known to work, other than skip, best first:
// the group's own country, then lowest delay.
func (c *connection) backups(skip map[*member]bool) []*member {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*member
	for _, m := range c.members {
		if m.alive && !skip[m] {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		hi, hj := out[i].country == c.home, out[j].country == c.home
		if hi != hj {
			return hi
		}
		return out[i].delay < out[j].delay
	})
	return out
}

// noteResult records a check of a member, on the member and on the server
// it runs, so the scanner does not need to test servers in use again.
func (e *Engine) noteResult(c *connection, m *member, delay int, ok bool) {
	now := time.Now()
	c.mu.Lock()
	m.checked = now
	if ok {
		m.alive, m.delay, m.fails = true, delay, 0
	} else {
		m.alive = false
		m.fails++
	}
	c.mu.Unlock()
	e.mu.Lock()
	s := m.server
	s.TestedAt = now
	if ok {
		s.LastOK, s.Delay, s.Fails = now, delay, 0
	} else {
		s.Fails++
	}
	e.mu.Unlock()
}

// race tests members at once and returns the first to answer, or nil when
// none does within timeout. The others keep being tested in the background
// and their results are recorded too.
func (e *Engine) race(ctx context.Context, c *connection, list []*member, timeout time.Duration) *member {
	if len(list) == 0 {
		return nil
	}
	first := make(chan *member, len(list))
	var wg sync.WaitGroup
	for _, m := range list {
		wg.Add(1)
		go func(m *member) {
			defer wg.Done()
			d, ok, reached := c.test(ctx, m, timeout)
			if !reached {
				return
			}
			e.noteResult(c, m, d, ok)
			if ok {
				first <- m
			}
		}(m)
	}
	go func() {
		wg.Wait()
		close(first)
	}()
	select {
	case m := <-first:
		return m
	case <-ctx.Done():
		return nil
	}
}

// monitor watches a connection: traffic, the health of the server in use
// and of its backups, and whether the group should be refreshed.
func (e *Engine) monitor(ctx context.Context, c *connection) {
	go c.streamTraffic(ctx)
	if c.bypassIran {
		go e.learnIran(ctx, c)
	}
	check := time.NewTimer(checkBusy)
	defer check.Stop()
	// The race at connect time already checked most of the group; look at
	// the whole of it again soon.
	sweep := time.NewTimer(20 * time.Second)
	defer sweep.Stop()
	var last time.Time
	for {
		urgent := false
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			if time.Since(last) < 2*time.Second {
				continue
			}
			urgent = true
		case <-check.C:
		case <-sweep.C:
			if !e.sweep(ctx, c) {
				return
			}
			sweep.Reset(c.every(sweepBusy, sweepIdle))
			continue
		}
		last = time.Now()
		if !e.checkActive(ctx, c, urgent) {
			return
		}
		check.Reset(c.every(checkBusy, checkIdle))
	}
}

// checkActive tests the server in use and fails over when it stopped
// answering. urgent means there is already evidence of trouble (failed
// connections, stalled traffic), so one failed check is enough. It reports
// false once the connection has been replaced.
func (e *Engine) checkActive(ctx context.Context, c *connection, urgent bool) bool {
	if !c.running.Alive() {
		e.logf("warn", "هسته متوقف شد؛ اتصال دوباره…")
		e.rebuild(c, nil)
		return false
	}
	m := c.activeMember()
	timeout := 4 * time.Second
	if urgent {
		// A server that usually answers in 300 ms and has not in 1.5 s is
		// not coming back; slow ones get up to 3 s.
		c.mu.Lock()
		usual := time.Duration(m.delay) * time.Millisecond
		c.mu.Unlock()
		timeout = min(max(3*usual+500*time.Millisecond, 1500*time.Millisecond), 3*time.Second)
	}
	d, ok, reached := c.test(ctx, m, timeout)
	if reached && !ok && !urgent && ctx.Err() == nil {
		// A second try rules out one lost request.
		d, ok, reached = c.test(ctx, m, 3*time.Second)
	}
	if ctx.Err() != nil {
		return false
	}
	if !reached {
		// The core did not answer its own API; the next tick sees whether
		// it died.
		return true
	}
	e.noteResult(c, m, d, ok)
	if ok {
		c.mu.Lock()
		c.healthy, c.delay = true, d
		c.mu.Unlock()
		return true
	}
	return e.failover(ctx, c, m)
}

// failover moves traffic off a member that stopped answering: at once to a
// backup known to work, else to whichever member answers first now, and
// only when the whole group is down to a fresh group in the same country.
// It reports false once the connection has been replaced.
func (e *Engine) failover(ctx context.Context, c *connection, dead *member) bool {
	tried := map[*member]bool{dead: true}
	for _, b := range c.backups(tried) {
		tried[b] = true
		if c.use(ctx, b) != nil {
			continue
		}
		c.closeOn(ctx, dead.tag)
		// Switch first, then confirm: the check that found b alive may be
		// a few minutes old.
		d, ok, reached := c.test(ctx, b, 4*time.Second)
		if reached {
			e.noteResult(c, b, d, ok)
		}
		if ok || !reached {
			e.switched(ctx, c, dead, b)
			return true
		}
	}
	if ctx.Err() != nil {
		return false
	}
	var rest []*member
	for _, m := range c.members {
		if !tried[m] {
			rest = append(rest, m)
		}
	}
	if b := e.race(ctx, c, rest, 5*time.Second); b != nil && c.use(ctx, b) == nil {
		c.closeOn(ctx, dead.tag)
		e.switched(ctx, c, dead, b)
		return true
	}
	if e.offline(ctx, c) && ctx.Err() == nil {
		// Every server looks dead when this device has no internet; keep
		// the group and carry on once it is back.
		e.setStatus(StatusReconnecting, "اینترنت دستگاه قطع است؛ با برگشتن آن، اتصال خودکار ادامه پیدا می‌کند.")
		for e.offline(ctx, c) {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(3 * time.Second):
			}
		}
		if ctx.Err() != nil {
			return false
		}
		e.setStatus(StatusConnected, connectedMessage(c))
		c.poke()
		return true
	}
	e.logf("warn", "هیچ‌کدام از سرورهای آماده جواب ندادند؛ گروه تازه‌ای در %s آماده می‌شود…", countryName(c.home))
	e.rebuild(c, c.members)
	return false
}

// switched finishes a failover to b.
func (e *Engine) switched(ctx context.Context, c *connection, from, to *member) {
	c.mu.Lock()
	c.healthy = true
	c.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	e.setStatus(StatusConnected, connectedMessage(c))
	e.logf("info", "سرور «%s» قطع شد؛ بلافاصله به سرور آماده «%s» در همان کشور منتقل شدید.", from.server.Name, to.server.Name)
	go e.refreshExit(ctx, c)
}

// refreshExit learns the new exit address after a switch.
func (e *Engine) refreshExit(ctx context.Context, c *connection) {
	r := tester.Probe(ctx, &url.URL{Scheme: "http", Host: c.proxyAddr}, 10*time.Second)
	if !r.OK {
		return
	}
	c.mu.Lock()
	c.exitIP = r.ExitIP
	if r.Country != "" {
		c.exitCC = r.Country
	}
	c.mu.Unlock()
}

// sweep checks every member at once, so backups are known to work before
// they are needed, moves to a clearly faster member, and refreshes a group
// that is running out of working backups while nobody is using it. It
// reports false once the connection has been replaced.
func (e *Engine) sweep(ctx context.Context, c *connection) bool {
	var live map[string]int
	timeout := 5 * time.Second
	if _, err := c.api(ctx, http.MethodGet, "/group/proxy/delay?"+delayQuery(timeout), nil, &live, timeout+3*time.Second); err != nil {
		return ctx.Err() == nil
	}
	if len(live) == 0 {
		// Nothing answered: more likely this device's network than eight
		// servers at once. A real check of the active one decides.
		c.poke()
		return true
	}
	for _, m := range c.members {
		d, ok := live[m.tag]
		e.noteResult(c, m, d, ok)
	}
	active := c.activeMember()
	if _, ok := live[active.tag]; !ok {
		c.poke()
		return true
	}
	e.mu.Lock()
	auto := e.settings.AutoSwitch
	e.mu.Unlock()
	if !auto || c.pinned {
		return true
	}
	if b := c.faster(); b != nil && c.use(ctx, b) == nil {
		c.mu.Lock()
		d := b.delay
		c.mu.Unlock()
		e.logf("info", "سرور سریع‌تری در همین گروه پیدا شد: «%s» (%d میلی‌ثانیه).", b.server.Name, d)
	}
	// A group younger than refreshAfter is left alone, so a country whose
	// servers keep failing does not restart the core over and over.
	if c.idle() && time.Since(c.started) > refreshAfter && e.groupStale(c) {
		e.logf("info", "سرورهای پشتیبان در %s تازه می‌شوند…", countryName(c.home))
		e.rebuild(c, nil)
		return false
	}
	return true
}

// faster returns a live member clearly faster than the one in use.
func (c *connection) faster() *member {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.active.delay
	if cur <= 0 {
		return nil
	}
	var best *member
	for _, m := range c.members {
		if m == c.active || !m.alive || m.country != c.active.country {
			continue
		}
		if float64(m.delay) < float64(cur)*0.6 && cur-m.delay > 80 && (best == nil || m.delay < best.delay) {
			best = m
		}
	}
	return best
}

// groupStale reports whether rebuilding the group would help: too few
// members still work and the same country has others to offer, or clearly
// faster servers have appeared there.
func (e *Engine) groupStale(c *connection) bool {
	c.mu.Lock()
	live := 0
	inGroup := map[string]bool{}
	for _, m := range c.members {
		inGroup[m.server.Key] = true
		if m.alive {
			live++
		}
	}
	cur := c.delay
	c.mu.Unlock()
	fresh := e.bestServers(c.home, groupSize, inGroup)
	if live < minReady && len(fresh) >= minReady-live {
		return true
	}
	for _, s := range fresh[:min(3, len(fresh))] {
		if cur > 0 && float64(s.Delay) < float64(cur)*0.6 && cur-s.Delay > 80 {
			return true
		}
	}
	return false
}

// rebuild replaces the connection with a fresh group in the same country,
// avoiding the members that just failed.
func (e *Engine) rebuild(c *connection, failed []*member) {
	e.setStatus(StatusReconnecting, fmt.Sprintf("در حال آماده کردن سرورهای تازه در %s…", countryName(c.home)))
	var avoid []string
	for _, m := range failed {
		avoid = append(avoid, m.server.Key)
	}
	e.mu.Lock()
	gen := e.connGen
	e.mu.Unlock()
	e.stopConn(c, false)
	go func() {
		if err := e.connectLoop(gen, c.country, c.home, nil, avoid); err != nil {
			e.mu.Lock()
			still := e.conn == nil
			e.mu.Unlock()
			if still {
				e.opt.SysProxy.Clear()
			}
		}
	}()
}

// offlineProbes are reachable from almost anywhere, inside Iran and out.
var offlineProbes = []string{"1.1.1.1:443", "8.8.8.8:443", "www.aparat.com:443", "www.digikala.com:443"}

// offline reports whether this device cannot open any TCP connection: not
// to the group's servers and not to well-known sites.
func (e *Engine) offline(ctx context.Context, c *connection) bool {
	addrs := append([]string(nil), offlineProbes...)
	for _, m := range c.members {
		if m.endpoint != "" {
			addrs = append(addrs, m.endpoint)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	up := make(chan struct{}, len(addrs))
	for _, a := range addrs {
		go func(a string) {
			var d net.Dialer
			if conn, err := d.DialContext(ctx, "tcp", a); err == nil {
				conn.Close()
				up <- struct{}{}
			}
		}(a)
	}
	select {
	case <-up:
		return false
	case <-ctx.Done():
		return true
	}
}

// streamTraffic follows the core's per-second traffic counters. One small
// line a second costs far less than polling the connection list.
func (c *connection) streamTraffic(ctx context.Context) {
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+c.clashAddr+"/traffic", nil)
		req.Header.Set("Authorization", "Bearer "+c.clashSecret)
		if resp, err := clashClient.Do(req); err == nil {
			dec := json.NewDecoder(resp.Body)
			for {
				var t struct {
					Up   int64 `json:"up"`
					Down int64 `json:"down"`
				}
				if dec.Decode(&t) != nil {
					break
				}
				c.addTraffic(t.Up, t.Down)
			}
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func (c *connection) addTraffic(up, down int64) {
	c.mu.Lock()
	c.up += up
	c.down += down
	c.upRate, c.dnRate = up, down
	if up+down > 0 {
		c.lastBusy = time.Now()
	}
	// Apps that sent something and got nothing back for a couple of
	// seconds usually mean the server in use went silent.
	now := time.Now()
	stalled := false
	switch {
	case up > 0 && c.waiting.IsZero():
		// Download in the same second may belong to older connections; only
		// a later answer counts.
		c.waiting = now
	case down > 0:
		c.waiting = time.Time{}
	case !c.waiting.IsZero() && now.Sub(c.waiting) >= 2*time.Second:
		c.waiting = time.Time{}
		stalled = true
	}
	c.mu.Unlock()
	if stalled {
		c.poke()
	}
}
