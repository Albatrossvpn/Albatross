package engine

import (
	"crypto/rand"
	"encoding/hex"
	"maps"
	"net"
	"strings"
	"time"

	"github.com/Albatrossvpn/vp/internal/geo"
)

// Snapshot is everything the main screen shows.
type Snapshot struct {
	Status     string        `json:"status"`
	Message    string        `json:"message"`
	Target     string        `json:"target"`
	TargetName string        `json:"target_name"`
	TargetFlag string        `json:"target_flag"`
	Conn       *ConnInfo     `json:"conn"`
	Countries  []CountryInfo `json:"countries"`
	// ProtocolServers counts the servers of each protocol (ProtocolChoice
	// values): those not known dead, and those that worked.
	ProtocolServers map[string][2]int `json:"protocol_servers,omitempty"`
	Scan            ScanInfo          `json:"scan"`
	Settings        Settings          `json:"settings"`
	Admin           bool              `json:"admin"`
	Version         string            `json:"version"`
	Cores           CoreInfo          `json:"cores"`
	WorkingNow      int               `json:"working_now"`
	Mobile          bool              `json:"mobile"` // the Android app: one VPN mode, no admin
	Update          *UpdateInfo       `json:"update,omitempty"`
	TotalGood       int               `json:"total_good"`
	Account         AccountState      `json:"account"`
	Notice          string            `json:"notice,omitempty"`
	Network         string            `json:"network,omitempty"` // the filtering regime found here
	// HelpAvailable says "Help others" can run on this network; Helping
	// that it does.
	HelpAvailable bool `json:"help_available"`
	Helping       bool `json:"helping"`
	// CrowdAvailable says this build has a service to share stats with.
	CrowdAvailable bool `json:"crowd_available"`
	// Share lists where other devices (a hotspot's, the same Wi-Fi's) can
	// reach this device's HTTP and SOCKS proxy, when sharing is on.
	Share []string `json:"share,omitempty"`
	// SharePAC are the automatic proxy setup addresses for the same.
	SharePAC []string `json:"share_pac,omitempty"`
}

// ScanInfo is scan progress.
type ScanInfo struct {
	Phase        string `json:"phase"`
	Candidates   int    `json:"candidates"`
	Untested     int    `json:"untested"`
	Dead         int    `json:"dead"`
	Testing      int    `json:"testing"`
	PerMinute    int    `json:"per_minute"`
	SourcesTotal int    `json:"sources_total"`
	SourcesDone  int    `json:"sources_done"`
	SourcesOK    int    `json:"sources_ok"`
	SourceCount  int    `json:"source_count"`
	LastRefresh  int64  `json:"last_refresh"`
	Downloaded   int64  `json:"downloaded"`
	Found        int    `json:"found"`
}

// CoreInfo says which cores are installed.
type CoreInfo struct {
	SingBox  bool `json:"sing_box"`
	Xray     bool `json:"xray"`
	WarpPlus bool `json:"warp_plus"`
}

