package main

import (
	"errors"
	"fmt"
	"sync"
)

const torVirtualNet = "10.192.0.0/10" // addresses Tor hands out for .onion names in VPN mode

var errNeedAdmin = errors.New("need_admin")

type VPNConfig struct {
	SocksAddr string
	DNSAddr   string
	TorDir    string
}

// Controller ties Tor, the local HTTP proxy, the system proxy and the VPN
// together and is what the UI talks to.
type Controller struct {
	settings *SettingsStore
	tor      *Tor
	torErr   error // set when the Tor bundle could not be found
	log      *LogBuffer

	mu        sync.Mutex
	httpProxy *HTTPProxy
	vpn       *VPN
	active    Settings // settings of the current session
	lastError string
}

type Status struct {
	Tor       TorStatus `json:"tor"`
	Mode      string    `json:"mode"`
	SocksAddr string    `json:"socksAddr"`
	HTTPAddr  string    `json:"httpAddr"`
	Elevated  bool      `json:"elevated"`
	Error     string    `json:"error,omitempty"`
	Missing   string    `json:"missing,omitempty"`
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	st := Status{Elevated: isElevated(), Error: c.lastError}
	if c.torErr != nil {
		st.Missing = c.torErr.Error()
		st.Tor = TorStatus{State: "off"}
		return st
	}
	st.Tor = c.tor.Status()
	if st.Tor.State != "off" {
		st.Mode = c.active.Mode
		st.SocksAddr = fmt.Sprintf("127.0.0.1:%d", c.active.SocksPort)
		if c.httpProxy != nil {
			st.HTTPAddr = fmt.Sprintf("127.0.0.1:%d", c.active.HTTPPort)
		}
	}
	if st.Tor.Error != "" && st.Error == "" {
		st.Error = st.Tor.Error
	}
	return st
}

func (c *Controller) setError(err error) {
	c.mu.Lock()
	if err == nil {
		c.lastError = ""
	} else {
		c.lastError = err.Error()
	}
	c.mu.Unlock()
}

func (c *Controller) Connect() error {
	if c.torErr != nil {
		return c.torErr
	}
	s := c.settings.Get()
	if s.Mode == ModeVPN && !isElevated() {
		return errNeedAdmin
	}
	c.setError(nil)

	c.mu.Lock()
	c.active = s
	c.mu.Unlock()

	if s.Mode != ModeVPN {
		hp, err := startHTTPProxy(fmt.Sprintf("127.0.0.1:%d", s.HTTPPort), fmt.Sprintf("127.0.0.1:%d", s.SocksPort), c.log)
		if err != nil {
			return fmt.Errorf("HTTP proxy port %d: %w", s.HTTPPort, err)
		}
		c.mu.Lock()
		c.httpProxy = hp
		c.mu.Unlock()
	}

	err := c.tor.Start(s, c.onTorReady, c.onTorExit)
	if err != nil {
		c.teardown()
		return err
	}
	return nil
}

// onTorReady applies the chosen mode once Tor has fully bootstrapped.
// Waiting until then means pluggable transports can still resolve their
// rendezvous servers while the network is untouched.
func (c *Controller) onTorReady() {
	c.mu.Lock()
	s := c.active
	c.mu.Unlock()
	switch s.Mode {
	case ModeProxy:
		prev, err := setSystemProxy(fmt.Sprintf("127.0.0.1:%d", s.HTTPPort))
		if err != nil {
			c.log.Add("system proxy: %v", err)
			c.setError(fmt.Errorf("could not set system proxy: %w", err))
			break
		}
		// Keep the first backup if one is already stored (e.g. after a crash).
		_ = c.settings.Update(func(st *Settings) {
			if st.SavedProxy == nil {
				st.SavedProxy = prev
			}
		})
		c.log.Add("system proxy set to 127.0.0.1:%d", s.HTTPPort)
	case ModeVPN:
		v, err := startVPN(VPNConfig{
			SocksAddr: fmt.Sprintf("127.0.0.1:%d", s.SocksPort),
			DNSAddr:   fmt.Sprintf("127.0.0.1:%d", s.DNSPort),
			TorDir:    c.tor.paths.TorDir,
		}, c.log)
		if err != nil {
			c.log.Add("VPN: %v", err)
			c.setError(err)
			go c.Disconnect()
			return
		}
		c.mu.Lock()
		c.vpn = v
		c.mu.Unlock()
	}
	c.tor.MarkOn()
}

func (c *Controller) onTorExit(error) { c.teardown() }

func (c *Controller) teardown() {
	c.mu.Lock()
	hp, v := c.httpProxy, c.vpn
	c.httpProxy, c.vpn = nil, nil
	c.mu.Unlock()
	if v != nil {
		v.Close()
	}
	if hp != nil {
		hp.Close()
	}
	c.restoreProxy()
}

func (c *Controller) restoreProxy() {
	saved := c.settings.Get().SavedProxy
	if saved == nil {
		return
	}
	if err := restoreSystemProxy(saved); err != nil {
		c.log.Add("restore system proxy: %v", err)
		return
	}
	_ = c.settings.Update(func(st *Settings) { st.SavedProxy = nil })
	c.log.Add("system proxy restored")
}

func (c *Controller) Disconnect() {
	if c.torErr == nil {
		c.tor.Stop()
	}
	c.teardown()
}

func (c *Controller) NewIdentity() error {
	if c.torErr != nil {
		return c.torErr
	}
	return c.tor.NewIdentity()
}
