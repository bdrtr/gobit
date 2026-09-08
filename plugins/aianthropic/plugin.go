// Package aianthropic answers gobit's closed questions about text with
// Anthropic's Messages API.
//
// # It is a plugin because the model is not gobit's decision
//
// gobit is a framework and cannot know which model an installation trusts, what
// it costs them, or whether their data may leave the building at all. So the
// contract lives in the core ([provider.Classifier]) and the client lives here,
// the same split the notification and error-reporting providers already use.
// An installation that does not name this plugin runs a tree in which no model
// is called and no column is filled.
//
// # What LEAVES the installation, said plainly
//
// The text of a customer's review. Not their name — the caller does not send it
// (see internal/jobs/reviewsuggest) — but a review is free text a member of the
// public wrote, and free text can carry anything they chose to put in it.
//
// That makes whoever runs this installation responsible for a SUB-PROCESSOR
// they did not have before, and gobit cannot make that decision for them: it is
// the embedder who is the data controller (ADR 0029). What gobit owes them is
// that the decision is an explicit act — naming the plugin — rather than a
// default, and that this paragraph is where they find out.
//
// # Why the API is spoken by hand
//
// The same reason errorsentry and errorotlp write their own bodies: the request
// is a small JSON object with a documented shape, and writing it costs less
// than another dependency in go.mod. Nothing here needs the parts of an SDK
// that are worth a dependency — no streaming, no batching, no retries.
//
// # Use
//
//	PLUGINS=ai-anthropic
//	ANTHROPIC_API_KEY=sk-ant-...
//	ANTHROPIC_MODEL=claude-haiku-4-5-20251001
//
// and optionally ANTHROPIC_BASE_URL for a gateway. Without the key the plugin
// refuses to start rather than running as a no-op, for errorotlp's reason: an
// installation that believes it has a model and does not is worse off than one
// that knows it has none.
package aianthropic

import (
	"context"
	"net/http"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
)

// Name is how an operator asks for this plugin in PLUGINS.
const Name = "ai-anthropic"

// ProviderID identifies the provider in the startup log and in the job's
// report. It is NOT what goes in the review's suggestion_model column — that
// carries the model the API says answered, which is a different thing.
const ProviderID = "anthropic"

// The settings this plugin reads.
const (
	keySetting     = "ANTHROPIC_API_KEY"
	modelSetting   = "ANTHROPIC_MODEL"
	baseURLSetting = "ANTHROPIC_BASE_URL"
)

// defaultBaseURL is the public API.
const defaultBaseURL = "https://api.anthropic.com"

// Error codes.
const (
	// CodeKeyMissing reports a plugin asked for with no API key.
	CodeKeyMissing = "ai_anthropic_key_missing"
	// CodeModelMissing reports a plugin asked for with no model named.
	CodeModelMissing = "ai_anthropic_model_missing"
)

// timeout bounds one call.
//
// The caller is expected to set its own deadline as well and does; this one is
// the floor under a caller that forgets, so a hung connection cannot hold a
// scheduled run open for as long as the run is allowed to last.
const timeout = 30 * time.Second

// Plugin is the plugin.
type Plugin struct {
	// classifier is kept so the tests can reach it; the core holds its own
	// reference through the container.
	classifier *Classifier
}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup reads the settings and registers the classifier.
//
// The model is REQUIRED and has no default, which is a deliberate difference
// from the plugins that default their endpoint. A model name is a price and a
// capability, both of which change between versions; defaulting it would mean
// an installation silently moving to a different model — and a different bill —
// on an upgrade of gobit rather than on a decision of theirs.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	key, _ := h.Setting(keySetting)
	if strings.TrimSpace(key) == "" {
		return coreerrors.Invalid(CodeKeyMissing,
			"the %s plugin needs %s; without it no question can be asked, and a plugin "+
				"that starts anyway would leave an installation believing it has a model",
			Name, keySetting)
	}

	model, _ := h.Setting(modelSetting)
	if strings.TrimSpace(model) == "" {
		return coreerrors.Invalid(CodeModelMissing,
			"the %s plugin needs %s; a model name is a price and a capability, so gobit "+
				"does not choose one on an installation's behalf",
			Name, modelSetting)
	}

	base, _ := h.Setting(baseURLSetting)
	if strings.TrimSpace(base) == "" {
		base = defaultBaseURL
	}

	p.classifier = &Classifier{
		key:    strings.TrimSpace(key),
		model:  strings.TrimSpace(model),
		base:   strings.TrimRight(strings.TrimSpace(base), "/"),
		client: &http.Client{Timeout: timeout},
	}

	h.RegisterAIProvider(p.classifier)

	return nil
}