// Snapshot returns the current state for the UI.
func (e *Engine) Snapshot() Snapshot {
	countries := e.Countries()
	now := time.Now()
	e.mu.Lock()
	e.uiSeen = now
	total, untested, dead := e.poolStats[0], e.poolStats[1], e.poolStats[2]
	working := 0
	for _, s := range e.good {
		if s.usable(now) {
			working++
		}
	}
	enabled := 0
	for _, s := range e.srcs {
		if s.Enabled {
			enabled++
		}
	}
	snap := Snapshot{
		Status:          e.status,
		Message:         e.message,
		Notice:          e.notice,
		Network:         string(e.reg.Regime),
		HelpAvailable:   e.helpEligible(),
		Helping:         e.helper != nil && !e.helper.Exited(),
		CrowdAvailable:  len(e.crowdURLs) > 0,
		Share:           e.shareAddrs(),
		SharePAC:        e.sharePAC(),
		Target:          e.target,
		TargetName:      countryName(e.target),
		TargetFlag:      geo.Flag(e.target),
		Countries:       countries,
		Settings:        e.settings,
		Version:         e.opt.Version,
		Mobile:          e.opt.Mobile,
		Update:          e.updateCopy(),
		Cores:           CoreInfo{SingBox: e.bin.SingBox != "", Xray: e.bin.Xray != "", WarpPlus: e.bin.WarpPlus != ""},
		WorkingNow:      working,
		TotalGood:       len(e.good),
		ProtocolServers: maps.Clone(e.protoStats),
		Scan: ScanInfo{
			Phase: e.scan.Phase, Candidates: total, Untested: untested, Dead: dead, Testing: e.scan.Testing,
			PerMinute: len(e.scan.recent), SourcesTotal: e.scan.SourcesTotal, SourcesDone: e.scan.SourcesDone,
			SourcesOK: e.scan.SourcesOK, SourceCount: enabled, LastRefresh: e.scan.LastRefresh.Unix(),
			Found: e.scan.Found,
		},
	}
	if e.target == "" {
		snap.TargetFlag = "⚡"
	}
	e.mu.Unlock()
	snap.Scan.Downloaded = e.fetcher.BytesDownloaded()
	snap.Admin = e.opt.IsAdmin()
	snap.Conn = e.connInfo()
	snap.Account = e.Account()
	return snap
}

// SetTarget changes the chosen country without connecting.
func (e *Engine) SetTarget(country string) {
	e.mu.Lock()
	e.target = country
	e.settings.Country = country
	e.mu.Unlock()
	e.poke()
}

// Refresh downloads all sources again now.
func (e *Engine) Refresh() {
	e.mu.Lock()
	for i := range e.srcStats {
		e.srcStats[i].Fetched = time.Time{}
	}
	e.mu.Unlock()
	methodsMu.Lock()
	methodsTested = map[string]time.Time{}
	methodsMu.Unlock()
	e.triggerRefill()
}

func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

type noProxy struct{}

func (noProxy) Set(string, []string) error { return nil }
func (noProxy) Clear() error               { return nil }
func (noProxy) Owned(string) bool          { return false }

// shareAddrs are this device's local-network addresses with the proxy
// port, while the proxy is open to other devices.
func (e *Engine) shareAddrs() []string {
	if !e.settings.AllowLAN || e.conn == nil {
		return nil
	}
	_, port, err := net.SplitHostPort(e.conn.proxyAddr)
	if err != nil {
		return nil
	}
	var out []string
	for _, ip := range e.lanIPs() {
		out = append(out, net.JoinHostPort(ip, port))
	}
	return out
}

// sharePAC are the automatic proxy setup addresses (servePAC).
func (e *Engine) sharePAC() []string {
	var out []string
	for _, a := range e.shareAddrs() {
		host, port, _ := net.SplitHostPort(a)
		out = append(out, "http://"+net.JoinHostPort(host, pacPort(port))+"/proxy.pac")
	}
	return out
}

// lanIPs are this device's private IPv4 addresses, without the tunnel's.
func (e *Engine) lanIPs() []string {
	if e.opt.LANAddrs != nil {
		return e.opt.LANAddrs()
	}
	ifaces, _ := net.Interfaces()
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || virtualIface(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ip, ok := a.(*net.IPNet)
			if !ok || ip.IP.To4() == nil || !ip.IP.IsPrivate() || ip.IP.To4()[1] == 19 && ip.IP.To4()[0] == 172 {
				continue
			}
			out = append(out, ip.IP.String())
		}
	}
	return out
}

// virtualIface reports adapters that only reach virtual machines and
// containers on this computer, never another device.
func virtualIface(name string) bool {
	n := strings.ToLower(name)
	for _, v := range []string{"docker", "br-", "veth", "vethernet", "virtualbox", "vmware", "vboxnet", "hyper-v", "wsl", "albatross"} {
		if strings.Contains(n, v) {
			return true
		}
	}
	return false
}
