// Package notificationsmtp delivers gobit's notifications over SMTP.
//
// It is the first notification provider in this repository that actually
// SENDS. The one that comes in the box (internal/modules/notification/logonly)
// only writes a log line, and its own documentation says why: gobit is a
// framework and cannot know which mail service an installation will use. This
// plugin answers that for the most widely available answer there is — a plain
// SMTP server — and it brings NO new dependency while doing it: net/smtp,
// crypto/tls, mime and text/template are all standard library. That follows the
// decision already made for the error reporters (ADR 0014), where the OTLP body
// and the Sentry envelope were hand-written rather than pulled in as a package.
//
// # The copy is NOT in the framework
//
// gobit ships no email wording. The templates come from a directory the
// installation owns (SMTP_TEMPLATE_DIR) and the plugin refuses to start
// without it. Embedding a default set was rejected: a framework that ships
// English boilerplate for "your order has shipped" produces a store that mails
// its customers in the wrong language and the wrong voice, and the day someone
// notices is the day a customer complains.
//
// Templates are read and PARSED AT STARTUP. A syntax error in a template is a
// configuration error, and configuration errors belong at startup, not at
// 03:00 inside an event subscriber where the failure surfaces as a
// notification that silently never arrived.
//
// # What it refuses to do
//
// It refuses a channel it does not serve, an unknown template, a recipient or
// a subject carrying a line break, and — unless the installation opts out in
// so many words — an unencrypted connection. Each of those refusals replaces a
// failure that would otherwise be silent; see the method documentation for the
// individual reasoning.
//
// # Usage
//
//	PLUGINS=notification-smtp
//	SMTP_HOST=smtp.example.test
//	SMTP_FROM="Store <no-reply@example.test>"
//	SMTP_TEMPLATE_DIR=/etc/gobit/mail
//
// and NOTIFICATION_PROVIDER must name this provider ("smtp") for the
// notification module to route to it.
package notificationsmtp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// Name is the plugin's name in the registry; it is the value the PLUGINS list
// recognizes.
const Name = "notification-smtp"

// ProviderID is the provider's id.
//
// The notification module resolves the provider BY THIS NAME
// (NOTIFICATION_PROVIDER), so the value is a contract with the installation's
// configuration and must stay stable across versions.
const ProviderID = "smtp"

// The setting names. The values themselves are read from the environment and
// are never written anywhere.
const (
	settingHost      = "SMTP_HOST"
	settingPort      = "SMTP_PORT"
	settingUsername  = "SMTP_USERNAME"
	settingPassword  = "SMTP_PASSWORD"
	settingFrom      = "SMTP_FROM"
	settingTemplates = "SMTP_TEMPLATE_DIR"
	settingPlaintext = "SMTP_ALLOW_PLAINTEXT"
)

// defaultPort is the submission port (RFC 6409).
//
// 587 is the default rather than 25: port 25 is server-to-server relay, it is
// blocked outbound by most hosting providers, and it carries no expectation of
// authentication. An installation that gives no port is far likelier to want
// submission than relay.
const defaultPort = 587

// templateExt is the extension of the template files read from the directory.
const templateExt = ".tmpl"

// The names of the two sub-templates every template file has to define.
const (
	subjectTemplate = "subject"
	bodyTemplate    = "body"
)

// Error codes.
const (
	codeMissingSetting  = "smtp_setting_missing"
	codeInvalidSetting  = "smtp_setting_invalid"
	codeTemplateLoad    = "smtp_template_load_failed"
	codeTemplateUnknown = "smtp_template_unknown"
	codeTemplateRender  = "smtp_template_render_failed"
	codeChannel         = "smtp_channel_unsupported"
	codeHeaderInjection = "smtp_header_injection"
	codeSendFailed      = "smtp_send_failed"
	codeInsecure        = "smtp_connection_not_encrypted"
)

// dialTimeout bounds a send that arrives with no deadline on its context.
//
// The provider contract asks the CALLER to put a deadline on ctx, and this
// value does not replace that. It is the backstop for the case where the
// caller forgot: without it a mail server that accepts the connection and then
// says nothing would hold an event subscriber forever, and the queue behind
// that subscriber would stop moving with nothing in the logs to show why.
const dialTimeout = 30 * time.Second

