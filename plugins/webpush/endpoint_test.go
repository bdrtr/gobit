package webpush

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The rule a device's endpoint is held to, and the guard the sender dials
// through (ADR 0440, D292).

// TestAnAddressInsideTheNetworkIsRefused classifies the addresses a stranger
// would point the sender at, each also written as IPv4-mapped IPv6, which a
// resolver may answer and which reaches the same host.
func TestAnAddressInsideTheNetworkIsRefused(t *testing.T) {
	refused := []string{
		"127.0.0.1", "127.8.9.10", "::1",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1", "fc00::1", "fd12:3456::1",
		"169.254.169.254", "fe80::1",
		"224.0.0.1", "239.255.255.250", "ff02::1", "ff05::2",
		"0.0.0.0", "::", "0.1.2.3",
		"100.64.0.1", "100.100.100.200", "192.0.0.192", "198.18.0.1", "240.0.0.1", "255.255.255.255",
		"192.0.2.1", "198.51.100.1", "203.0.113.1", "2001:db8::1", "3fff::1",
		"64:ff9b::a00:1", "64:ff9b:1::1", "2002:a00:1::1", "2001::1", "100::1", "fec0::1",
		"::7f00:1", "::ffff:0:a00:1",
	}
	for _, raw := range refused {
		addr := netip.MustParseAddr(raw)
		assert.NotEmpty(t, forbiddenAddress(addr), "%s has to be refused", raw)
		if addr.Is4() {
			mapped := netip.AddrFrom16(addr.As16())
			assert.NotEmpty(t, forbiddenAddress(mapped), "%s, written %s, has to be refused", raw, mapped)
		}
	}

	for _, raw := range []string{"8.8.8.8", "142.250.185.10", "17.253.144.10", "2a00:1450:4001:80b::200a", "2001:4860:4860::8888"} {
		addr := netip.MustParseAddr(raw)
		assert.Empty(t, forbiddenAddress(addr), "%s is a public address", raw)
		if addr.Is4() {
			assert.Empty(t, forbiddenAddress(netip.AddrFrom16(addr.As16())), "%s mapped is the same host", raw)
		}
	}

	assert.NotEmpty(t, forbiddenAddress(netip.Addr{}), "the zero address is no address")
}

// TestAnEndpointIsAdmittedOnlyAtAPushService holds the subscription rule to the
// four browsers' push services, as their endpoints are written.
func TestAnEndpointIsAdmittedOnlyAtAPushService(t *testing.T) {
	var none pushServices

	for _, endpoint := range []string{
		"https://fcm.googleapis.com/fcm/send/dXJ0aWNsZQ:APA91b",
		"https://fcm.googleapis.com/wp/dXJ0aWNsZQ",
		"https://android.googleapis.com/gcm/send/abc",
		"https://updates.push.services.mozilla.com/wpush/v2/gAAAAABk",
		"https://web.push.apple.com/QGkUVsXwXN",
		"https://wns2-by3p.notify.windows.com/w/?token=BQYAAAB",
		"https://FCM.googleapis.com/fcm/send/upper",
		"https://fcm.googleapis.com:443/fcm/send/port",
	} {
		assert.Empty(t, none.refusal(endpoint), "%s is a push service's", endpoint)
	}

	for _, endpoint := range []string{
		"",
		"/fcm/send/relative",
		"fcm.googleapis.com/fcm/send/no-scheme",
		"http://fcm.googleapis.com/fcm/send/plain",
		"https://fcm.googleapis.com:8443/fcm/send/port",
		"https://user:pw@fcm.googleapis.com/fcm/send/credentials",
		"https://fcm.googleapis.com.attacker.test/fcm/send/x",
		"https://evilfcm.googleapis.com/x",
		"https://googleapis.com/x",
		"https://push.apple.com/x",
		"https://.push.apple.com/x",
		"https://a..push.apple.com/x",
		"https://web.push.apple.com./x",
		"https://notify.windows.com.attacker.test/x",
		"https://attacker.test/x",
		"https://127.0.0.1/x",
		"https://[::ffff:127.0.0.1]/x",
		"https://169.254.169.254/latest/meta-data/",
		"mailto:ops@fcm.googleapis.com",
		"javascript:alert(1)",
	} {
		assert.NotEmpty(t, none.refusal(endpoint), "%q has to be refused", endpoint)
	}
}

