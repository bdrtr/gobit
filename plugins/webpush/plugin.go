// Package webpush delivers browser push notifications, and it brings its own
// module to do it.
//
// # Why this is not a notification provider
//
// gobit has a notification provider slot and this plugin deliberately does not
// fill it. The contract's destination is `To string`, documented as "an email
// address or a phone number"; a push destination is not an address. It is three
// values the BROWSER mints — an endpoint URL, a P-256 public key and a 16-byte
// auth secret — and the framework has to have STORED them before a send is
// possible.
//
// Routing push through the notification module would also make its delivery
// ledger lie. A fan-out to many devices has no single truth value: Send returns
// one error, so a send to zero devices returns nil and the ledger records
// "sent" — and because a repeat claim is skipped regardless of the prior
// status, that false "sent" permanently disables the resend the module's own
// documentation says must stay a human's decision.
//
// The whole reasoning, and the five designs that were rejected, are in ADR
// 0018.
//
// # What it registers
//
// A module and one event subscription. Nothing else — no provider, no
// container name, no link definition. It is the plugins/searchpg shape: the
// plugin owns a table, a migration and its endpoints. Outside its own directory
// it is named in two places: one line in the composition root's catalog, and
// the binary's `webpush-key` command (internal/app/webpushkey.go, ADR 0383),
// which prints a key for it. Removing it is `rm -rf plugins/webpush` plus that
// line and that command: its file, its test, its dispatch case and its usage
// line.
//
// # The VAPID key is durable state on the order of the database
//
// Losing or rotating WEBPUSH_VAPID_PRIVATE_KEY invalidates every subscription
// ever issued, and every user has to re-subscribe from their own browser.
// gobit has no other secret with that property — a JWT secret rotation drops
// sessions a customer can re-create by logging in, while this cannot be
// repaired from the server side at all. It lives in no migration and in no
// backup story unless the operator puts it there.
//
// # Usage
//
//	PLUGINS=web-push
//	# `gobit webpush-key` prints this line filled in.
//	WEBPUSH_VAPID_PRIVATE_KEY=<base64url of the raw 32-byte P-256 scalar>
//	WEBPUSH_VAPID_SUBJECT=mailto:ops@example.test
//	WEBPUSH_TEMPLATE_DIR=/etc/gobit/push
package webpush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is the plugin's name in the registry; the PLUGINS list recognizes it.
const Name = "web-push"

// ModuleName is the module's name, and it differs from the plugin's on
// purpose.
//
// A module name becomes the prefix of its migration ledger table, which the
// core validates against a strict identifier pattern — a hyphen is refused.
// searchpg carries the same split for the same reason (`search-pg` /
// `searchpg`).
const ModuleName = "webpush"

// The setting names. The values are read from the environment and the private
// key is never written anywhere.
const (
	settingPrivateKey = "WEBPUSH_VAPID_PRIVATE_KEY"
	settingSubject    = "WEBPUSH_VAPID_SUBJECT"
	settingTemplates  = "WEBPUSH_TEMPLATE_DIR"
)

// KeyCommand is the binary's command that prints a fresh signing key pair:
// `gobit webpush-key` (ADR 0383).
//
// It is declared here, beside the setting it fills, because every error a
// missing or malformed key produces names it ([keyHint]) and a plugin cannot
// import the composition root that dispatches it. The root's own test runs
// those errors and fails when one names a command the root does not answer.
const KeyCommand = "webpush-key"

// keyHint ends every error about a missing or malformed private key.
//
// The operator who reaches any of them, with no key, a hex dump, or the public
// half pasted in place of the private one, needs the same next step. It names
// the program `gobit` because a plugin does not know what an embedding program
// calls itself.
func keyHint() string {
	return "generate one with `gobit " + KeyCommand +
		"` (a program that embeds gobit answers the same command under its own name)"
}

// Error codes.
const (
	codeMissingSetting = "webpush_setting_missing"
	codeInvalidSetting = "webpush_setting_invalid"
	codeSetupFailed    = "webpush_module_setup_failed"
)

// orderPlacedEvent is the event the plugin subscribes to.
//
// It is the ONLY order event the repository publishes today; shipped, delivered
// and canceled pushes wait on the order module publishing them, which is that
// module's change and not this plugin's.
const orderPlacedEvent = "order.placed"