// Plugin is the SMTP notification plugin.
type Plugin struct{}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the configuration, loads the templates and registers the
// provider.
//
// Every fault here stops startup. Skipping silently would produce an
// installation that believes it sends mail and does not — and the notification
// path is precisely where that belief goes unchallenged the longest, because
// nobody reports an email they never knew was coming.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	host, ok := h.Setting(settingHost)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting", Name, settingHost)
	}

	from, ok := h.Setting(settingFrom)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting", Name, settingFrom)
	}
	if err := checkHeaderValue("from address", from); err != nil {
		return err
	}

	dir, ok := h.Setting(settingTemplates)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting; gobit ships no email copy",
			Name, settingTemplates)
	}

	port, err := readPort(h)
	if err != nil {
		return err
	}

	templates, err := loadTemplates(dir)
	if err != nil {
		return err
	}

	username, _ := h.Setting(settingUsername)
	password, _ := h.Setting(settingPassword)

	plaintext, err := readPlaintext(h)
	if err != nil {
		return err
	}

	// Neither the password nor the sender address is logged. The template
	// NAMES are: they answer "did the installation's templates load" without
	// carrying anything the log collector should not hold (plan Section 8).
	h.Logger().Info("registering the smtp notification provider",
		"provider_id", ProviderID,
		"host", host,
		"port", port,
		"authenticated", username != "",
		"encrypted", !plaintext,
		"templates", templateNames(templates),
	)

	h.RegisterNotificationProvider(&sender{
		host:      host,
		port:      port,
		username:  username,
		password:  password,
		from:      from,
		templates: templates,
		plaintext: plaintext,
	})

	return nil
}

// readPort reads the port setting and applies [defaultPort] in its absence.
func readPort(h *coreplugin.Host) (int, error) {
	raw, ok := h.Setting(settingPort)
	if !ok {
		return defaultPort, nil
	}

	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return 0, coreerrors.Invalid(codeInvalidSetting,
			"%s has to be a port number between 1 and 65535: %q", settingPort, raw)
	}

	return port, nil
}

// readPlaintext reads the opt-out that permits an unencrypted connection.
//
// The setting is deliberately long and unpleasant to type, and it accepts only
// the exact word "true". A boolean parser that also took "1", "yes" or "on"
// would make it easier to end up here by accident, and the accident costs the
// SMTP password in clear text on the wire.
func readPlaintext(h *coreplugin.Host) (bool, error) {
	raw, ok := h.Setting(settingPlaintext)
	if !ok {
		return false, nil
	}
	if raw != "true" {
		return false, coreerrors.Invalid(codeInvalidSetting,
			"%s only accepts the exact value %q; %q was given", settingPlaintext, "true", raw)
	}

	return true, nil
}

// loadTemplates reads every template file in the directory and parses it.
//
// A file has to define both sub-templates ("subject" and "body"). Checking that
// here rather than at send time is the point of the whole function: a template
// missing its subject would otherwise mail a customer a message with an empty
// subject line, and nothing in the system would record that as a fault.
func loadTemplates(dir string) (map[string]*template.Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeTemplateLoad,
			"the template directory %q given in %s could not be read", dir, settingTemplates)
	}

	out := make(map[string]*template.Template)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != templateExt {
			continue
		}

		name := strings.TrimSuffix(e.Name(), templateExt)
		path := filepath.Join(dir, e.Name())

		tmpl, err := template.New(e.Name()).ParseFiles(path)
		if err != nil {
			return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeTemplateLoad,
				"the template %q could not be parsed", path)
		}
		for _, required := range []string{subjectTemplate, bodyTemplate} {
			if tmpl.Lookup(required) == nil {
				return nil, coreerrors.Invalid(codeTemplateLoad,
					"the template %q does not define the %q block; every template file has to define both %q and %q",
					path, required, subjectTemplate, bodyTemplate)
			}
		}

		out[name] = tmpl
	}

	if len(out) == 0 {
		return nil, coreerrors.Invalid(codeTemplateLoad,
			"no %s file was found in the template directory %q", templateExt, dir)
	}

	return out, nil
}

// templateNames returns the loaded template names in a stable order, for the
// startup log.
func templateNames(templates map[string]*template.Template) []string {
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	// The map's iteration order is random; an unstable log line makes two
	// startups impossible to diff.
	sortStrings(names)

	return names
}

// sortStrings sorts in place. It exists so the package does not import "sort"
// for a single call in a log path.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// sender is the [coreprovider.NotificationProvider] implementation over SMTP.
//
// It is safe for concurrent use: every field is written once at Setup and read
// afterwards, and each Send opens its own connection. Holding a pooled
// connection was rejected — an SMTP connection is stateful, a mail server
// closes an idle one without warning, and a pool would have to distinguish
// "the server hung up" from "the message was refused" on every send.
type sender struct {
	host     string
	port     int
	username string
	// password is the SMTP password. It is NEVER logged and never put in an
	// error message: an error message travels to the error reporter, and the
	// reporter's audience is wider than the operator's.
	password string
	from     string
	// templates are keyed by [coreprovider.Notification.Template].
	templates map[string]*template.Template
	// plaintext permits an unencrypted connection; see [readPlaintext].
	plaintext bool
}

