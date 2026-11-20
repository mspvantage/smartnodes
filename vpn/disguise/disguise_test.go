package disguise

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestKeysAndValidation(t *testing.T) {
	k, err := NewServerKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Check(); err != nil {
		t.Fatal(err)
	}
	r := k.Published(443, "www.example.com")
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]Reality{
		"no port":      {PublicKey: r.PublicKey, ShortID: r.ShortID, ServerName: r.ServerName, UUID: r.UUID},
		"short key":    {Port: 443, PublicKey: "abc", ShortID: r.ShortID, ServerName: r.ServerName, UUID: r.UUID},
		"odd short id": {Port: 443, PublicKey: r.PublicKey, ShortID: "abc", ServerName: r.ServerName, UUID: r.UUID},
		"bad name":     {Port: 443, PublicKey: r.PublicKey, ShortID: r.ShortID, ServerName: "not a name", UUID: r.UUID},
		"bad uuid":     {Port: 443, PublicKey: r.PublicKey, ShortID: r.ShortID, ServerName: r.ServerName, UUID: "x"},
	}
	for name, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	back, err := ParseCompact(r.Compact())
	if err != nil || back != r {
		t.Fatalf("compact form: %+v %v", back, err)
	}
	if _, err := ParseCompact("443/x"); err == nil {
		t.Fatal("short compact form accepted")
	}
	other, _ := NewServerKeys()
	k.PublicKey = other.PublicKey
	if k.Check() == nil {
		t.Fatal("mismatched key pair accepted")
	}
}

func TestConfigsKeepNoLogsAndRefuseOtherPlaces(t *testing.T) {
	k, _ := NewServerKeys()
	cfg, err := ServerConfig(k, netip.MustParseAddrPort("0.0.0.0:443"), "www.example.com:443", "www.example.com", netip.MustParseAddrPort("203.0.113.5:51820"))
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(cfg, &v); err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{`"access": "none"`, `"loglevel": "none"`, `"203.0.113.5/32"`, `"port": "51820"`, `"outboundTag": "refuse"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("server config lacks %s:\n%s", want, s)
		}
	}
	if _, err := ClientConfig(k.Published(443, "www.example.com"), netip.MustParseAddr("203.0.113.5"),
		netip.MustParseAddrPort("0.0.0.0:20000"), netip.MustParseAddrPort("203.0.113.5:51820")); err == nil {
		t.Fatal("client listening beyond loopback accepted")
	}
}

func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	p := uint16(l.Addr().(*net.TCPAddr).Port)
	// The same number must be free for UDP too.
	u, err := net.ListenPacket("udp", l.Addr().String())
	if err != nil {
		return freePort(t)
	}
	u.Close()
	return p
}

// echo answers on TCP and UDP at one port, as a node's WireGuard and
// payment port share one.
func echo(t *testing.T, tag string) netip.AddrPort {
	t.Helper()
	p := freePort(t)
	addr := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), p)
	tl, err := net.Listen("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	ul, err := net.ListenPacket("udp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tl.Close(); ul.Close() })
	go func() {
		for {
			c, err := tl.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 1024)
				n, _ := c.Read(buf)
				_, _ = c.Write(append([]byte(tag+":"), buf[:n]...))
			}()
		}
	}()
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := ul.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = ul.WriteTo(append([]byte(tag+":"), buf[:n]...), from)
		}
	}()
	return addr
}

func xrayPath(t *testing.T) string {
	if p := os.Getenv("XRAY"); p != "" {
		return p
	}
	if p, err := exec.LookPath("xray"); err == nil {
		return p
	}
	t.Skip("xray not installed; set XRAY to run the end-to-end test")
	return ""
}

func run(t *testing.T, xray, name string, cfg []byte) {
	t.Helper()
	path, err := WriteConfig(t.TempDir(), name, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(xray, "run", "-test", "-c", path).CombinedOutput(); err != nil {
		t.Fatalf("xray rejects the %s config: %v\n%s", name, err, out)
	}
	p, err := Start(xray, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
}

// The whole path: a device's TCP and UDP reach the node's port through the
// disguise; nothing else can be reached through it; and a censor that
// connects to test the node is shown the cover site.
func TestThroughRealXray(t *testing.T) {
	xray := xrayPath(t)

	cover := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "an ordinary website")
	}))
	cover.EnableHTTP2 = true
	// Probing visitors make the cover site log failed handshakes; not news.
	cover.Config.ErrorLog = log.New(io.Discard, "", 0)
	cover.StartTLS()
	defer cover.Close()

	allowed := echo(t, "node")
	forbidden := echo(t, "elsewhere")
	k, _ := NewServerKeys()
	listen := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), freePort(t))
	srv, err := ServerConfig(k, listen, strings.TrimPrefix(cover.URL, "https://"), "example.com", allowed)
	if err != nil {
		t.Fatal(err)
	}
	run(t, xray, "server.json", srv)

	pub := k.Published(listen.Port(), "example.com")
	local := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), freePort(t))
	cli, err := ClientConfig(pub, listen.Addr(), local, allowed)
	if err != nil {
		t.Fatal(err)
	}
	run(t, xray, "client.json", cli)
	localBad := netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), freePort(t))
	cliBad, _ := ClientConfig(pub, listen.Addr(), localBad, forbidden)
	run(t, xray, "client-bad.json", cliBad)

	for _, a := range []string{listen.String(), local.String(), localBad.String()} {
		if err := WaitListening(a, 10*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	// TCP, as the payment connection uses.
	tcp := func(addr string) string {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			return ""
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(5 * time.Second))
		_, _ = c.Write([]byte("pay"))
		buf := make([]byte, 64)
		n, _ := c.Read(buf)
		return string(buf[:n])
	}
	if got := tcp(local.String()); got != "node:pay" {
		t.Fatalf("TCP through the disguise: %q", got)
	}
	// UDP, as WireGuard uses.
	udp := func(addr string) string {
		c, err := net.Dial("udp", addr)
		if err != nil {
			return ""
		}
		defer c.Close()
		buf := make([]byte, 2048)
		for i := 0; i < 5; i++ {
			_, _ = c.Write(bytes.Repeat([]byte("w"), 148))
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			if n, err := c.Read(buf); err == nil {
				return string(buf[:n])
			}
		}
		return ""
	}
	if got := udp(local.String()); got != "node:"+strings.Repeat("w", 148) {
		t.Fatalf("UDP through the disguise: %q", got)
	}
	// Nowhere else.
	if got := tcp(localBad.String()); strings.HasPrefix(got, "elsewhere") {
		t.Fatal("the disguise reached a place other than the node's port")
	}
	if got := udp(localBad.String()); strings.HasPrefix(got, "elsewhere") {
		t.Fatal("the disguise passed UDP to a place other than the node's port")
	}

	// A censor's test connection is shown the cover site's own certificate.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", listen.String(),
		&tls.Config{ServerName: "example.com", InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("a plain TLS visit failed: %v", err)
	}
	got := conn.ConnectionState().PeerCertificates[0].Raw
	conn.Close()
	if !bytes.Equal(got, cover.Certificate().Raw) {
		t.Fatal("a censor's visit did not see the cover site's certificate")
	}
}
