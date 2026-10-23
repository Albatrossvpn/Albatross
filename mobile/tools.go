//go:build tools

package mobile

// gomobile bind needs golang.org/x/mobile/bind in this module's
// requirements; this import keeps it there.
import _ "golang.org/x/mobile/bind"
