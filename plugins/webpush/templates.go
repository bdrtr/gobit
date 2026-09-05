package webpush

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// templateExt is the extension of the files read from the template directory.
const templateExt = ".tmpl"

// The sub-templates every file has to define.
//
// A push notification is three things and only three: what the lock screen
// shows, what the body says, and where a tap goes. Anything else the service
// worker wants can be added by the installation's own worker; the framework
// does not model it.
const (
	blockTitle = "title"
	blockBody  = "body"
	blockURL   = "url"
)

// defaultLocale is the template used when a device recorded no locale.
const defaultLocale = "default"

// Error codes.
const (
	codeTemplateLoad    = "webpush_template_load_failed"
	codeTemplateUnknown = "webpush_template_unknown"
	codeTemplateRender  = "webpush_template_render_failed"
)

// templateKey identifies one rendering: an event and a locale.
type templateKey struct {
	event  string
	locale string
}

// templateSet is the parsed copy the module holds.
type templateSet map[templateKey]*template.Template

// payload is what a service worker receives after decryption.
type payload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
}

// loadTemplates reads and parses every template file in the directory.
//
// The file name carries both the event and the locale:
//
//	order.placed.tmpl        the default for that event
//	order.placed.tr.tmpl     the Turkish rendering
//
// # Why locale is a file and not a field
//
// One customer's phone is set to one language and their work machine to
// another, and the same order has to reach both correctly. Locale therefore
// belongs to the DEVICE, and the device chooses which file renders — which
// makes adding a language a matter of adding a file rather than editing one.
//
// The parsing happens at startup, following the smtp plugin's rule: a broken
// template is a configuration error, and a configuration error belongs at
// startup rather than at 03:00 inside an event subscriber where it surfaces as
// a notification that silently never arrived.
func loadTemplates(dir string) (templateSet, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeTemplateLoad,
			"the template directory %q given in %s could not be read", dir, settingTemplates)
	}

	out := templateSet{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != templateExt {
			continue
		}

		key := keyOf(strings.TrimSuffix(e.Name(), templateExt))
		path := filepath.Join(dir, e.Name())

		tmpl, err := template.New(e.Name()).ParseFiles(path)
		if err != nil {
			return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeTemplateLoad,
				"the template %q could not be parsed", path)
		}
		for _, required := range []string{blockTitle, blockBody} {
			if tmpl.Lookup(required) == nil {
				return nil, coreerrors.Invalid(codeTemplateLoad,
					"the template %q does not define the %q block; every file has to define %q and %q",
					path, required, blockTitle, blockBody)
			}
		}

		out[key] = tmpl
	}

	if len(out) == 0 {
		return nil, coreerrors.Invalid(codeTemplateLoad,
			"no %s file was found in the template directory %q", templateExt, dir)
	}

	return out, nil
}

// keyOf splits a file's base name into an event and a locale.
//
// The last dot-separated segment is a locale when it looks like one — two
// letters, or two letters and a region. Anything else is part of the event
// name, which is what keeps "order.placed" from being read as the event
// "order" in the locale "placed".
func keyOf(base string) templateKey {
	idx := strings.LastIndex(base, ".")
	if idx > 0 && looksLikeLocale(base[idx+1:]) {
		return templateKey{event: base[:idx], locale: base[idx+1:]}
	}

	return templateKey{event: base, locale: defaultLocale}
}

// looksLikeLocale reports whether a segment is a language tag.
func looksLikeLocale(s string) bool {
	if len(s) != 2 && len(s) != 5 {
		return false
	}
	if len(s) == 5 && s[2] != '-' {
		return false
	}
	for i, r := range s {
		if i == 2 {
			continue
		}
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') {
			return false
		}
	}

	return true
}

// render produces the encrypted payload for one device.
//
// The locale falls back to the default rather than failing: a device that
// recorded an unusual language must still receive its order confirmation, and
// a message in the wrong language beats no message at all. A missing EVENT is
// a different matter and is an error — it means nobody wrote the copy.
func (t templateSet) render(event, locale string, data map[string]string) ([]byte, error) {
	tmpl, ok := t[templateKey{event: event, locale: locale}]
	if !ok {
		tmpl, ok = t[templateKey{event: event, locale: defaultLocale}]
	}
	if !ok {
		return nil, coreerrors.Invalid(codeTemplateUnknown,
			"there is no push template for %q; %s holds: %v", event, settingTemplates, templateNames(t))
	}

	title, err := renderBlock(tmpl, blockTitle, event, data)
	if err != nil {
		return nil, err
	}
	body, err := renderBlock(tmpl, blockBody, event, data)
	if err != nil {
		return nil, err
	}

	var target string
	if tmpl.Lookup(blockURL) != nil {
		if target, err = renderBlock(tmpl, blockURL, event, data); err != nil {
			return nil, err
		}
	}

	encoded, err := json.Marshal(payload{Title: title, Body: body, URL: target})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeTemplateRender,
			"the push payload could not be encoded")
	}

	// The size is checked HERE, where the template that produced it can be
	// named, rather than after a device list has been walked and a per-device
	// error can only say "too large" about a message nobody can point at.
	if len(encoded) > MaxPayloadBytes {
		return nil, coreerrors.Invalid(codePayloadTooLarge,
			"the %q push payload renders to %d bytes; at most %d fit a push message",
			event, len(encoded), MaxPayloadBytes)
	}

	return encoded, nil
}

// renderBlock executes one sub-template.
func renderBlock(tmpl *template.Template, block, event string, data map[string]string) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, block, data); err != nil {
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeTemplateRender,
			"the %q block of the %q push template could not be rendered", block, event)
	}

	return strings.TrimSpace(buf.String()), nil
}

// templateNames lists what is loaded, in a stable order, for logs and errors.
func templateNames(t templateSet) []string {
	names := make([]string, 0, len(t))
	for key := range t {
		if key.locale == defaultLocale {
			names = append(names, key.event)

			continue
		}
		names = append(names, key.event+" ("+key.locale+")")
	}
	slices.Sort(names)

	return names
}