// The provider satisfies the core contract; a signature drift is caught at
// compile time rather than at the first send.
var _ coreprovider.NotificationProvider = (*sender)(nil)

// ID returns the provider's id.
func (s *sender) ID() string { return ProviderID }

// Send renders the notification and delivers it over SMTP.
//
// The call BLOCKS. The contract asks the caller for a deadline on ctx and this
// method honors it: the deadline is applied to the connection, so a server
// that stops answering mid-conversation fails at the same moment as one that
// never answered.
func (s *sender) Send(ctx context.Context, n coreprovider.Notification) error {
	if n.Channel != coreprovider.ChannelEmail {
		// Sending nothing and returning an error is what the contract asks
		// for. The alternative — treating an SMS as an email — would put a
		// phone number in the To header and bounce, which reads in the
		// delivery log as a bad address rather than a misrouted channel.
		return coreerrors.Invalid(codeChannel,
			"the %s provider only serves the %q channel; %q was requested",
			ProviderID, coreprovider.ChannelEmail, n.Channel)
	}

	if err := checkHeaderValue("recipient", n.To); err != nil {
		return err
	}

	tmpl, ok := s.templates[n.Template]
	if !ok {
		// Falling back to some generic body was rejected: a customer would
		// receive a message that says nothing, and the delivery log would
		// record a success. An error puts the missing template in front of
		// whoever can add it.
		return coreerrors.Invalid(codeTemplateUnknown,
			"the %s provider has no template named %q; %s holds: %v",
			ProviderID, n.Template, settingTemplates, templateNames(s.templates))
	}

	subject, err := render(tmpl, subjectTemplate, n)
	if err != nil {
		return err
	}
	body, err := render(tmpl, bodyTemplate, n)
	if err != nil {
		return err
	}

	// The subject is rendered from template data, and that data comes from an
	// event payload — a customer name, a product title, values this process
	// did not author. A line break in any of them would end the Subject header
	// and let whatever follows be read as further headers: a Bcc, a second
	// From. Rejecting is the whole defense, and it happens after rendering
	// because that is where the untrusted value lands.
	if err := checkHeaderValue("subject", subject); err != nil {
		return err
	}

	msg := buildMessage(s.from, n.To, subject, body)

	return s.deliver(ctx, n.To, msg)
}

// render executes one sub-template with the notification's data.
func render(tmpl *template.Template, name string, n coreprovider.Notification) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, n.Data); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeTemplateRender,
			"the %q block of the %q template could not be rendered", name, n.Template)
	}

	return strings.TrimSpace(buf.String()), nil
}

// buildMessage assembles the RFC 5322 message.
//
// # The subject is encoded, the body is base64
//
// Both follow from the same fact: this is a framework for a repository whose
// own language is not ASCII, and a subject line carrying accented letters sent
// as raw bytes arrives as mojibake in most clients. The subject therefore goes
// through RFC 2047 encoded-word form and the body is base64 with an explicit
// charset.
//
// Base64 rather than quoted-printable, for a reason that is not aesthetic: RFC
// 5321 caps a line at 1000 octets including CRLF, and a template that renders
// a long URL or an un-wrapped paragraph exceeds it. A server that enforces the
// cap answers with an error that names the line length and nothing about the
// template — base64's fixed 76-octet lines make the class of fault impossible
// instead of diagnosable.
func buildMessage(from, to, subject, body string) []byte {
	var b strings.Builder

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("\r\n")

	encoded := base64.StdEncoding.EncodeToString([]byte(body))
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")

	return []byte(b.String())
}

