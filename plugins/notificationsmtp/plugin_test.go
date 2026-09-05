package notificationsmtp

import (
	"bufio"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// testTemplate is the template every test writes to disk. It defines both
// required blocks and interpolates a value, so a test that changes the data can see it land.
const testTemplate = `{{define "subject"}}Your order {{.order_id}} has shipped{{end}}` +
	`{{define "body"}}Hello {{.name}}, order {{.order_id}} is on its way.{{end}}`

// writeTemplates writes the given files into a fresh directory and returns it.
func writeTemplates(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
	}

	return dir
}

// newSender builds a provider whose templates come from disk, without going
// through the plugin Host.
//
// The tests that exercise Send do not need the registration path, and building
// the struct directly keeps the failure they report pointed at the send logic
// rather than at the configuration reader.
func newSender(t *testing.T, host string, port int, plaintext bool) *sender {
	t.Helper()

	dir := writeTemplates(t, map[string]string{"order.shipped.tmpl": testTemplate})
	templates, err := loadTemplates(dir)
	require.NoError(t, err)

	return &sender{
		host:      host,
		port:      port,
		from:      "Store <no-reply@example.test>",
		templates: templates,
		plaintext: plaintext,
	}
}

// shipped is a valid notification the tests vary one field of at a time.
func shipped() coreprovider.Notification {
	return coreprovider.Notification{
		Channel:  coreprovider.ChannelEmail,
		To:       "customer@example.test",
		Template: "order.shipped",
		Data:     map[string]string{"order_id": "1001", "name": "Ada"},
	}
}

// --- the refusals -----------------------------------------------------------

// TestSendRefusesAChannelItDoesNotServe proves the contract's rule: a provider
// that sees a channel it does not support sends nothing and returns an error.
//
// Without this the SMS would be delivered as an email, the phone number would
// land in the To header, and the bounce would read in the delivery log as a bad
// address rather than as a misrouted channel.
func TestSendRefusesAChannelItDoesNotServe(t *testing.T) {
	s := newSender(t, "127.0.0.1", 1, false)

	n := shipped()
	n.Channel = coreprovider.ChannelSMS
	n.To = "+905550000000"

	err := s.Send(t.Context(), n)

	require.Error(t, err)
	assert.Equal(t, codeChannel, coreerrors.CodeOf(err))
	assert.True(t, coreerrors.IsInvalid(err),
		"an unsupported channel is a caller fault, not a server outage: %v", err)
}

// TestSendRefusesARecipientCarryingALineBreak is the header injection proof for
// the recipient.
//
// The address arrives from the notification module, which took it from an
// event; a CR in it would end the To header and let everything after it be read
// as further headers. The test asserts NO CONNECTION is attempted — the port is
// closed, so a provider that dialed before validating would fail with a
// connection error and a different code.
func TestSendRefusesARecipientCarryingALineBreak(t *testing.T) {
	s := newSender(t, "127.0.0.1", 1, false)

	for name, victim := range map[string]string{
		"a carriage return": "customer@example.test\rBcc: attacker@evil.test",
		"a line feed":       "customer@example.test\nBcc: attacker@evil.test",
		"a NUL byte":        "customer@example.test\x00",
	} {
		t.Run(name, func(t *testing.T) {
			n := shipped()
			n.To = victim

			err := s.Send(t.Context(), n)

			require.Error(t, err)
			assert.Equal(t, codeHeaderInjection, coreerrors.CodeOf(err),
				"the recipient has to be refused BEFORE the connection is opened")
			assert.NotContains(t, err.Error(), "attacker@evil.test",
				"the refused value must not be echoed into the error: it is attacker-controlled "+
					"and the message is copied into logs and error reports")
		})
	}
}

// TestSendRefusesASubjectRenderedWithALineBreak is the header injection proof
// for the SUBJECT, and it is the one that matters most.
//
// The recipient is at least an address the system produced. The subject is
// rendered from TEMPLATE DATA, and that data comes from an event payload — a
// customer name, a product title, values this process did not author. This is
// the only place an attacker-supplied string reaches a header, so the check has
// to happen AFTER rendering.
func TestSendRefusesASubjectRenderedWithALineBreak(t *testing.T) {
	s := newSender(t, "127.0.0.1", 1, false)

	n := shipped()
	// The order id lands inside the subject template.
	n.Data["order_id"] = "1001\r\nBcc: attacker@evil.test"

	err := s.Send(t.Context(), n)

	require.Error(t, err)
	assert.Equal(t, codeHeaderInjection, coreerrors.CodeOf(err),
		"a line break that arrives through template DATA has to be caught after rendering")
}

