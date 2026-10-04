package app

import (
	"bytes"
	"encoding/base64"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/plugins/webpush"
)

// The key command exists because the plugin's error named a command that did
// not exist (D233). What these tests hold is the whole round trip an operator
// makes: the error names a command, the binary answers it, and what it prints
// starts the plugin.

// runKeyCommand runs the command through the dispatch and returns what it
// printed.
func runKeyCommand(t *testing.T) string {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, Main([]string{webpushKeyCommand}, &out, Options{}))

	return out.String()
}

// generatedPair runs the command and reads back the two halves it printed.
func generatedPair(t *testing.T) (privateKey, publicKey string) {
	t.Helper()

	out := runKeyCommand(t)
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, webpushPrivateKeySetting+"="); ok {
			privateKey = value
		}
		// The public half is the one comment line that is a single word.
		if value, ok := strings.CutPrefix(line, "# "); ok && !strings.Contains(value, " ") {
			publicKey = value
		}
	}
	require.NotEmpty(t, privateKey, "the command printed no setting line:\n%s", out)
	require.NotEmpty(t, publicKey, "the command printed no public key:\n%s", out)

	return privateKey, publicKey
}

// setUpPlugin runs the plugin's Setup with the given private key and returns
// its error and what it logged.
func setUpPlugin(t *testing.T, privateKey string) (string, error) {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "order.placed.tmpl"),
		[]byte(`{{define "title"}}Order{{end}}{{define "body"}}placed{{end}}`), 0o600))

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelInfo}))
	host := coreplugin.NewHost(nil, nil, nil, log, map[string]string{
		webpushPrivateKeySetting: privateKey,
		"WEBPUSH_VAPID_SUBJECT":  "mailto:ops@example.test",
		"WEBPUSH_TEMPLATE_DIR":   dir,
	})

	err := webpush.New().Setup(t.Context(), host)

	return logged.String(), err
}

// TestTheGeneratedKeyStartsThePlugin is the command's whole purpose: the line it
// prints, pasted as the setting, is a key the plugin accepts, and the public
// half it prints is the one the plugin derives and announces at startup — the
// value GET /store/v1/webpush/vapid-key serves.
func TestTheGeneratedKeyStartsThePlugin(t *testing.T) {
	t.Parallel()

	privateKey, publicKey := generatedPair(t)

	logged, err := setUpPlugin(t, privateKey)
	require.NoError(t, err, "the plugin refused the key the command printed")
	announced := regexp.MustCompile(`public_key=(\S+)`).FindStringSubmatch(logged)
	require.NotNil(t, announced, "the plugin logged no public key:\n%s", logged)
	assert.Equal(t, announced[1], publicKey,
		"the public key the command printed is not the one the plugin derives from the private key")
}

// TestTheKeyOutputAppendsToAnEnvFile holds the output's shape: exactly one line
// is a setting, and every other line is a comment.
//
// That is what lets `gobit webpush-key >> .env` work as printed. A prose line
// without its `#` is a line a dotenv parser refuses or reads as a variable.
func TestTheKeyOutputAppendsToAnEnvFile(t *testing.T) {
	t.Parallel()

	out := runKeyCommand(t)
	setting := regexp.MustCompile(`^` + regexp.QuoteMeta(webpushPrivateKeySetting) + `=[A-Za-z0-9_-]+$`)

	settings := 0
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
		case setting.MatchString(line):
			settings++
		default:
			assert.True(t, strings.HasPrefix(line, "#"),
				"a line that is neither the setting nor a comment: %q", line)
		}
	}
	assert.Equal(t, 1, settings, "the output has to carry exactly one setting line:\n%s", out)
}

// TestTwoRunsPrintTwoKeys holds that the command generates rather than recites.
//
// A fixed pair printed by every installation would be a signing key the whole
// world holds.
func TestTwoRunsPrintTwoKeys(t *testing.T) {
	t.Parallel()

	firstPrivate, firstPublic := generatedPair(t)
	secondPrivate, secondPublic := generatedPair(t)

	assert.NotEqual(t, firstPrivate, secondPrivate)
	assert.NotEqual(t, firstPublic, secondPublic)
}

// TestTheKeyCommandRefusesAnArgument keeps a typo from passing as a request.
//
// The command takes nothing, so `-force` or a stray word is an operator who
// meant something else, and printing a key anyway would hide that.
func TestTheKeyCommandRefusesAnArgument(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"-force"}, {"extra"}} {
		var out bytes.Buffer
		err := Main(append([]string{webpushKeyCommand}, args...), &out, Options{})

		require.Error(t, err, "%v was accepted", args)
		assert.True(t, errors.IsInvalid(err), "%v: %v", args, err)
		assert.Equal(t, codeUsage, errors.CodeOf(err))
		assert.Empty(t, out.String(), "%v: a refused run printed a key anyway", args)
	}
}

// TestTheKeyCommandIsInTheUsageText keeps the command findable from the help.
func TestTheKeyCommandIsInTheUsageText(t *testing.T) {
	t.Parallel()

	assert.Contains(t, usageText(binaryName, "test"), binaryName+" "+webpushKeyCommand)
}

// TestThePluginErrorNamesTheCommand closes D233 where it opened: every error a
// missing or malformed key produces names a command, and the binary answers
// that command.
//
// The command is read OUT of the error and then run, so the test fails when an
// error stops naming one, names one spelled differently from the dispatch, or
// names one the dispatch does not have. The cases are the ways an operator
// reaches the plugin without a usable key: none set, a value that is not
// base64url, a hex dump (64 characters that ARE base64url, decoding to 48
// bytes), the public half pasted in place of the private one (65 bytes), and
// 32 bytes that are no P-256 scalar.
func TestThePluginErrorNamesTheCommand(t *testing.T) {
	t.Parallel()

	_, publicKey := generatedPair(t)
	cases := []struct {
		name, key, code string
	}{
		{"missing", "", "webpush_setting_missing"},
		{"not base64url", "not base64url!", "webpush_setting_invalid"},
		{"hex", strings.Repeat("0f", 32), "webpush_setting_invalid"},
		{"public half", publicKey, "webpush_setting_invalid"},
		{"not a scalar", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "webpush_setting_invalid"},
	}
	command := regexp.MustCompile("`" + regexp.QuoteMeta(binaryName) + " ([a-z-]+)`")

	for _, tc := range cases {
		_, err := setUpPlugin(t, tc.key)
		require.Error(t, err, tc.name)
		assert.Equal(t, tc.code, errors.CodeOf(err), "%s: the error's code changed", tc.name)

		named := command.FindStringSubmatch(err.Error())
		if !assert.NotNil(t, named, "%s: the error names no command to run: %v", tc.name, err) {
			continue
		}
		assert.Equal(t, webpushKeyCommand, named[1], tc.name)

		var out bytes.Buffer
		assert.NoError(t, Main([]string{named[1]}, &out, Options{}),
			"%s: the command the error names is not one the binary answers", tc.name)
	}
}
