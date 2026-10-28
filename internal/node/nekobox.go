package node

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

// NekoBox's and Exclave's own share links, sn://<type>?<data> and
// exclave://<type>?<data>, carry a server's settings in those apps' binary
// form: base64url of zlib of Kryo 5's output, the fields in the order each
// kind of server writes them (its version first), the name after them.
// Those apps give the protocols that have links of their own those links;
// these are for the rest: WireGuard, SSH, and a whole sing-box config or
// outbound.

var errKryo = errors.New("short sn data")

// kryo reads Kryo 5's ints (4 bytes, little-endian) and strings (ASCII
// with the last byte's high bit set, or a flagged length, then modified
// UTF-8: a UTF-16 unit in one to three bytes).
type kryo struct {
	b   []byte
	err error
}

func (k *kryo) int() int {
	if len(k.b) < 4 {
		k.err = errKryo
		return 0
	}
	v := int(int32(binary.LittleEndian.Uint32(k.b)))
	k.b = k.b[4:]
	return v
}

func (k *kryo) str() string {
	if k.err != nil || len(k.b) == 0 {
		k.err = errKryo
		return ""
	}
	if c := k.b[0]; c&0x80 == 0 {
		for i, x := range k.b {
			if x&0x80 != 0 {
				s := string(k.b[:i]) + string(rune(x&0x7f))
				k.b = k.b[i+1:]
				return s
			}
		}
		k.err = errKryo
		return ""
	}
	c := k.b[0]
	n, i := int(c&0x3f), 1
	if c&0x40 != 0 {
		for shift := 6; ; shift += 7 {
			if i >= len(k.b) || shift > 34 {
				k.err = errKryo
				return ""
			}
			x := k.b[i]
			i++
			n |= int(x&0x7f) << shift
			if x&0x80 == 0 {
				break
			}
		}
	}
	k.b = k.b[i:]
	if n <= 1 {
		return "" // 0 is null, 1 the empty string
	}
	units := make([]uint16, 0, n-1)
	for len(units) < n-1 {
		if len(k.b) == 0 {
			k.err = errKryo
			return ""
		}
		x := k.b[0]
		switch {
		case x < 0x80:
			units = append(units, uint16(x))
			k.b = k.b[1:]
		case x>>5 == 6 && len(k.b) >= 2:
			units = append(units, uint16(x&0x1f)<<6|uint16(k.b[1]&0x3f))
			k.b = k.b[2:]
		case x>>4 == 14 && len(k.b) >= 3:
			units = append(units, uint16(x&0x0f)<<12|uint16(k.b[1]&0x3f)<<6|uint16(k.b[2]&0x3f))
			k.b = k.b[3:]
		default:
			k.err = errKryo
			return ""
		}
	}
	return string(utf16.Decode(units))
}

// parseNekoBox reads an sn:// or exclave:// link.
func parseNekoBox(link, scheme string) (*Node, error) {
	kind, data, ok := strings.Cut(link[len(scheme)+3:], "?")
	if !ok {
		return nil, fmt.Errorf("%s link without data", scheme)
	}
	data, _, _ = strings.Cut(data, "#")
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(strings.TrimSpace(data), "="))
	if err != nil {
		return nil, fmt.Errorf("%s base64: %w", scheme, err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("%s zlib: %w", scheme, err)
	}
	b, err := io.ReadAll(io.LimitReader(zr, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s zlib: %w", scheme, err)
	}
	k := &kryo{b: b}
	version := k.int()
	server, port := k.str(), k.int()
	var n *Node
	switch kind {
	case "wg":
		n = &Node{Proto: "wireguard", Server: server, Port: port}
		local := k.str()
		n.PrivateKey, n.PeerPublicKey, n.PreSharedKey = k.str(), k.str(), k.str()
		// NekoBox writes its version 2 whole; Exclave adds fields by version.
		if scheme == "sn" || version >= 1 {
			n.MTU = k.int()
		}
		if scheme == "sn" || version >= 2 {
			n.Reserved = parseReserved(k.str())
		}
		if scheme == "exclave" && version >= 4 {
			k.int() // the keepalive interval
		}
		for _, a := range strings.FieldsFunc(local, func(r rune) bool { return r == '\n' || r == ',' || r == ' ' }) {
			n.LocalAddress = append(n.LocalAddress, a)
		}
	case "ssh":
		n = &Node{Proto: "ssh", Server: server, Port: port, Username: k.str()}
		switch k.int() {
		case 1:
			n.Password = k.str()
		case 2:
			k.str() // a private key and its passphrase: the app logs in with a password only
			k.str()
		}
		n.HostKey = k.str()
	case "config":
		typ, config := k.int(), k.str()
		if k.err != nil {
			return nil, k.err
		}
		var nodes []*Node
		if typ == 1 {
			// One sing-box outbound.
			var o map[string]any
			if json.Unmarshal([]byte(config), &o) == nil {
				if nd := singBoxOutbound(cmap(o)); nd != nil {
					nodes = append(nodes, nd)
				}
			}
		} else {
			nodes = ParseConfigs(config)
		}
		if len(nodes) == 0 {
			return nil, fmt.Errorf("%s config without a server", scheme)
		}
		n = nodes[0]
	default:
		return nil, ErrUnsupported
	}
	if k.err != nil {
		return nil, k.err
	}
	// The name, after the server's own fields and a version of its own.
	if k.int(); k.err == nil {
		if name := k.str(); k.err == nil {
			n.Name = cleanName(name)
		}
	}
	return n, nil
}