// TestSendRefusesAnUnknownTemplate proves that a missing template is a loud
// failure and not an empty email.
//
// A generic fallback body was the rejected alternative: the customer would get
// a message that says nothing and the delivery log would record a success.
func TestSendRefusesAnUnknownTemplate(t *testing.T) {
	s := newSender(t, "127.0.0.1", 1, false)

	n := shipped()
	n.Template = "order.refunded"

	err := s.Send(t.Context(), n)

	require.Error(t, err)
	assert.Equal(t, codeTemplateUnknown, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "order.shipped",
		"the error has to name what IS loaded; otherwise the operator cannot tell a typo "+
			"in the template name from a template that was never deployed")
}

// --- template loading -------------------------------------------------------

// TestLoadTemplatesRefusesAFileMissingABlock proves the startup check.
//
// A template without a subject block would otherwise mail a customer a message
// with an empty subject line, at send time, inside an event subscriber — and
// nothing in the system would record that as a fault.
func TestLoadTemplatesRefusesAFileMissingABlock(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"order.shipped.tmpl": `{{define "body"}}no subject here{{end}}`,
	})

	_, err := loadTemplates(dir)

	require.Error(t, err)
	assert.Equal(t, codeTemplateLoad, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), subjectTemplate)
}

// TestLoadTemplatesRefusesAnEmptyDirectory proves that a directory with no
// templates is a configuration error rather than a provider that starts and
// then refuses every send it is given.
func TestLoadTemplatesRefusesAnEmptyDirectory(t *testing.T) {
	_, err := loadTemplates(t.TempDir())

	require.Error(t, err)
	assert.Equal(t, codeTemplateLoad, coreerrors.CodeOf(err))
}

// TestLoadTemplatesSkipsNonTemplateFiles proves a README or an editor backup
// sitting next to the templates does not stop startup.
func TestLoadTemplatesSkipsNonTemplateFiles(t *testing.T) {
	dir := writeTemplates(t, map[string]string{
		"order.shipped.tmpl": testTemplate,
		"README.md":          "# how to edit these",
		"order.shipped.bak":  "{{ this would not parse",
	})

	templates, err := loadTemplates(dir)

	require.NoError(t, err)
	assert.Equal(t, []string{"order.shipped"}, templateNames(templates))
}

// --- the message ------------------------------------------------------------

// TestTheSubjectIsEncodedForNonASCII proves the RFC 2047 encoding.
//
// This repository's own language is not ASCII and neither is its first
// installation's. A subject carrying accented letters sent as raw bytes arrives
// as mojibake in most clients — a fault nobody reports as a bug because the
// email did arrive.
//
// The subject is written with escape sequences rather than as literal text: the
// point of the case is that the bytes are NOT ASCII, and a literal would put
// this file in front of the repository's language detector, where an escaped
// value cannot silently be "fixed" into ASCII and quietly neuter the test.
func TestTheSubjectIsEncodedForNonASCII(t *testing.T) {
	// "Siparisiniz kargolandi" with the Turkish letters restored.
	subject := "Sipari\u015finiz kargoland\u0131"

	msg := string(buildMessage(
		"store@example.test", "customer@example.test", subject, "body"))

	assert.NotContains(t, msg, subject,
		"a non-ASCII subject must not go out as raw bytes")
	assert.Contains(t, msg, "Subject: =?utf-8?q?",
		"the subject has to be in RFC 2047 encoded-word form")
}

// TestTheBodyIsBase64WithinTheLineLimit proves the transfer encoding.
//
// RFC 5321 caps a line at 1000 octets. A template rendering a long URL would
// exceed it, and the server's refusal names the line length and nothing about
// the template.
func TestTheBodyIsBase64WithinTheLineLimit(t *testing.T) {
	long := strings.Repeat("https://example.test/orders/1001/track ", 80)

	msg := string(buildMessage("store@example.test", "customer@example.test", "Subject", long))

	assert.Contains(t, msg, "Content-Transfer-Encoding: base64")
	assert.NotContains(t, msg, "https://example.test",
		"the body must not appear unencoded")
	for _, line := range strings.Split(msg, "\r\n") {
		assert.LessOrEqual(t, len(line), 998,
			"no line may exceed the RFC 5321 limit; the offending line was %d octets", len(line))
	}
}

// --- configuration ----------------------------------------------------------

// TestReadPlaintextOnlyAcceptsTheExactWord proves the opt-out is hard to hit by
// accident.
//
// A boolean parser that also took "1", "yes" or "on" would make it easier to
// end up unencrypted by mistake, and the mistake costs the SMTP password in
// clear text on the wire.
func TestReadPlaintextOnlyAcceptsTheExactWord(t *testing.T) {
	for _, raw := range []string{"1", "yes", "on", "TRUE", "True"} {
		t.Run(raw, func(t *testing.T) {
			_, err := readPlaintext(hostWith(t, map[string]string{settingPlaintext: raw}))

			require.Error(t, err, "%q must not enable an unencrypted connection", raw)
			assert.Equal(t, codeInvalidSetting, coreerrors.CodeOf(err))
		})
	}
}

