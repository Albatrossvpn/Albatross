//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/xjasonlyu/tun2socks/v2/core"
	"github.com/xjasonlyu/tun2socks/v2/core/adapter"
	"github.com/xjasonlyu/tun2socks/v2/core/device"
	"github.com/xjasonlyu/tun2socks/v2/core/device/tun"
	"golang.org/x/sys/windows"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	tunName    = "OrbotTun"
	tunAddr4   = "198.18.0.1"
	tunMask4   = "255.255.0.0"
	tunDNS4    = "198.18.0.2" // any address inside the TUN subnet that is not ours
	tunAddr6   = "fdfe:dcba:9876::1"
	tunPrefix6 = 126
)

type VPN struct {
	dev   device.Device
	stack *stack.Stack
	log   *LogBuffer
}

// physicalInterface finds the adapter that currently carries the default
// route, by asking the OS which source address it would use.
func physicalInterface(network, probe string) (int, netip.Addr) {
	c, err := net.Dial(network, probe)
	if err != nil {
		return 0, netip.Addr{}
	}
	local := c.LocalAddr().(*net.UDPAddr)
	c.Close()
	ip, _ := netip.AddrFromSlice(local.IP)
	ip = ip.Unmap()
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if x, ok := netip.AddrFromSlice(n.IP); ok && x.Unmap() == ip {
					return ifc.Index, ip
				}
			}
		}
	}
	return 0, ip
}

func netsh(log *LogBuffer, args ...string) error {
	cmd := exec.Command("netsh", args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Add("netsh %v: %v %s", args, err, string(out))
	}
	return err
}

func startVPN(cfg VPNConfig, log *LogBuffer) (*VPN, error) {
	if !isElevated() {
		return nil, errNeedAdmin
	}
	if4, ip4 := physicalInterface("udp4", "1.1.1.1:53")
	if6, _ := physicalInterface("udp6", "[2606:4700:4700::1111]:53")
	if if4 == 0 {
		return nil, errors.New("could not find the active network adapter")
	}
	log.Add("VPN: physical adapter index %d (%s), ipv6 index %d", if4, ip4, if6)

	dev, err := tun.Open(tunName, 1500)
	if err != nil {
		return nil, fmt.Errorf("open wintun adapter (is wintun.dll next to the app?): %w", err)
	}

	h := &tunHandler{
		socks:   socksDialer(cfg.SocksAddr),
		dnsAddr: cfg.DNSAddr,
		matcher: newProcessMatcher(cfg.TorDir),
		if4:     if4,
		if6:     if6,
		log:     log,
	}
	st, err := core.CreateStack(&core.Config{LinkEndpoint: dev, TransportHandler: h})
	if err != nil {
		dev.Close()
		return nil, fmt.Errorf("create network stack: %w", err)
	}
	v := &VPN{dev: dev, stack: st, log: log}

	name := "name=" + tunName
	steps := [][]string{
		{"interface", "ipv4", "set", "address", name, "source=static", "address=" + tunAddr4, "mask=" + tunMask4, "gateway=none"},
		{"interface", "ipv4", "set", "dnsservers", name, "source=static", "address=" + tunDNS4, "register=none", "validate=no"},
		{"interface", "ipv4", "set", "interface", tunName, "metric=1"},
		{"interface", "ipv4", "add", "route", "prefix=0.0.0.0/1", "interface=" + tunName, "nexthop=0.0.0.0", "metric=1", "store=active"},
		{"interface", "ipv4", "add", "route", "prefix=128.0.0.0/1", "interface=" + tunName, "nexthop=0.0.0.0", "metric=1", "store=active"},
		{"interface", "ipv4", "add", "route", "prefix=" + torVirtualNet, "interface=" + tunName, "nexthop=0.0.0.0", "metric=1", "store=active"},
	}
	for _, s := range steps {
		if err := netsh(log, s...); err != nil {
			v.Close()
			return nil, fmt.Errorf("configure %s: %w", tunName, err)
		}
	}
	// IPv6: route it into the tunnel too so it cannot leak around Tor.
	// Failures here are not fatal (IPv6 may be disabled on the adapter).
	_ = netsh(log, "interface", "ipv6", "add", "address", "interface="+tunName, "address="+tunAddr6+"/"+strconv.Itoa(tunPrefix6), "store=active")
	_ = netsh(log, "interface", "ipv6", "set", "interface", tunName, "metric=1")
	_ = netsh(log, "interface", "ipv6", "add", "route", "prefix=::/1", "interface="+tunName, "metric=1", "store=active")
	_ = netsh(log, "interface", "ipv6", "add", "route", "prefix=8000::/1", "interface="+tunName, "metric=1", "store=active")
	_ = netsh(log, "interface", "ipv6", "set", "dnsservers", name, "source=static", "address=none", "register=none", "validate=no")

	// Drop cached DNS answers that were resolved outside Tor.
	flush := exec.Command("ipconfig", "/flushdns")
	hideWindow(flush)
	_ = flush.Run()

	log.Add("VPN: %s is up, all traffic now goes through Tor", tunName)
	return v, nil
}

