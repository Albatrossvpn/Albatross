//go:build windows

package main

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Finding which process owns a connection lets the VPN send Tor's own
// traffic (tor.exe and its pluggable transports) straight out of the
// physical adapter instead of looping it back into Tor.

var (
	iphlpapi                = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetExtendedTcpTable = iphlpapi.NewProc("GetExtendedTcpTable")
	procGetExtendedUdpTable = iphlpapi.NewProc("GetExtendedUdpTable")
)

const (
	tcpTableOwnerPIDAll = 5
	udpTableOwnerPID    = 1
)

func fetchTable(proc *windows.LazyProc, family uint32, class uint32) []byte {
	size := uint32(16 * 1024)
	for i := 0; i < 5; i++ {
		buf := make([]byte, size)
		r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(family), uintptr(class), 0)
		if r == 0 {
			return buf[:size]
		}
		if syscall.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER {
			return nil
		}
		size += 4096
	}
	return nil
}

func portFromDword(b []byte) uint16 {
	// Port is stored in network byte order in the low 16 bits.
	return binary.BigEndian.Uint16(b[0:2])
}

// findOwnerPID returns the PID owning the local socket (addr, port).
func findOwnerPID(udp bool, addr netip.Addr, port uint16) uint32 {
	v6 := addr.Is6() && !addr.Is4In6()
	family := uint32(windows.AF_INET)
	if v6 {
		family = windows.AF_INET6
	}
	var tbl []byte
	var rowSize, portOff, pidOff, addrLen int
	switch {
	case !udp && !v6:
		tbl = fetchTable(procGetExtendedTcpTable, family, tcpTableOwnerPIDAll)
		rowSize, portOff, pidOff, addrLen = 24, 8, 20, 4 // state, laddr, lport, raddr, rport, pid
	case !udp && v6:
		tbl = fetchTable(procGetExtendedTcpTable, family, tcpTableOwnerPIDAll)
		rowSize, portOff, pidOff, addrLen = 56, 20, 52, 16 // laddr[16], scope, lport, raddr[16], scope, rport, state, pid
	case udp && !v6:
		tbl = fetchTable(procGetExtendedUdpTable, family, udpTableOwnerPID)
		rowSize, portOff, pidOff, addrLen = 12, 4, 8, 4 // laddr, lport, pid
	default:
		tbl = fetchTable(procGetExtendedUdpTable, family, udpTableOwnerPID)
		rowSize, portOff, pidOff, addrLen = 28, 20, 24, 16 // laddr[16], scope, lport, pid
	}
	if len(tbl) < 4 {
		return 0
	}
	n := int(binary.LittleEndian.Uint32(tbl[0:4]))
	addrOff := 0
	if !udp && !v6 {
		addrOff = 4
	}
	want := addr.Unmap()
	var wildcard uint32
	for i := 0; i < n; i++ {
		row := tbl[4+i*rowSize:]
		if len(row) < rowSize {
			break
		}
		if portFromDword(row[portOff:]) != port {
			continue
		}
		pid := binary.LittleEndian.Uint32(row[pidOff:])
		var la netip.Addr
		if addrLen == 4 {
			la = netip.AddrFrom4([4]byte(row[addrOff : addrOff+4]))
		} else {
			la = netip.AddrFrom16([16]byte(row[addrOff : addrOff+16]))
		}
		if la == want {
			return pid
		}
		if la.IsUnspecified() {
			wildcard = pid
		}
	}
	return wildcard
}

type pidCacheEntry struct {
	bypass bool
	at     time.Time
}

// ProcessMatcher decides whether a PID belongs to the bundled Tor files.
type ProcessMatcher struct {
	prefix string // lower-case directory of the Tor bundle
	mu     sync.Mutex
	cache  map[uint32]pidCacheEntry
}

func newProcessMatcher(dir string) *ProcessMatcher {
	return &ProcessMatcher{prefix: strings.ToLower(dir), cache: map[uint32]pidCacheEntry{}}
}

func (m *ProcessMatcher) IsTor(pid uint32) bool {
	if pid == 0 {
		return false
	}
	m.mu.Lock()
	e, ok := m.cache[pid]
	m.mu.Unlock()
	if ok && time.Since(e.at) < 30*time.Second {
		return e.bypass
	}
	bypass := strings.HasPrefix(strings.ToLower(processPath(pid)), m.prefix)
	m.mu.Lock()
	m.cache[pid] = pidCacheEntry{bypass: bypass, at: time.Now()}
	m.mu.Unlock()
	return bypass
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}
