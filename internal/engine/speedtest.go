package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// SpeedTest is the in-app speed test of the server in use, measured through
// the tunnel: ping and jitter, then download, then upload. Speeds are in
// bits a second and move while their phase runs.
type SpeedTest struct {
	// Phase is "" before any test, then "ping", "download", "upload", and
	// "done", "stopped" or "error".
	Phase   string  `json:"phase"`
	Ping    int     `json:"ping"`   // ms
	Jitter  int     `json:"jitter"` // ms
	Down    float64 `json:"down"`
	Up      float64 `json:"up"`
	Country string  `json:"country,omitempty"` // where the server in use is
	Error   string  `json:"error,omitempty"`
}

type speedRun struct {
	mu     sync.Mutex
	state  SpeedTest
	cancel context.CancelFunc
	id     int // which run owns state: a run started over stops writing
}

// The speed test's addresses; tests point them elsewhere. speedPingURL is
// small and answers at once, speedUpURL takes the bytes sent to it.
var (
	speedPingURL = healthURL
	speedUpURL   = "https://speed.cloudflare.com/__up"
)

var (
	// speedPhase is how long download and upload each run.
	speedPhase = 8 * time.Second
	// speedUpChunk is the bytes of one upload request. Small enough that a
	// slow uplink (a few megabits, the usual there) completes several in a
	// phase, so the measurement rests on finished requests, not on one
	// huge body cut short; a fast line takes many.
	speedUpChunk int64 = 2 << 20
	// speedCap is the most bytes a phase moves: a fast line finishes the
	// measurement sooner instead of moving data for nothing.
	speedCap int64 = 64 << 20
	// speedGrace is how long past its phase a request may take to end.
	speedGrace = 5 * time.Second
)

const speedPings = 6

// ErrNotConnected is a speed test asked for while nothing is connected.
var ErrNotConnected = errors.New("Connect first, then test the speed.")

// errTimeUp ends an upload body when the phase is over.
var errTimeUp = errors.New("speed test: time is up")

// SpeedTestState is the speed test running or last run.
func (e *Engine) SpeedTestState() SpeedTest {
	e.speed.mu.Lock()
	defer e.speed.mu.Unlock()
	return e.speed.state
}

// StopSpeedTest stops a running speed test, keeping what it measured.
func (e *Engine) StopSpeedTest() {
	e.speed.mu.Lock()
	defer e.speed.mu.Unlock()
	if e.speed.cancel != nil {
		e.speed.cancel()
	}
}

// StartSpeedTest starts a speed test of the server in use; one already
// running is started over.
func (e *Engine) StartSpeedTest() error {
	e.mu.Lock()
	c := e.conn
	e.mu.Unlock()
	if c == nil {
		return ErrNotConnected
	}
	c.mu.Lock()
	m := c.active
	c.mu.Unlock()
	if m == nil {
		return ErrNotConnected
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.speed.mu.Lock()
	if e.speed.cancel != nil {
		e.speed.cancel()
	}
	e.speed.id++
	id := e.speed.id
	e.speed.cancel = cancel
	e.speed.state = SpeedTest{Phase: "ping", Country: m.server.Country}
	e.speed.mu.Unlock()
	go e.runSpeedTest(ctx, cancel, id, c, m)
	return nil
}

func (e *Engine) runSpeedTest(ctx context.Context, cancel context.CancelFunc, id int, c *connection, m *member) {
	defer cancel()
	set := func(f func(*SpeedTest)) {
		e.speed.mu.Lock()
		defer e.speed.mu.Unlock()
		if e.speed.id == id {
			f(&e.speed.state)
		}
	}
	fail := func(err error) {
		if ctx.Err() != nil {
			set(func(s *SpeedTest) { s.Phase = "stopped" })
			return
		}
		set(func(s *SpeedTest) {
			s.Phase, s.Error = "error", "The test could not finish. Try again in a moment."
		})
		e.logf("warn", "Speed test: %v", err)
	}
	proxy := c.memberProxy(m)
	keep := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}, Timeout: 10 * time.Second}
	defer keep.CloseIdleConnections()
	ping, jitter, err := speedPing(ctx, keep)
	if err != nil {
		fail(err)
		return
	}
	set(func(s *SpeedTest) { s.Ping, s.Jitter, s.Phase = ping, jitter, "download" })
	fresh := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true}}
	if err := speedDownload(ctx, fresh, func(bps float64) { set(func(s *SpeedTest) { s.Down = bps }) }); err != nil {
		fail(err)
		return
	}
	set(func(s *SpeedTest) { s.Phase = "upload" })
	if err := speedUpload(ctx, fresh, func(bps float64) { set(func(s *SpeedTest) { s.Up = bps }) }); err != nil {
		if ctx.Err() != nil {
			fail(err)
			return
		}
		// The download was measured; a server that will not carry the
		// upload (many cap or drop large uploads) does not throw that
		// away. The result stands, with a word on what could not be done.
		e.logf("warn", "Speed test: %v", err)
		set(func(s *SpeedTest) {
			s.Phase, s.Error = "done", "Download measured; upload could not be measured through this server."
		})
		return
	}
	// Stopped just as it finished: it was stopped.
	if ctx.Err() != nil {
		fail(ctx.Err())
		return
	}
	set(func(s *SpeedTest) { s.Phase = "done" })
}

