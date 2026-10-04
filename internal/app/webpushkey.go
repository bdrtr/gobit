package app

import (
	"fmt"
	"io"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/plugins/webpush"
)

// webpushKeyCommand prints a fresh signing key pair for the web-push plugin
// (ADR 0383).
//
// # Why the binary prints it
//
// The plugin refuses to start without WEBPUSH_VAPID_PRIVATE_KEY, and the value
// it wants, the raw 32-byte P-256 scalar in base64url, is not what openssl
// prints. The binary is the one tool every installation already has, and
// [webpush.GenerateKey] is the code whose output the plugin's own parse
// accepts. Before this command the plugin's error sent the operator to a
// command that did not exist (D233).
//
// # Why it opens nothing
//
// It reads no configuration and reaches no database, as [newCommand] does not:
// the key is needed BEFORE the plugin is configured, and a command that first
// demanded a reachable installation would refuse the operator at the moment
// they are setting one up.
//
// The name is spelled here as a literal, like every verb in [Main]'s switch,
// because TestUsageNamesEveryVerbTheDispatchAccepts resolves the switch's
// cases from this package's source alone. The plugin's error names the same
// command through [webpush.KeyCommand], and TestThePluginErrorNamesTheCommand
// fails when the two spell it differently.
const webpushKeyCommand = "webpush-key"

// runWebpushKey generates a pair and prints it.
//
// The private half is printed as the setting line, ready to paste into .env,
// and every other line is a comment, so the whole output can be appended to
// one. The public half is printed so the operator can check the running plugin
// against it: the plugin logs it at startup as public_key, and GET
// /store/v1/webpush/vapid-key serves it. The storefront takes it from that
// route and never has it configured, so a replaced key cannot leave a
// storefront subscribing under the old one.
func runWebpushKey(args []string, out io.Writer) error {
	if len(args) > 0 {
		return errors.Invalid(codeUsage,
			"%s takes no arguments; it prints a new key pair and changes nothing", webpushKeyCommand)
	}

	privateKey, publicKey, err := webpush.GenerateKey()
	if err != nil {
		return err
	}

	return writeReport(out, webpushKeyText(privateKey, publicKey))
}

// webpushKeyText renders a generated pair.
func webpushKeyText(privateKey, publicKey string) string {
	return fmt.Sprintf(`# Keep this key as you keep the database: losing or replacing it ends every
# push subscription ever issued, and each browser has to subscribe again.
%s=%s
# The public key, to check the running plugin against: it logs it at startup
# as public_key, and GET /store/v1/webpush/vapid-key serves it. A storefront
# reads its applicationServerKey from that route; do not configure it there.
# %s
`, webpushPrivateKeySetting, privateKey, publicKey)
}

// webpushPrivateKeySetting is the setting the printed line fills.
//
// It is spelled here because the plugin keeps its setting names unexported;
// TestTheGeneratedKeyStartsThePlugin hands the plugin a host keyed by this name,
// so a rename on either side fails there.
const webpushPrivateKeySetting = "WEBPUSH_VAPID_PRIVATE_KEY"
