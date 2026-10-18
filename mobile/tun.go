//go:build linux

package mobile

import (
	"errors"
	"strconv"
	"sync"

	"github.com/xjasonlyu/tun2socks/v2/engine"
	"golang.org/x/sys/unix"
)

// MTU of the VPN interface; the app's VpnService.Builder uses the same.
const MTU = 1500

var (
	tunMu  sync.Mutex
	tunFD  = -1 // the VpnService's descriptor; the app owns and closes it
	tunRun bool
)

// StartTun starts carrying the VPN's packets to the local proxy. fd is the
// VpnService's tunnel; it stays the app's to close after StopTun.
func StartTun(fd int32) error {
	addr := vpn.target()
	if addr == "" {
		return errors.New("not connected")
	}
	tunMu.Lock()
	defer tunMu.Unlock()
	if tunRun {
		stopLocked()
	}
	tunFD = int(fd)
	return startLocked(addr)
}

// StopTun stops tun2socks.
func StopTun() {
	tunMu.Lock()
	defer tunMu.Unlock()
	stopLocked()
	tunFD = -1
}

// retarget points a running tunnel at a new proxy address.
func retarget(addr string) {
	tunMu.Lock()
	defer tunMu.Unlock()
	if !tunRun {
		return
	}
	stopLocked()
	startLocked(addr)
}

func startLocked(addr string) error {
	if tunFD < 0 {
		return errors.New("no tunnel")
	}
	// tun2socks closes the descriptor it is given when it stops.
	dup, err := unix.Dup(tunFD)
	if err != nil {
		return err
	}
	engine.Insert(&engine.Key{
		Device:   "fd://" + strconv.Itoa(dup),
		Proxy:    "socks5://" + addr,
		MTU:      MTU,
		LogLevel: "warn",
	})
	// Every value above is valid, so engine.Start (which exits the process
	// on a bad key) cannot fail here.
	engine.Start()
	tunRun = true
	return nil
}

func stopLocked() {
	if tunRun {
		engine.Stop()
		tunRun = false
	}
}