// speedPing times small requests over one kept-alive connection: the first
// opens it, the rest are round trips. Ping is their median, jitter the
// mean change from one to the next.
func speedPing(ctx context.Context, client *http.Client) (ping, jitter int, err error) {
	var times []time.Duration
	var lastErr error
	for i := 0; i < speedPings; i++ {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, speedPingURL, nil)
		if err != nil {
			return 0, 0, err
		}
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			// A flaky line drops a request now and then; the ones that
			// answered still say what the ping is. Only a line that
			// answers almost nothing fails the test.
			lastErr = err
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if i > 0 {
			times = append(times, time.Since(start))
		}
	}
	if len(times) < 2 {
		if lastErr == nil {
			lastErr = errors.New("ping: too few answers")
		}
		return 0, 0, lastErr
	}
	var sum time.Duration
	for i := 1; i < len(times); i++ {
		d := times[i] - times[i-1]
		sum += max(d, -d)
	}
	sum /= time.Duration(len(times) - 1)
	sorted := append([]time.Duration(nil), times...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return int(sorted[len(sorted)/2].Milliseconds()), int(sum.Milliseconds()), nil
}

// rateMeter measures a transfer's speed over its last few seconds, leaving
// out the first: buffers filling and slow start make a start read high or
// low, and what a line carries is what it carries once it is going.
type rateMeter struct {
	mu      sync.Mutex
	start   time.Time
	end     time.Time // when the transfer stopped; zero while it goes on
	total   int64
	samples []rateSample // at most one in 100 ms
}

type rateSample struct {
	at time.Duration
	n  int64
}

func (m *rateMeter) bytes() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.total
}

func (m *rateMeter) add(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if m.start.IsZero() {
		m.start = now
	}
	m.total += int64(n)
	at := now.Sub(m.start)
	if k := len(m.samples); k == 0 || at-m.samples[k-1].at >= 100*time.Millisecond {
		m.samples = append(m.samples, rateSample{at, m.total})
	}
}

// stop says the transfer is over, so what follows is not counted as idle time.
func (m *rateMeter) stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.end.IsZero() {
		m.end = time.Now()
	}
}

// rate is the speed in bits a second.
func (m *rateMeter) rate() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.start.IsZero() {
		return 0
	}
	end := m.end
	if end.IsZero() {
		end = time.Now()
	}
	samples := append(append([]rateSample(nil), m.samples...), rateSample{end.Sub(m.start), m.total})
	return rateOf(samples)
}