// Plugin is the web push plugin.
type Plugin struct {
	mod *webpushModule
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup parses the signing key, loads the templates and adds the module.
//
// Every fault stops startup, and the reason is sharper here than for most
// settings: a push plugin that installs without a usable key produces an
// installation where subscribe succeeds, the browser stores a subscription
// against a key nobody holds, and every send afterwards answers 401 — a state
// that cannot be repaired without the user visiting the site again.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	rawKey, ok := h.Setting(settingPrivateKey)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting; %s",
			Name, settingPrivateKey, keyHint())
	}

	subject, ok := h.Setting(settingSubject)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting; push services may refuse "+
				"a token with no contact address", Name, settingSubject)
	}
	if !strings.HasPrefix(subject, "mailto:") && !strings.HasPrefix(subject, "https://") {
		return coreerrors.Invalid(codeInvalidSetting,
			"%s has to be a mailto: or https: URI; %q was given", settingSubject, subject)
	}

	dir, ok := h.Setting(settingTemplates)
	if !ok {
		return coreerrors.Invalid(codeMissingSetting,
			"the %s plugin cannot be set up without the %s setting; gobit ships no push copy",
			Name, settingTemplates)
	}

	key, err := parseVAPIDKey(rawKey)
	if err != nil {
		return err
	}

	publicKey, err := publicKeyOf(key)
	if err != nil {
		return err
	}

	templates, err := loadTemplates(dir)
	if err != nil {
		return err
	}

	log := h.Logger()

	// Neither the private key nor its base64 form is logged. The PUBLIC key is,
	// and deliberately: it is what a storefront passes as applicationServerKey,
	// so an operator debugging "nobody receives anything" can compare the
	// startup line against what the browser was given without reading a
	// database.
	log.Info("registering the web push module",
		"module", ModuleName,
		"public_key", publicKey,
		"fingerprint", fingerprintOf(publicKey),
		"subject", subject,
		"templates", templateNames(templates),
	)

	p.mod = newModule(moduleOptions{
		key:         key,
		publicKey:   publicKey,
		fingerprint: fingerprintOf(publicKey),
		subject:     subject,
		templates:   templates,
		log:         log.With("module", ModuleName),
	})

	h.AddModule(p.mod)
	h.Subscribe(orderPlacedEvent, p.mod.onOrderPlaced)

	return nil
}

// parseVAPIDKey reads the raw 32-byte P-256 scalar the operator configured.
//
// # Why a raw scalar and not PEM
//
// The value has to be pasted into an environment variable, and a PEM block is
// multi-line. A single base64url line survives a .env file, a Kubernetes
// secret and a copy-paste; a PEM block survives none of them reliably.
//
// The key is validated HERE rather than at the first send. A malformed key
// discovered at send time is discovered after subscriptions already exist
// against it.
func parseVAPIDKey(raw string) (*ecdsa.PrivateKey, error) {
	decoded, err := decodeKey(raw)
	if err != nil {
		return nil, coreerrors.Invalid(codeInvalidSetting,
			"%s is not base64url; %s", settingPrivateKey, keyHint())
	}
	if len(decoded) != 32 {
		return nil, coreerrors.Invalid(codeInvalidSetting,
			"%s has to be the raw 32-byte P-256 scalar; %d bytes were given; %s",
			settingPrivateKey, len(decoded), keyHint())
	}

	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), decoded)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidSetting,
			"%s is not a valid P-256 private key; %s", settingPrivateKey, keyHint())
	}

	return key, nil
}

// GenerateKey mints a signing key pair and returns both halves base64url
// encoded.
//
// Its caller outside this package's tests is the binary's [KeyCommand], which
// prints both halves, so an operator produces a key without reaching for
// openssl and a PEM conversion. The public half is returned alongside so the
// operator can check the running plugin against it: the plugin logs it at
// startup and GET /store/v1/webpush/vapid-key serves it, which is where a
// storefront takes it from.
func GenerateKey() (privateKey, publicKey string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSetupFailed,
			"a VAPID key pair could not be generated")
	}

	raw, err := key.Bytes()
	if err != nil {
		return "", "", coreerrors.Wrap(err, coreerrors.KindInternal, codeSetupFailed,
			"the generated private key could not be encoded")
	}
	pub, err := publicKeyOf(key)
	if err != nil {
		return "", "", err
	}

	return base64.RawURLEncoding.EncodeToString(raw), pub, nil
}

// slogDiscard is used when a caller gives no logger.
func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }
