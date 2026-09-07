package arch_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file guards a contract that is held by two string literals in two trees
// with nothing between them.
//
// # What the contract is
//
// A module opens a read surface to the Query layer (ADR 0004) and NAMES the
// fields it serves: inventory declares FieldAvailableQuantity = "available_quantity"
// beside the provider that fills it. Those names are a published vocabulary — the
// thing ADR 0004 exists to let modules share without importing one another.
//
// The admin panel is an API CLIENT (ADR 0030): it imports nothing from
// internal/modules and reads the admin API's JSON. So when it wants a provider's
// field it spells the name again, in a struct tag, on the other side of an HTTP
// boundary. Nothing compares the two spellings, and Go cannot: the panel may not
// import the constant, which is the whole point of that decision.
//
// # Why it is worth a gate
//
// A rename on the module side leaves both sides compiling and both test suites
// green. What breaks is a number on a screen: the panel decodes a field that is
// no longer sent, gets the zero value, and shows a variant with no stock. No
// error, no log line — the same shape as every defect this repository has been
// caught by this week.
//
// # Why the list is written out AND checked against the tree
//
// The map below is the panel's declared dependencies. A hand-written list is
// right on the day it is written, so the second half of this audit reads the
// panel and refuses a dependency that is not declared. That is the same pairing
// the e-mail fold audit uses, for the same reason: a comparison is only as wide
// as its idea of who is being compared.

// panelDecodedProviderFields names every Query-provider field the admin panel
// decodes by name, and the module that publishes it.
//
// One entry today, and it arrived with ADR 0040: the "in stock" definition is
// computed over inventory's available quantity, and the panel already showed that
// number on the variant screen before the definition existed.
var panelDecodedProviderFields = map[string]string{
	"available_quantity": "inventory",
}

// providerFieldConstant matches a published field name in a module's provider.
var providerFieldConstant = regexp.MustCompile(`Field[A-Z][A-Za-z]*\s*=\s*"([a-z_]+)"`)

// panelFieldReference matches the two ways the panel names a field it decodes: a
// struct tag, and an index into a loosely typed record.
var panelFieldReference = regexp.MustCompile(`json:"([a-z_]+)|\[\s*"([a-z_]+)"\s*\]`)

// TestEveryProviderFieldThePanelDecodesIsStillPublished is the guard.
func TestEveryProviderFieldThePanelDecodesIsStillPublished(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)

	require.NotEmpty(t, published,
		"no Query-provider field constant was found in any module; the scan has gone BLIND and "+
			"this audit would pass whatever the panel decoded")

	for field, module := range panelDecodedProviderFields {
		names, serves := published[module]

		assert.Truef(t, serves,
			"the panel decodes %q and this map says the %s module publishes it, but that module "+
				"publishes no provider fields at all. Either the module was renamed or its "+
				"provider is gone; the panel is decoding a field nothing sends.", field, module)

		if serves {
			assert.Containsf(t, names, field,
				"the panel decodes %q but the %s module no longer publishes a field by that "+
					"name.\nThe panel is an API client (ADR 0030) and cannot import the "+
					"constant, so a rename on the module side leaves both trees compiling and "+
					"both suites green — and shows a screen with a zero where the number was.\n"+
					"Rename the panel's side too, or drop the entry if the panel stopped "+
					"reading it. Published today: %v", field, module, names)
		}
	}
}

// TestEveryProviderFieldThePanelReadsIsDeclared is the half that survives a NEW
// dependency.
//
// The map above is hand-written. This reads the panel's own source and fails when
// it names a field some module publishes without saying so — which is the moment
// a second screen starts depending on a vocabulary nobody is comparing.
func TestEveryProviderFieldThePanelReadsIsDeclared(t *testing.T) {
	t.Parallel()

	published := publishedProviderFields(t)
	everyName := map[string]string{}

	for module, names := range published {
		for _, name := range names {
			everyName[name] = module
		}
	}

	read := panelReadFields(t)

	require.NotEmpty(t, read,
		"no field name was read out of internal/adminui; the panel scan has gone BLIND")

	for _, field := range read {
		module, isProviderField := everyName[field]
		if !isProviderField {
			// A name the panel uses for its own JSON, which is its own business.
			continue
		}

		assert.Containsf(t, panelDecodedProviderFields, field,
			"the panel names %q, which the %s module publishes as a Query-provider field, and "+
				"panelDecodedProviderFields does not mention it.\nThat is a cross-module "+
				"contract held by two literals with nothing comparing them. Add it to the map "+
				"so a rename on the module side fails here instead of on a screen.", field, module)
	}
}

// publishedProviderFields returns the field names each module publishes to the
// Query layer, by module.
func publishedProviderFields(t *testing.T) map[string][]string {
	t.Helper()

	out := map[string][]string{}
	root := filepath.Join(repoRoot, modulesDir)

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		matches := providerFieldConstant.FindAllStringSubmatch(string(body), -1)
		if len(matches) == 0 {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}

		module := strings.Split(filepath.ToSlash(rel), "/")[0]
		for _, match := range matches {
			if !slices.Contains(out[module], match[1]) {
				out[module] = append(out[module], match[1])
			}
		}

		return nil
	})
	require.NoError(t, err, "%s could not be walked for provider fields", modulesDir)

	for module := range out {
		sort.Strings(out[module])
	}

	return out
}

// panelReadFields returns every field name the admin panel names in its own
// production source.
func panelReadFields(t *testing.T) []string {
	t.Helper()

	seen := map[string]bool{}
	root := filepath.Join(repoRoot, "internal", "adminui")

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		for _, match := range panelFieldReference.FindAllStringSubmatch(string(body), -1) {
			for _, name := range match[1:] {
				if name != "" {
					seen[name] = true
				}
			}
		}

		return nil
	})
	require.NoError(t, err, "internal/adminui could not be walked")

	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}