// TestARefusalNamesTheHostAndNotTheEndpoint keeps the capability out of an
// answer and a log line: the path is what pushes to the browser.
func TestARefusalNamesTheHostAndNotTheEndpoint(t *testing.T) {
	var none pushServices

	why := none.refusal("https://attacker.test/SECRET-PATH?token=SECRET")
	assert.Contains(t, why, "attacker.test")
	assert.NotContains(t, why, "SECRET")
	assert.Contains(t, why, settingPushServices, "the refusal names the setting that lists more")
}

// TestTheOperatorsListIsReadStrictly refuses an entry that would admit more
// than it names.
func TestTheOperatorsListIsReadStrictly(t *testing.T) {
	for _, raw := range []string{
		"push.example.test",
		"http://push.example.test",
		"https://push.example.test/path",
		"https://push.example.test?x=1",
		"https://user@push.example.test",
		"https://*.example.test",
		"ftp://push.example.test",
		"https://ok.example.test, http://10.0.0.1:8080",
	} {
		_, err := parsePushServices(raw)
		require.Error(t, err, "%q has to stop startup", raw)
		assert.True(t, coreerrors.HasKind(err, coreerrors.KindInvalid), "%q: %v", raw, err)
	}

	services, err := parsePushServices(" https://Push.Example.test , http://127.0.0.1:8080,,http://localhost:9000/ ,http://[::1]:7000")
	require.NoError(t, err)
	assert.Equal(t, []origin{
		{scheme: "https", host: "push.example.test", port: "443"},
		{scheme: "http", host: "127.0.0.1", port: "8080"},
		{scheme: "http", host: "localhost", port: "9000"},
		{scheme: "http", host: "::1", port: "7000"},
	}, services.listed)

	empty, err := parsePushServices("")
	require.NoError(t, err)
	assert.Empty(t, empty.listed)
}

// TestAnOperatorsOriginIsAdmittedExactly admits a listed origin's scheme, host
// and port and nothing beside them.
func TestAnOperatorsOriginIsAdmittedExactly(t *testing.T) {
	services, err := parsePushServices("http://127.0.0.1:8080,https://push.example.test")
	require.NoError(t, err)

	for _, endpoint := range []string{
		"http://127.0.0.1:8080/push/1",
		"https://push.example.test/p",
		"https://push.example.test:443/p",
		"https://fcm.googleapis.com/fcm/send/x",
	} {
		assert.Empty(t, services.refusal(endpoint), "%s is admitted", endpoint)
	}
	for _, endpoint := range []string{
		"http://127.0.0.1:8081/push/1",
		"https://127.0.0.1:8080/push/1",
		"http://127.0.0.2:8080/push/1",
		"http://push.example.test/p",
		"https://push.example.test:8443/p",
		"https://sub.push.example.test/p",
	} {
		assert.NotEmpty(t, services.refusal(endpoint), "%s is refused", endpoint)
	}

	assert.True(t, services.trustsAddr("127.0.0.1:8080"))
	assert.True(t, services.trustsAddr("PUSH.example.test:443"))
	assert.False(t, services.trustsAddr("127.0.0.1:8081"))
	assert.False(t, services.trustsAddr("fcm.googleapis.com:443"), "a built-in host is never trusted blind")
}

// recordingDial is a dial that records where it was asked to connect.
type recordingDial struct {
	calls []string
}

func (d *recordingDial) dial(_ context.Context, _, addr string) (net.Conn, error) {
	d.calls = append(d.calls, addr)
	client, server := net.Pipe()
	_ = server.Close()

	return client, nil
}

// resolver answers each host from a table, and counts the lookups.
func resolver(table map[string][]string, lookups *atomic.Int32) func(context.Context, string, string) ([]netip.Addr, error) {
	return func(_ context.Context, _, host string) ([]netip.Addr, error) {
		lookups.Add(1)
		var out []netip.Addr
		for _, raw := range table[host] {
			out = append(out, netip.MustParseAddr(raw))
		}

		return out, nil
	}
}

// TestTheGuardRefusesANameThatResolvesInside is the DNS half: a name the
// subscription rule admitted is refused when it resolves to an address inside
// the network, whatever else it resolves to, and nothing is dialed.
func TestTheGuardRefusesANameThatResolvesInside(t *testing.T) {
	for name, answer := range map[string][]string{
		"loopback":                {"127.0.0.1"},
		"metadata":                {"169.254.169.254"},
		"metadata, mapped":        {"::ffff:169.254.169.254"},
		"private beside public":   {"8.8.8.8", "10.0.0.1"},
		"unique-local":            {"fd00::1"},
		"loopback, mapped":        {"::ffff:127.0.0.1"},
		"unspecified":             {"0.0.0.0"},
		"multicast":               {"ff02::1"},
		"provider-shared address": {"100.100.100.200"},
	} {
		t.Run(name, func(t *testing.T) {
			var lookups atomic.Int32
			dialer := &recordingDial{}
			guard := &dialGuard{
				lookup:  resolver(map[string][]string{"fcm.googleapis.com": answer}, &lookups),
				dial:    dialer.dial,
				trusted: func(string) bool { return false },
			}

			conn, err := guard.dialContext(t.Context(), "tcp", "fcm.googleapis.com:443")
			require.Error(t, err)
			assert.Nil(t, conn)
			assert.ErrorIs(t, err, errAddressRefused)
			assert.Empty(t, dialer.calls, "nothing may be dialed for a refused name")
			assert.Equal(t, int32(1), lookups.Load())
		})
	}
}

// TestTheGuardDialsTheAddressItChecked connects to the address it resolved and
// judged rather than handing the name back to a second lookup.
func TestTheGuardDialsTheAddressItChecked(t *testing.T) {
	var lookups atomic.Int32
	dialer := &recordingDial{}
	guard := &dialGuard{
		lookup:  resolver(map[string][]string{"fcm.googleapis.com": {"::ffff:142.250.185.10", "2a00:1450:4001:80b::200a"}}, &lookups),
		dial:    dialer.dial,
		trusted: func(string) bool { return false },
	}

	conn, err := guard.dialContext(t.Context(), "tcp", "fcm.googleapis.com:443")
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
	assert.Equal(t, []string{"142.250.185.10:443"}, dialer.calls)
}

// TestTheGuardDialsAnOperatorsOriginAndTheProxyWithoutAsking reaches what the
// operator configured, where it is.
func TestTheGuardDialsAnOperatorsOriginAndTheProxyWithoutAsking(t *testing.T) {
	services, err := parsePushServices("https://push.internal.test:8443")
	require.NoError(t, err)

	var lookups atomic.Int32
	dialer := &recordingDial{}
	guard := &dialGuard{
		lookup:  resolver(map[string][]string{"10.0.0.6": {"10.0.0.6"}}, &lookups),
		dial:    dialer.dial,
		trusted: services.trustsAddr,
		proxy: func(*http.Request) (*url.URL, error) {
			return url.Parse("http://10.0.0.5:3128")
		},
	}

	conn, err := guard.dialContext(t.Context(), "tcp", "push.internal.test:8443")
	require.NoError(t, err)
	_ = conn.Close()

	_, err = guard.dialContext(t.Context(), "tcp", "10.0.0.5:3128")
	require.Error(t, err, "a proxy the transport was never told of is an address like any other")

	proxy, err := guard.proxyFor(httptest.NewRequest(http.MethodPost, "https://fcm.googleapis.com/fcm/send/x", http.NoBody))
	require.NoError(t, err)
	require.NotNil(t, proxy)
	conn, err = guard.dialContext(t.Context(), "tcp", "10.0.0.5:3128")
	require.NoError(t, err)
	_ = conn.Close()

	_, err = guard.dialContext(t.Context(), "tcp", "10.0.0.6:3128")
	require.ErrorIs(t, err, errAddressRefused, "a remembered proxy trusts its own address and no other")

	assert.Equal(t, []string{"push.internal.test:8443", "10.0.0.5:3128"}, dialer.calls)
	assert.Equal(t, int32(2), lookups.Load(),
		"the unannounced proxy address and the address beside the announced one were looked up")
}

// countingServer is a local server that counts what reached it.
func countingServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)

	return server, &hits
}

// TestTheClientReachesNoLoopbackServiceThroughAName drives the real transport:
// a name answering 127.0.0.1 reaches nothing listening there.
func TestTheClientReachesNoLoopbackServiceThroughAName(t *testing.T) {
	server, hits := countingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)

	var lookups atomic.Int32
	client := newGuardedClient(&dialGuard{
		lookup:  resolver(map[string][]string{"push.rebound.test": {"127.0.0.1"}}, &lookups),
		dial:    (&net.Dialer{Timeout: time.Second}).DialContext,
		trusted: func(string) bool { return false },
		proxy:   func(*http.Request) (*url.URL, error) { return nil, nil },
	})

	resp, err := client.Post("http://push.rebound.test:"+port+"/p", "application/octet-stream", nil)
	if resp != nil {
		_ = resp.Body.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, errAddressRefused)
	assert.Zero(t, hits.Load(), "the loopback service must hear nothing")
}

// TestARedirectIsNotFollowed hands a redirect back as the answer: the address
// it names hears nothing.
func TestARedirectIsNotFollowed(t *testing.T) {
	elsewhere, elsewhereHits := countingServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusCreated) })
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		push, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", elsewhere.URL+"/after")
			w.WriteHeader(status)
		})
		services, err := parsePushServices(push.URL + "," + elsewhere.URL)
		require.NoError(t, err)

		resp, err := newPushClient(services).Post(push.URL+"/p", "application/octet-stream", strings.NewReader("x"))
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, status, resp.StatusCode)
	}
	assert.Zero(t, elsewhereHits.Load(), "a redirect's target must hear nothing, even one the list admits")
}

// testSender is a sender over the guard, with a signing key.
func testSender(t *testing.T, services pushServices, guard *dialGuard) *sender {
	t.Helper()

	privateKey, _, err := GenerateKey()
	require.NoError(t, err)
	key, err := parseVAPIDKey(privateKey)
	require.NoError(t, err)

	client := newPushClient(services)
	if guard != nil {
		client = newGuardedClient(guard)
	}

	return &sender{client: client, services: services, key: key, subject: "mailto:ops@example.test", now: time.Now}
}

// TestAFailedPushNamesNoEndpoint keeps the capability out of the error a
// failed push is logged with (D294): the client's error names the URL.
func TestAFailedPushNamesNoEndpoint(t *testing.T) {
	var lookups atomic.Int32
	s := testSender(t, pushServices{}, &dialGuard{
		lookup:  resolver(map[string][]string{"fcm.googleapis.com": {"10.0.0.1"}}, &lookups),
		dial:    (&recordingDial{}).dial,
		trusted: func(string) bool { return false },
		proxy:   func(*http.Request) (*url.URL, error) { return nil, nil },
	})

	out, err := s.send(t.Context(), subscription{
		ID:       "wps_TEST",
		Endpoint: "https://fcm.googleapis.com/fcm/send/SECRET-REGISTRATION-TOKEN",
	}, []byte("sealed"), "")
	require.Error(t, err)
	assert.Equal(t, outcomeFailed, out, "a name resolving inside is a fault to keep, not a dead device")
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable), "%v", err)
	assert.ErrorIs(t, err, errAddressRefused)
	assert.NotContains(t, err.Error(), "SECRET", "the endpoint is the device's capability")
	assert.Contains(t, err.Error(), "fcm.googleapis.com", "the host is what the operator needs")
}

// TestARedirectAnswerKeepsTheDevice counts a redirect as a failed push: it is
// not the push service saying the device is gone.
func TestARedirectAnswerKeepsTheDevice(t *testing.T) {
	push, _ := countingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://127.0.0.1:1/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	services, err := parsePushServices(push.URL)
	require.NoError(t, err)
	s := testSender(t, services, nil)

	out, err := s.send(t.Context(), subscription{ID: "wps_TEST", Endpoint: push.URL + "/p"}, []byte("sealed"), "")
	require.Error(t, err)
	assert.Equal(t, outcomeFailed, out)
	assert.Contains(t, err.Error(), "redirect")
}

// TestTheSendersClientIsGuarded is the wiring: the client Register builds
// dials through the guard and follows no redirect.
func TestTheSendersClientIsGuarded(t *testing.T) {
	client := newPushClient(pushServices{})

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	require.NotNil(t, client.CheckRedirect)
	assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)

	_, err := transport.DialContext(t.Context(), "tcp", "127.0.0.1:1")
	require.Error(t, err)
	assert.ErrorIs(t, err, errAddressRefused)
}