func rateOf(samples []rateSample) float64 {
	if len(samples) == 0 {
		return 0
	}
	last := samples[len(samples)-1]
	skip, window := speedPhase/8, speedPhase*5/8
	// A short transfer (a fast line reaches the cap in a second or two)
	// leaves out a quarter of itself, and with no sample after that reads as
	// a whole. In a long one a start with no sample after it is a line that
	// stopped: no rate.
	long := last.at >= 2*skip
	if !long {
		skip = last.at / 4
	}
	from := max(skip, last.at-window)
	base := rateSample{}
	for i, s := range samples {
		if s.at >= from && (long || i < len(samples)-1) {
			base = s
			break
		}
	}
	if last.at <= base.at || last.at == 0 {
		return 0
	}
	return float64(last.n-base.n) * 8 / (last.at - base.at).Seconds()
}

// speedDownload downloads for speedPhase, telling the speed as it goes.
func speedDownload(ctx context.Context, client *http.Client, tell func(float64)) error {
	ctx, cancel := context.WithTimeout(ctx, speedPhase+speedGrace)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, speedURL+strconv.FormatInt(speedCap, 10), nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("download: HTTP " + strconv.Itoa(resp.StatusCode))
	}
	meter := &rateMeter{}
	buf := make([]byte, 64<<10)
	var start time.Time
	told := time.Now()
	for {
		k, err := resp.Body.Read(buf)
		if k > 0 {
			if start.IsZero() {
				start = time.Now()
			}
			meter.add(k)
		}
		if time.Since(told) > 200*time.Millisecond {
			tell(meter.rate())
			told = time.Now()
		}
		if !start.IsZero() && time.Since(start) >= speedPhase {
			break
		}
		if err != nil {
			// The end, the phase's end, or a line that broke after carrying
			// a measurable amount (a second of data, or a megabyte): what
			// moved is the measurement. Only a break before anything
			// meaningful came through fails the test, so a flaky line is
			// measured rather than given up on.
			if err == io.EOF || meter.bytes() > 0 && (ctx.Err() != nil || time.Since(start) >= time.Second || meter.bytes() >= 1<<20) {
				break
			}
			return err
		}
	}
	meter.stop()
	if meter.bytes() == 0 {
		return errors.New("download: no data")
	}
	tell(meter.rate())
	return nil
}

// zeroBody is an upload's body: zeros, counted, until it has sent its length
// or the phase is over.
type zeroBody struct {
	left  int64
	until time.Time
	meter *rateMeter
}

func (b *zeroBody) Read(p []byte) (int, error) {
	if time.Now().After(b.until) {
		return 0, errTimeUp
	}
	if b.left <= 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > b.left {
		n = b.left
	}
	clear(p[:n])
	b.left -= n
	b.meter.add(int(n))
	return int(n), nil
}

// speedUpload sends for speedPhase, in requests of speedUpChunk bytes, telling
// the speed as it goes. A request the phase cuts short is no failure.
func speedUpload(ctx context.Context, client *http.Client, tell func(float64)) error {
	stopped := ctx
	ctx, cancel := context.WithTimeout(ctx, speedPhase+speedGrace)
	defer cancel()
	meter := &rateMeter{}
	until := time.Now().Add(speedPhase)
	done := make(chan error, 1)
	go func() {
		for time.Now().Before(until) && meter.bytes() < speedCap {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, speedUpURL, &zeroBody{left: speedUpChunk, until: until, meter: meter})
			if err != nil {
				done <- err
				return
			}
			req.ContentLength = speedUpChunk
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := client.Do(req)
			if err != nil {
				switch {
				case stopped.Err() != nil:
					done <- stopped.Err()
				case time.Now().Before(until) && ctx.Err() == nil:
					done <- err
				default: // the phase is over, and cut the request short
					done <- nil
				}
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode/100 != 2 {
				done <- errors.New("upload: HTTP " + strconv.Itoa(resp.StatusCode))
				return
			}
		}
		done <- nil
	}()
	// finish ends the phase: what moved is the result, and nothing moved is
	// an error, as it is when the line hangs until the time is up.
	finish := func() error {
		meter.stop()
		rate := meter.rate()
		if rate == 0 {
			return errors.New("upload: no data")
		}
		tell(rate)
		return nil
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				return err
			}
			return finish()
		case <-tick.C:
			tell(meter.rate())
		case <-ctx.Done():
			if stopped.Err() != nil {
				return stopped.Err()
			}
			return finish()
		}
	}
}
