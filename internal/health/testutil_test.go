package health

import (
	"net"
	"testing"
)

// pickFreePort opens, then immediately closes, a TCP listener on a kernel-
// assigned port and returns its address. Tests use this to avoid colliding
// with another test or a real service.
//
// There is a tiny window between the close here and the real Listen in
// Server.Run where the OS could give the same port to someone else, but in
// practice on test machines this is safe.
func pickFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickFreePort: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}
