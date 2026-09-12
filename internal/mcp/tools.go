package mcp

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// schemaType is the JSON Schema key a parameter's type sits under.
const schemaType = "type"

// adminPrefix is the surface the tools are derived from.
//
// Only the admin reads. The store surface is a shopper's and its catalog is
// scoped by a publishable key this server does not hold; the panel serves HTML.
// What a model client is for is the operator's own questions.
const adminPrefix = "/admin/v1"

// tool is one entry of the list a client sees.
type tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// path and params are how the call is made; they are not sent to the client.
	path   string  `json:"-"`
	params []param `json:"-"`
}

// param is a path or query parameter the tool accepts.
type param struct {
	name     string
	in       string
	required bool
}

// toolsFrom derives the tool list from the served OpenAPI document.
//
// # Why the document and not the router
//
// The router knows the paths and nothing else. What makes a tool usable is the
// SUMMARY and the parameter descriptions, and those live in the document — the
// same document the installation serves at its schema endpoint, fetched from
// this very process. A tool therefore exists exactly when an endpoint does, and
// it says what the endpoint's own describe block says.
//
// # Why only GET
//
// The server is read-only, and read-only is a property of what it CAN do rather
// than of what it promises. A tool list built from every method would make the
// promise a matter of the caller's restraint.
func toolsFrom(document map[string]any) ([]tool, error) {
	paths, ok := document["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the document carries no paths object; it is not an OpenAPI document")
	}

	var tools []tool
	for path, raw := range paths {
		if !strings.HasPrefix(path, adminPrefix+"/") && path != adminPrefix {
			continue
		}
		methods, methodsOK := raw.(map[string]any)
		if !methodsOK {
			continue
		}
		operation, hasGet := methods["get"].(map[string]any)
		if !hasGet {
			continue
		}

		tools = append(tools, toolOf(path, operation))
	}

	// Sorted by name so the list a client sees does not move between calls: a map
	// walk is unordered, and a tool list that reordered itself would look like
	// the installation had changed.
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	return tools, nil
}

// toolOf turns one operation into a tool.
func toolOf(path string, operation map[string]any) tool {
	t := tool{
		Name:        toolName(path, operation),
		Description: description(operation),
		path:        path,
	}

	properties := map[string]any{}
	var required []string
	for _, raw := range parameters(operation) {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := p["name"].(string)
		in, _ := p["in"].(string)
		if name == "" || (in != "path" && in != "query") {
			continue
		}

		schema, hasSchema := p["schema"].(map[string]any)
		if !hasSchema {
			schema = map[string]any{schemaType: "string"}
		}
		entry := map[string]any{schemaType: schema[schemaType]}
		if entry[schemaType] == nil {
			entry[schemaType] = "string"
		}
		if text, _ := p["description"].(string); text != "" {
			entry["description"] = text
		}
		properties[name] = entry

		isRequired, _ := p["required"].(bool)
		if isRequired {
			required = append(required, name)
		}
		t.params = append(t.params, param{name: name, in: in, required: isRequired})
	}

	sort.Strings(required)
	t.InputSchema = map[string]any{schemaType: "object", "properties": properties}
	if len(required) > 0 {
		t.InputSchema["required"] = required
	}

	return t
}

// toolName is what the client calls the tool.
//
// The operationId when the document carries one, because that is the name a
// generated client would use and two names for one endpoint is one too many.
// Otherwise it is derived from the path, which is stable in the same way the
// endpoint is: it moves when the endpoint moves, and a tool that vanished is a
// clearer failure than one that answers about something else.
func toolName(path string, operation map[string]any) string {
	if id, _ := operation["operationId"].(string); id != "" {
		return id
	}

	trimmed := strings.TrimPrefix(path, adminPrefix+"/")
	trimmed = strings.NewReplacer("/", "_", "{", "", "}", "").Replace(trimmed)

	return "get_" + trimmed
}

// description is what the client shows about the tool.
func description(operation map[string]any) string {
	summary, _ := operation["summary"].(string)
	detail, _ := operation["description"].(string)

	switch {
	case summary != "" && detail != "":
		return summary + "\n\n" + detail
	case summary != "":
		return summary
	case detail != "":
		return detail
	}

	// An endpoint with neither is a gap in its own describe block rather than in
	// this list, and saying so is more useful to a model than an empty string.
	return "This endpoint carries no description in the served schema."
}

// parameters reads the operation's parameter list.
func parameters(operation map[string]any) []any {
	list, _ := operation["parameters"].([]any)

	return list
}

// requestPath builds the address a tool call is made at.
//
// A path parameter is substituted into the pattern and a query parameter is
// appended. A missing required path parameter is refused rather than substituted
// with an empty string: the request would then reach another endpoint entirely —
// the collection instead of the item — and answer confidently about the wrong
// thing.
func (t tool) requestPath(arguments map[string]any) (string, error) {
	path := t.path
	query := url.Values{}

	for _, p := range t.params {
		raw, given := arguments[p.name]
		if !given {
			if p.required {
				return "", fmt.Errorf("%q is required and was not given", p.name)
			}

			continue
		}

		value := fmt.Sprintf("%v", raw)
		if p.in == "path" {
			if value == "" {
				return "", fmt.Errorf("%q is empty; the address would name a different endpoint", p.name)
			}
			path = strings.ReplaceAll(path, "{"+p.name+"}", url.PathEscape(value))

			continue
		}
		query.Set(p.name, value)
	}

	if strings.Contains(path, "{") {
		return "", fmt.Errorf("the address still carries an unfilled parameter: %s", path)
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}

	return path, nil
}
