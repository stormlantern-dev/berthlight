package tui

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// Over SSH, a write to the terminal returns as soon as sshd has the bytes,
// and sshd and the socket buffer a megabyte or more of them: enough for
// half the Amiga scene, all of it queued for a slow link, with the
// command's output behind it. Measured through a real sshd with the link
// throttled to 200 KB/s, the scene took 9.3 s instead of 4. So over SSH the
// header watches the connection itself: the bytes its socket has sent that
// the client has not acknowledged yet, from the kernel's socket table.
// That shows what is really on the way to the terminal, and the writer
// waits for it to drain before it draws the next frame.

// sshBacklog returns a function that reports the bytes the SSH connection
// this process runs under (SSH_CONNECTION) has sent and the client has not
// acknowledged, or nil where that cannot be seen: not over SSH, not on
// Linux, or not in this network namespace.
func sshBacklog(getenv func(string) string) func() (int, bool) {
	f := strings.Fields(getenv("SSH_CONNECTION"))
	if len(f) != 4 {
		return nil
	}
	client, err1 := netip.ParseAddrPort(joinHostPort(f[0], f[1]))
	server, err2 := netip.ParseAddrPort(joinHostPort(f[2], f[3]))
	if err1 != nil || err2 != nil {
		return nil
	}
	client, server = plain(client), plain(server)
	backlog := func() (int, bool) {
		for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
			file, err := os.Open(table)
			if err != nil {
				continue
			}
			n, ok := txQueue(file, server, client)
			file.Close()
			if ok {
				return n, true
			}
		}
		return 0, false
	}
	if _, ok := backlog(); !ok {
		return nil
	}
	return backlog
}

// plain drops what the kernel's tables leave out: an IPv6 zone, and IPv4
// mapped into IPv6.
func plain(a netip.AddrPort) netip.AddrPort {
	return netip.AddrPortFrom(a.Addr().WithZone("").Unmap(), a.Port())
}

func joinHostPort(host, port string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// txQueue finds the connection from local to remote in a kernel socket
// table (the format of /proc/net/tcp and tcp6) and returns its tx_queue:
// for an established connection, the bytes sent and not yet acknowledged.
func txQueue(table io.Reader, local, remote netip.AddrPort) (int, bool) {
	s := bufio.NewScanner(table)
	s.Scan() // the column names
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 5 {
			continue
		}
		l, ok1 := procAddr(f[1])
		r, ok2 := procAddr(f[2])
		if !ok1 || !ok2 || l != local || r != remote {
			continue
		}
		tx, _, _ := strings.Cut(f[4], ":")
		n, err := strconv.ParseUint(tx, 16, 32)
		return int(n), err == nil
	}
	return 0, false
}

// procAddr parses an address of the kernel's socket tables: the address as
// 32-bit words in hex, each in host byte order (little endian on the
// machines berthlight runs on), a colon, and the port in hex. An IPv4
// address mapped into IPv6 comes back as IPv4.
func procAddr(s string) (netip.AddrPort, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	var b [16]byte
	for i := 0; i < len(raw); i += 4 {
		binary.BigEndian.PutUint32(b[i:], binary.LittleEndian.Uint32(raw[i:]))
	}
	var addr netip.Addr
	if len(raw) == 4 {
		addr = netip.AddrFrom4([4]byte(b[:4]))
	} else {
		addr = netip.AddrFrom16(b).Unmap()
	}
	return netip.AddrPortFrom(addr, uint16(port)), true
}
