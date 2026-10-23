package engine

import (
	"github.com/cezomagoanlace29861-jpg/vp/internal/core"
)

// "Help others": a Snowflake proxy that lets people in censored countries
// reach Tor through this computer, the way the Tor Project's own Snowflake
// extension does. Running one inside Iran would draw attention to the user,
// so it only runs while the computer is outside Iran on an open network;
// it stops by itself as soon as that changes. Albatross itself uses Tor's
// Snowflake bridges, so the help comes back to its own users.

// helpEligible reports whether this network may run the proxy. Callers
// hold e.mu.
func (e *Engine) helpEligible() bool {
	p := e.reg.Probe
	// While the whole device goes through the VPN, the help would run over
	// the VPN's servers, which are not ours to lend.
	tunnelled := e.conn != nil && e.settings.Mode == "tun"
	return e.bin.Snowflake != "" && !e.opt.Mobile && !tunnelled && e.reg.Regime == regimeFree && p.Country != "" && p.Country != "IR"
}

// updateHelp starts or stops the proxy to match the setting and network.
func (e *Engine) updateHelp() {
	e.mu.Lock()
	want := e.settings.HelpOthers && e.helpEligible() && e.ctx.Err() == nil
	p := e.helper
	running := p != nil && !p.Exited()
	if want == running {
		e.mu.Unlock()
		return
	}
	e.helper = nil
	e.mu.Unlock()
	if running {
		p.Stop()
		e.logf("info", "Stopped helping others: this computer is no longer on an open network outside Iran, or the setting is off.")
		return
	}
	np, err := core.Start("snowflake", e.bin.Snowflake, []string{"-capacity", "4"}, nil, nil)
	if err != nil {
		e.logf("warn", "Could not start helping others: %v", err)
		return
	}
	e.mu.Lock()
	e.helper = np
	e.mu.Unlock()
	e.logf("info", "Helping others: people in censored countries can now reach Tor through this computer.")
}

// stopHelp stops the proxy (when the app closes).
func (e *Engine) stopHelp() {
	e.mu.Lock()
	p := e.helper
	e.helper = nil
	e.mu.Unlock()
	p.Stop()
}
