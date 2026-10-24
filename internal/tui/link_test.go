package tui

import (
	"net/netip"
	"strings"
	"testing"
)

// A socket table as in /proc/net/tcp6, with sshd listening, a connection
// from an IPv4 client mapped into IPv6, and one from ::1.
const tcp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7695376 1 0000000000000000 100 0 0 10 0
   1: 0000000000000000FFFF00000200000A:0016 0000000000000000FFFF00000100000A:C350 01 00004E20:00000000 01:00000014 00000000     0        0 7695999 4 0000000000000000 20 4 30 10 -1
   2: 00000000000000000000000001000000:0016 00000000000000000000000001000000:D431 01 00000400:00000000 00:00000000 00000000     0        0 7696000 1 0000000000000000 20 4 30 10 -1
`

const tcp4 = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0016 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 7695375 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0016 0100007F:C351 01 0001ADB0:00000000 01:00000014 00000000     0        0 7695377 4 0000000000000000 20 4 30 10 -1
`

func TestTxQueue(t *testing.T) {
	ap := netip.MustParseAddrPort
	for _, tc := range []struct {
		table         string
		local, remote netip.AddrPort
		want          int
		found         bool
	}{
		{tcp4, ap("127.0.0.1:22"), ap("127.0.0.1:50001"), 0x1adb0, true},
		{tcp4, ap("127.0.0.1:22"), ap("127.0.0.1:50000"), 0, false},
		{tcp6, ap("10.0.0.2:22"), ap("10.0.0.1:50000"), 20000, true},
		{tcp6, ap("[::1]:22"), ap("[::1]:54321"), 1024, true},
		{tcp6, ap("10.0.0.2:22"), ap("10.0.0.3:50000"), 0, false},
	} {
		n, ok := txQueue(strings.NewReader(tc.table), tc.local, tc.remote)
		if n != tc.want || ok != tc.found {
			t.Errorf("%v from %v: %d, %v; want %d, %v", tc.local, tc.remote, n, ok, tc.want, tc.found)
		}
	}
}

func TestSSHBacklogNeedsAConnection(t *testing.T) {
	for _, v := range []string{"", "nonsense", "10.0.0.1 50000 10.0.0.2", "10.0.0.1 x 10.0.0.2 22"} {
		if sshBacklog(func(string) string { return v }) != nil {
			t.Errorf("SSH_CONNECTION=%q: watching a link", v)
		}
	}
	// A well-formed connection that is not in this machine's tables.
	if sshBacklog(func(string) string { return "192.0.2.1 50000 192.0.2.2 22" }) != nil {
		t.Error("watching a connection that does not exist")
	}
}