// --- delivery ---------------------------------------------------------------

// TestSendRefusesAServerWithoutSTARTTLS proves the encryption default.
//
// net/smtp already refuses to send the PASSWORD over a cleartext link, which is
// exactly why this test exists: a relay needing no authentication would
// otherwise mail every order confirmation — name, address, what was bought —
// across the network in the clear, with nothing reporting a fault.
func TestSendRefusesAServerWithoutSTARTTLS(t *testing.T) {
	addr, _ := fakeSMTP(t)
	host, port := splitAddr(t, addr)

	s := newSender(t, host, port, false)

	err := s.Send(t.Context(), shipped())

	require.Error(t, err)
	assert.Equal(t, codeInsecure, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), settingPlaintext,
		"the refusal has to name the setting that would accept the risk deliberately")
}

// TestSendDeliversOverAnAcceptedPlaintextConnection proves the whole path and
// the message it produces.
//
// The plaintext opt-out is used because a TLS handshake would need a
// certificate the test would have to mint; the message assembly and the SMTP
// conversation are what this test is for, and neither changes with TLS.
func TestSendDeliversOverAnAcceptedPlaintextConnection(t *testing.T) {
	addr, received := fakeSMTP(t)
	host, port := splitAddr(t, addr)

	s := newSender(t, host, port, true)

	require.NoError(t, s.Send(t.Context(), shipped()))

	select {
	case msg := <-received:
		assert.Contains(t, msg, "To: customer@example.test")
		assert.Contains(t, msg, "From: Store <no-reply@example.test>")
		assert.Contains(t, msg, "Content-Type: text/plain; charset=utf-8")
		assert.Contains(t, msg, "Subject: Your order 1001 has shipped",
			"an ASCII subject stays readable rather than being encoded needlessly")
	case <-time.After(5 * time.Second):
		t.Fatal("the fake server received no message")
	}
}

// TestSendHonorsAContextDeadline proves the deadline reaches the connection.
//
// A dial timeout alone would miss this case: the server here ACCEPTS the
// connection and then says nothing, which is how a wedged mail server behaves
// and how an event subscriber ends up stopped with nothing in the logs.
func TestSendHonorsAContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck // test cleanup

	// Accept and stay silent: no greeting is ever written.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		<-t.Context().Done()
		conn.Close() //nolint:errcheck // test cleanup
	}()

	host, port := splitAddr(t, ln.Addr().String())
	s := newSender(t, host, port, true)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	err = s.Send(ctx, shipped())

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second,
		"the send has to end at the caller's deadline, not at the backstop")
	assert.Equal(t, codeSendFailed, coreerrors.CodeOf(err))
}

// --- helpers ----------------------------------------------------------------

// fakeSMTP starts a minimal SMTP server that does NOT advertise STARTTLS.
//
// It speaks just enough of the protocol for one message and publishes the body
// it received on the returned channel.
func fakeSMTP(t *testing.T) (addr string, received chan string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() }) //nolint:errcheck // test cleanup

	received = make(chan string, 1)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveOne(conn, received)
		}
	}()

	return ln.Addr().String(), received
}

// serveOne runs the SMTP dialog for a single connection.
func serveOne(conn net.Conn, received chan<- string) {
	defer conn.Close() //nolint:errcheck // test helper

	r := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }

	w("220 fake ESMTP")

	var body strings.Builder
	inData := false

	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")

		if inData {
			if line == "." {
				inData = false
				select {
				case received <- body.String():
				default:
				}
				w("250 OK queued")

				continue
			}
			body.WriteString(line + "\r\n")

			continue
		}

		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			// STARTTLS is deliberately NOT advertised: that is what the
			// encryption-default test needs to see.
			w("250-fake")
			w("250 SIZE 10240000")
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			w("250 OK")
		case strings.HasPrefix(line, "DATA"):
			inData = true
			w("354 go ahead")
		case strings.HasPrefix(line, "QUIT"):
			w("221 bye")

			return
		default:
			w("250 OK")
		}
	}
}

// hostWith builds a plugin Host carrying just the given settings.
//
// The other Host dependencies are nil: the configuration readers under test
// touch nothing but the settings map, and passing real ones would make a
// failure here ambiguous about which of them broke.
func hostWith(t *testing.T, settings map[string]string) *coreplugin.Host {
	t.Helper()

	return coreplugin.NewHost(nil, nil, nil, nil, settings)
}

// splitAddr breaks a "host:port" into its parts.
func splitAddr(t *testing.T, addr string) (host string, port int) {
	t.Helper()

	host, rawPort, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	port, err = strconv.Atoi(rawPort)
	require.NoError(t, err)

	return host, port
}