func (v *VPN) Close() {
	if v == nil {
		return
	}
	// Closing the wintun adapter removes its addresses and routes.
	if v.stack != nil {
		v.stack.Close()
	}
	if v.dev != nil {
		v.dev.Close()
	}
	v.log.Add("VPN: %s removed", tunName)
}

type tunHandler struct {
	socks   func(ctx context.Context, network, addr string) (net.Conn, error)
	dnsAddr string
	matcher *ProcessMatcher
	if4     int
	if6     int
	log     *LogBuffer
}

func endpoint(id stack.TransportEndpointID) (src, dst netip.AddrPort) {
	s, _ := netip.AddrFromSlice(id.RemoteAddress.AsSlice())
	d, _ := netip.AddrFromSlice(id.LocalAddress.AsSlice())
	return netip.AddrPortFrom(s.Unmap(), id.RemotePort), netip.AddrPortFrom(d.Unmap(), id.LocalPort)
}

// isTorTraffic reports whether the packet came from tor.exe or one of its
// pluggable transports, which must reach the internet directly.
func (h *tunHandler) isTorTraffic(udp bool, src netip.AddrPort) bool {
	return h.matcher.IsTor(findOwnerPID(udp, src.Addr(), src.Port()))
}

// directDialer binds sockets to the physical adapter with IP_UNICAST_IF,
// so they bypass the TUN routes.
func (h *tunHandler) directDialer(v6 bool) *net.Dialer {
	return &net.Dialer{
		Timeout: 30 * time.Second,
		Control: func(network, address string, c syscall.RawConn) error {
			var serr error
			err := c.Control(func(fd uintptr) {
				s := windows.Handle(fd)
				if v6 {
					serr = windows.SetsockoptInt(s, windows.IPPROTO_IPV6, 31 /* IPV6_UNICAST_IF */, h.if6)
				} else {
					var be [4]byte
					binary.BigEndian.PutUint32(be[:], uint32(h.if4))
					serr = windows.SetsockoptInt(s, windows.IPPROTO_IP, 31 /* IP_UNICAST_IF */, int(binary.LittleEndian.Uint32(be[:])))
				}
			})
			if err != nil {
				return err
			}
			return serr
		},
	}
}

func (h *tunHandler) HandleTCP(conn adapter.TCPConn) {
	go func() {
		src, dst := endpoint(conn.ID())
		var up net.Conn
		var err error
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if h.isTorTraffic(false, src) {
			v6 := dst.Addr().Is6()
			if v6 && h.if6 == 0 {
				conn.Close()
				return
			}
			up, err = h.directDialer(v6).DialContext(ctx, "tcp", dst.String())
		} else {
			up, err = h.socks(ctx, "tcp", dst.String())
		}
		if err != nil {
			conn.Close()
			return
		}
		relay(conn, up)
	}()
}

func (h *tunHandler) HandleUDP(conn adapter.UDPConn) {
	go func() {
		defer conn.Close()
		src, dst := endpoint(conn.ID())
		switch {
		case h.isTorTraffic(true, src):
			// Snowflake uses WebRTC (UDP) to reach its proxies.
			v6 := dst.Addr().Is6()
			if v6 && h.if6 == 0 {
				return
			}
			up, err := h.directDialer(v6).Dial("udp", dst.String())
			if err != nil {
				return
			}
			relayUDP(conn, up, 2*time.Minute)
		case dst.Port() == 53:
			// DNS goes to Tor's DNSPort. Tor cannot carry other UDP.
			up, err := net.Dial("udp", h.dnsAddr)
			if err != nil {
				return
			}
			relayUDP(conn, up, 30*time.Second)
		default:
			// Dropping makes apps such as browsers fall back from QUIC to TCP.
		}
	}()
}

// relayUDP forwards datagrams for one flow until it is idle for timeout.
func relayUDP(a net.Conn, b net.Conn, timeout time.Duration) {
	defer b.Close()
	done := make(chan struct{}, 2)
	cp := func(dst, src net.Conn) {
		buf := make([]byte, 65535)
		for {
			_ = src.SetReadDeadline(time.Now().Add(timeout))
			n, err := src.Read(buf)
			if err != nil {
				break
			}
			if _, err := dst.Write(buf[:n]); err != nil {
				break
			}
		}
		done <- struct{}{}
	}
	go cp(b, a)
	go cp(a, b)
	<-done
	_ = a.SetReadDeadline(time.Now())
	_ = b.SetReadDeadline(time.Now())
	<-done
}