// deliver opens the connection and hands the message over.
//
// net/smtp's SendMail is not used, and the reason is the context: SendMail
// dials on its own and takes no ctx, so a caller's deadline would be
// decoration. Running the conversation by hand also makes the encryption
// decision explicit rather than inherited from whether the server happened to
// advertise STARTTLS.
func (s *sender) deliver(ctx context.Context, to string, msg []byte) error {
	addr := net.JoinHostPort(s.host, strconv.Itoa(s.port))

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(dialTimeout)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the smtp server %q could not be reached", addr)
	}
	defer conn.Close() //nolint:errcheck // the send result is already reported; a close error adds nothing

	// One deadline covers the whole conversation, not just the dial. A server
	// that accepts the connection and then falls silent is the case this
	// catches, and it is the case a dial timeout alone misses.
	if err := conn.SetDeadline(deadline); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeSendFailed,
			"the deadline could not be set on the smtp connection")
	}

	client, err := smtp.NewClient(conn, s.host)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the smtp greeting from %q could not be read", addr)
	}
	defer client.Close() //nolint:errcheck // same as the connection above

	if err := s.secure(client); err != nil {
		return err
	}
	if err := s.authenticate(client); err != nil {
		return err
	}

	if err := client.Mail(strings.TrimSpace(s.from)); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the smtp server refused the sender")
	}
	if err := client.Rcpt(to); err != nil {
		// The address is NOT put in the message. A refused recipient is the
		// most common failure there is, so this error is also the likeliest to
		// reach the error reporter — and the reporter must not become the
		// place where customer addresses accumulate (plan Section 8).
		return coreerrors.Wrap(err, coreerrors.KindInvalid, codeSendFailed,
			"the smtp server refused the recipient")
	}

	w, err := client.Data()
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the smtp server did not accept the message body")
	}
	if _, err := w.Write(msg); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the message could not be written to the smtp server")
	}
	if err := w.Close(); err != nil {
		// The close is where the server ACCEPTS the message; an error here
		// means it was not queued, however well the write went.
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the smtp server did not accept the message")
	}

	// A Quit failure is not reported: the message is already accepted, and
	// turning a dirty disconnect into a send error would make the caller
	// retry a mail that went out.
	_ = client.Quit()

	return nil
}

// secure raises the connection to TLS.
//
// An unencrypted connection is refused unless the installation opted out in
// writing. net/smtp already refuses to send the PASSWORD over a cleartext
// link, which is why relying on that alone is not enough: a provider needing
// no authentication would then mail every order confirmation — name, address,
// what was bought — across the network in the clear, and nothing would report
// a fault.
func (s *sender) secure(client *smtp.Client) error {
	ok, _ := client.Extension("STARTTLS")
	if !ok {
		if s.plaintext {
			return nil
		}

		return coreerrors.Unavailable(codeInsecure,
			"the smtp server %q does not offer STARTTLS; set %s=true to accept an unencrypted connection",
			s.host, settingPlaintext)
	}

	// ServerName is set from the configured host, never from anything the
	// server said: a certificate is only evidence when it is checked against
	// the name the client meant to reach.
	if err := client.StartTLS(&tls.Config{ServerName: s.host, MinVersion: tls.VersionTLS12}); err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"the TLS handshake with the smtp server %q failed", s.host)
	}

	return nil
}

// authenticate runs PLAIN authentication when a username is configured.
//
// No username means no authentication, which is an ordinary setup for a relay
// on a private network. A username with no password is NOT ordinary — it is a
// half-filled configuration, and it is refused rather than attempted with an
// empty secret.
func (s *sender) authenticate(client *smtp.Client) error {
	if s.username == "" {
		return nil
	}
	if s.password == "" {
		return coreerrors.Invalid(codeMissingSetting,
			"%s is set but %s is not; smtp authentication cannot be attempted with an empty password",
			settingUsername, settingPassword)
	}

	auth := smtp.PlainAuth("", s.username, s.password, s.host)
	if err := client.Auth(auth); err != nil {
		// The username is not put in the message either: an error message is
		// the wrong place to publish half a credential pair.
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeSendFailed,
			"smtp authentication against %q failed", s.host)
	}

	return nil
}

// checkHeaderValue refuses a value that would break out of its header.
//
// CR and LF are the whole attack: a recipient or a subject carrying one ends
// the header it sits in and lets everything after it be read as further
// headers — a Bcc that copies the message elsewhere, a second From that
// forges the sender. A NUL is refused with them because it truncates the
// header at a different place in different servers, so what was checked and
// what is delivered stop being the same string.
//
// The value is REJECTED rather than stripped. Stripping would deliver a
// message the caller did not write, quietly, and the caller would have no way
// to learn that the subject a customer saw was not the subject it produced.
func checkHeaderValue(field, value string) error {
	if strings.ContainsAny(value, "\r\n\x00") {
		// The value itself is not echoed: it is attacker-controlled, and an
		// error message is copied into logs and error reports where a
		// deliberately long or confusing value does its second job.
		return coreerrors.Invalid(codeHeaderInjection,
			"the %s carries a line break or a NUL byte and was refused", field)
	}
	if strings.TrimSpace(value) == "" {
		return coreerrors.Invalid(codeHeaderInjection,
			"the %s cannot be empty", field)
	}

	return nil
}
