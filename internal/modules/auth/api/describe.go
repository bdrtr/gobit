package api

import (
	"net/http"
	"strings"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The JSON Schema names that appear in the schema and parameter definitions.
//
// The core's own counterparts are unexported; the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// documentation gets produced, and it only surfaces when the client reading the
// schema produces the field with the wrong type.
const (
	schemaType       = "type"
	schemaFormat     = "format"
	schemaProperties = "properties"
	schemaRef        = "$ref"
	typeString       = "string"
	typeInteger      = "integer"
	typeBoolean      = "boolean"
)

// refPrefix is the path prefix of the references made to component schemas.
const refPrefix = "#/components/schemas/"

// The path leading to the JSON schema in a request body definition.
const (
	bodyContent   = "content"
	bodyMediaType = "application/json"
	bodySchema    = "schema"
)

// The name and the format of the password field.
const (
	// fieldPassword is the JSON name of the plaintext password in the request
	// bodies.
	fieldPassword = "password"
	// formatPassword is JSON Schema's password FORMAT; that it is identical to
	// the field name is a coincidence, not a link.
	formatPassword = "password"
)

// Describe writes auth's ADMIN endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies being described are this package's UNEXPORTED DTOs (loginRequest,
// userDTO …) and the schema is derived from them by reflection. Exporting the
// types in order to be able to describe them would have meant widening the
// module's surface merely for the sake of producing documentation: an exported
// type is a contract and would have become constructible from the outside. The
// query parameters, too, are the ones the handler REALLY reads, and that
// reading lives in this package's admin.go and [pageParams]; had the
// description sat in another package the two would have drifted apart
// silently. That is why the module's [openapi.Describer] implementation
// delegates here.
//
// # Why a package-level function
//
// The description looks at no run-time state — the schema comes FROM THE TYPES.
// Attaching the method to [Handler] would have said the documentation DEPENDS
// on the service having been built; whereas the document can and must be
// produced even when Register has never run.
//
// # There is only /admin/v1
//
// auth has NO storefront endpoint; its counterpart on the store side is not an
// endpoint but the corehttp.RequireStore middleware that reads the publishable
// key.
//
// # SECURITY: how the password appears in the schema
//
// The password fields APPEAR in the request schema — the client has to send it
// and has to know the field's name — but are marked with format: "password"
// (see [passwordBody]). In the response schemas a password NEVER APPEARS and
// cannot appear: the response bodies derive from separate types such as
// [userDTO] and those types have no password field.
//
// # SECURITY: the login endpoint's unprotectedness IS NOT WRITTEN HERE
//
// In the [LoginPath] operation the Security field is DELIBERATELY left empty.
// The core recognizes the login path and writes the empty array there, which
// means "explicitly unprotected" (see internal/core/openapi, security). Writing
// a value here by hand would have copied the decision into two places; the day
// the two drifted apart, the endpoint that hands out the token would appear in
// the schema as asking for a token and the client generator would produce a
// method that can never be called.
//
// # Known limit: the "required" set of the request bodies is TOO WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes
// ([openapi.Doc.SchemaOf]) and that is the right answer for RESPONSE bodies. In
// a request body, however, "required" means a field the client MUST SEND and a
// type cannot know that: because this package's request DTOs carry no
// omitempty, all of them look mandatory — POST /admin/v1/users, for example,
// asks for the password that may be left empty as well. The field NAMES and
// TYPES are correct, that is, the schema invents no wrong field; it merely asks
// for too much. The right fix is IN THE CORE (a separate "required" policy for
// request bodies); sprinkling omitempty over the tags would move the obligation
// from the service's validation to the json tag and the two would drift apart
// silently.
func Describe(d *openapi.Doc) {
	describeIdentity(d)
	describeUsers(d)
	describeAPIKeys(d)
	describeSalesChannels(d)
}

// describeIdentity describes the login, identity read and logout endpoints.
func describeIdentity(d *openapi.Doc) {
	d.Describe(http.MethodPost, LoginPath, openapi.Operation{
		Summary: "Produces an admin session token from an email and a password.",
		Description: "This is the only way to obtain a token and the endpoint is " +
			"UNPROTECTED. A wrong email and a wrong password return the SAME 401; had " +
			"a distinction been made, the response itself would have handed out the " +
			"information 'this email is registered'.",
		RequestBody: passwordBody(d, loginRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The session token, its type and its expiry moment",
				d.Item(loginResponse{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/auth/me", openapi.Operation{
		Summary: "Returns the authenticated caller's identity and scopes.",
		Responses: map[string]any{
			"200": openapi.Response("The caller's identity", d.Item(principalResponse{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/auth/logout", openapi.Operation{
		Summary: "Closes ALL of the caller's sessions.",
		Description: "The revocation is WHOLESALE: an admin logging out from their " +
			"phone has closed their session on the laptop too. The response is 200 and " +
			"not 204, because a status code cannot say the scope of the revocation and " +
			"the moment it rests on.",
		// The endpoint READS NO BODY: it knows who is logging out from the token
		// (see [Handler.adminLogout]). Writing a body into the schema would both
		// promise a field that is never read and imply that the question "whose
		// session" can be asked from the body.
		Responses: map[string]any{
			"200": openapi.Response("The scope and the moment of the revocation",
				d.Item(logoutResponse{})),
		},
	})
}

// describeUsers describes the admin user endpoints.
func describeUsers(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/users", openapi.Operation{
		Summary: "Creates a new admin user.",
		Description: "The password in the body may be left empty; the user is then " +
			"created without a password and POST /admin/v1/users/{id}/password has to " +
			"be called first before they can log in. The requested scopes CANNOT " +
			"EXCEED the caller's own. No password APPEARS in the response.",
		RequestBody: passwordBody(d, createUserRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created user", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/users", openapi.Operation{
		Summary: "Lists the admin users, filtering and paging them.",
		// The parameters are the ones the handler READS, not the ones we could
		// wish for: [Handler.adminListUsers] reads only these four.
		Parameters: append(pageParameters(),
			queryParameter("email", typeString, "Limits the users to a single email."),
			queryParameter("scope", typeString, "Returns the users carrying the given scope."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of users", d.List(userDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Returns a single admin user.",
		Responses: map[string]any{
			"200": openapi.Response("The user", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Updates the given fields of the user.",
		// There is NO password in the body and this is deliberate (see
		// [updateUserRequest]): had it been in the same body, it would have been
		// possible for a request updating a name to change the password by
		// accident.
		RequestBody: d.RequestBody(updateUserRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated user", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Soft-deletes the user and their login credentials.",
		Responses: map[string]any{
			"204": emptyResponse("The user was deleted"),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/users/{id}/password", openapi.Operation{
		Summary: "Assigns the user's password.",
		Description: "The endpoint is separate: had it been put into the same body " +
			"as the profile update, it would have been possible for a request changing " +
			"a name to reset the password by accident. The response HAS NO BODY; there " +
			"is no point in writing anything password-related back.",
		RequestBody: passwordBody(d, setPasswordRequest{}),
		Responses: map[string]any{
			"204": emptyResponse("The password was changed"),
		},
	})
}

// describeAPIKeys describes the API key and channel link endpoints.
func describeAPIKeys(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/api-keys", openapi.Operation{
		Summary: "Produces a new API key and returns its plaintext ONCE.",
		Description: "The plaintext key is returned ONLY in this response's \"key\" " +
			"field and can never again be read from any endpoint: storage is done over " +
			"the digest alone. If the client does not store the value now the key is " +
			"lost and the only remedy is to revoke it and produce a new one. Every " +
			"other endpoint returns only the masked representation (\"redacted\") of " +
			"the key. created_by is filled not from the body but from the " +
			"authenticated identity; the requested scopes CANNOT EXCEED the caller's " +
			"own.",
		RequestBody: d.RequestBody(createAPIKeyRequest{}),
		Responses: map[string]any{
			"201": openapi.Response(
				"The key record and its PLAINTEXT; the plaintext is never returned again",
				d.Item(createAPIKeyResponse{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys", openapi.Operation{
		Summary: "Lists the API keys, filtering and paging them.",
		Parameters: append(pageParameters(),
			queryParameter("type", typeString,
				"The key's type: \"publishable\" or \"secret\"."),
			queryParameter("revoked", typeBoolean,
				"true returns only the revoked ones, false only the active ones."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of keys; it CONTAINS NO plaintext",
				d.List(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys/{id}", openapi.Operation{
		Summary: "Returns a single API key; it CONTAINS NO plaintext.",
		Responses: map[string]any{
			"200": openapi.Response("The key record", d.Item(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/api-keys/{id}", openapi.Operation{
		Summary: "Soft-deletes the API key.",
		Description: "After a leak the operation to prefer is revocation " +
			"(POST /admin/v1/api-keys/{id}/revoke); deletion is for cleaning up a " +
			"record that was created by mistake.",
		Responses: map[string]any{
			"204": emptyResponse("The key was deleted"),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/api-keys/{id}/revoke", openapi.Operation{
		Summary: "Revokes the API key.",
		Description: "REVOCATION IS NOT DELETION: the record stays in the list and " +
			"when and by whom it was closed remains visible. On an already revoked key " +
			"a 409 is returned.",
		// The endpoint READS NO BODY: it knows the key to be revoked from the
		// path and who is revoking it from the token (see
		// [Handler.adminRevokeAPIKey]).
		Responses: map[string]any{
			"200": openapi.Response("The revoked key record", d.Item(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys/{id}/sales-channels", openapi.Operation{
		Summary: "Returns the sales channels the key is attached to.",
		// There is NO query parameter: the endpoint does not page, it writes all
		// the links on a single page (see [writeItems]). Announcing
		// limit/offset would have meant promising a feature the server silently
		// ignores.
		Responses: map[string]any{
			"200": openapi.Response("The attached channels; the disabled ones are listed too",
				d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/api-keys/{id}/sales-channels", openapi.Operation{
		Summary:     "Attaches the publishable key to a sales channel.",
		RequestBody: d.RequestBody(linkChannelRequest{}),
		// The response is 200 and NOT 201, and it is a LIST rather than a single
		// record: once the link is established the key's CURRENT channel list is
		// returned (see [Handler.adminLinkKeyChannel]). Writing 201 would
		// produce a wrong branch in the client generator, and writing a single
		// envelope would produce a body that cannot be read.
		Responses: map[string]any{
			"200": openapi.Response("The current channel list after the link was established",
				d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}",
		openapi.Operation{
			Summary: "Removes the link between the key and the sales channel.",
			Responses: map[string]any{
				"204": emptyResponse("The link was removed"),
			},
		})
}

// describeSalesChannels describes the sales channel endpoints.
//
// # A component name CLASH was resolved
//
// This module's [salesChannelRequest] type creates the channel ITSELF; the
// product module had a type carrying the same Go name as well, but that one
// ATTACHES a product to a channel. When two different things ask for the same
// published component name ("SalesChannelRequest"), [openapi.Doc.Build] returns
// an error and the WHOLE document cannot be produced — not just that endpoint,
// but /openapi.json itself becomes a 500.
//
// The fix was to name the type on the product side after what it really is
// (linkSalesChannelRequest). A component name is the published contract; a Go
// naming coincidence is not allowed to decide it.
func describeSalesChannels(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/sales-channels", openapi.Operation{
		Summary:     "Creates a new sales channel.",
		RequestBody: d.RequestBody(salesChannelRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created channel", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/sales-channels", openapi.Operation{
		Summary: "Lists the sales channels, filtering and paging them.",
		Parameters: append(pageParameters(),
			queryParameter("name", typeString, "Filters the channels by name."),
			queryParameter("is_disabled", typeBoolean,
				"If it is not given no filtering happens; false returns only the enabled channels."),
		),
		Responses: map[string]any{
			"200": openapi.Response("A page of channels", d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary: "Returns a single sales channel.",
		Responses: map[string]any{
			"200": openapi.Response("The channel", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary:     "Updates the given fields of the sales channel.",
		RequestBody: d.RequestBody(updateSalesChannelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated channel", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary: "Soft-deletes the sales channel and removes the key links.",
		Responses: map[string]any{
			"204": emptyResponse("The channel was deleted"),
		},
	})
}

// passwordBody derives the password-carrying request body from the DTO and
// marks the password field with format: "password".
//
// # Why it is marked afterwards
//
// The core derives the schema FROM THE GO TYPE and [secret] is an ordinary
// string on the wire; the type does not carry the information "this is a
// password". Adding a hook to the core would have moved auth's concept into the
// core (Principle 2.4), so the mark is written ON TOP of the derived schema,
// here.
//
// # What the mark buys
//
// format: "password" is not a validation but a PRESENTATION contract: client
// generators and schema viewers mask the field, and tools producing sample
// requests do not print it in the clear. A password left unmarked is an
// ordinary string that looks just like an email.
//
// # Why writing to the COMPONENT is safe
//
// The mark is written into the component itself through [openapi.Doc.Schemas].
// The map is copied at the top level, but component schemas are SHARED maps and
// the write lands on them. Writing to the component is correct because all
// three password-carrying types are ONLY request bodies; no response refers to
// them. The coupling is subtle and is deliberately locked down by a test
// (describe_internal_test.go): if the core one day returns a deep copy the test
// falls over, and the schema is not left silently unmarked.
func passwordBody(d *openapi.Doc, bodyType any) map[string]any {
	body := d.RequestBody(bodyType)

	schema := subMap(body, bodyContent, bodyMediaType, bodySchema)

	ref, _ := schema[schemaRef].(string)
	component, _ := d.Schemas()[strings.TrimPrefix(ref, refPrefix)].(map[string]any)

	if password := subMap(component, schemaProperties, fieldPassword); password != nil {
		password[schemaFormat] = formatPassword
	}

	return body
}

// subMap follows a path through nested maps; returns nil if the path breaks.
//
// A nil root is safe too: in Go, reading from a nil map gives the zero value and
// the path is cut at the first step.
func subMap(root map[string]any, path ...string) map[string]any {
	node := root

	for _, name := range path {
		child, ok := node[name].(map[string]any)
		if !ok {
			return nil
		}

		node = child
	}

	return node
}

// pageParameters returns the paging parameters.
//
// Neither of them IS MANDATORY: when they are not given the service's default
// is applied (see [pageParams]). The reason they were pulled into a shared
// helper is not the repetition itself but the drifting apart of the
// descriptions — had they been hand-written at three list endpoints, the others
// would silently go stale the day one of them changed.
func pageParameters() []openapi.Parameter {
	return []openapi.Parameter{
		queryParameter("limit", typeInteger,
			"The page size; if it is not given the service's default is applied."),
		queryParameter("offset", typeInteger, "The number of records to skip."),
	}
}

// queryParameter defines a parameter that is read from the query string.
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse produces a BODYLESS response definition.
//
// [openapi.Response] always writes a body schema, whereas a 204 HAS NO body
// (see admin.go, the calls that hand nil to corehttp.WriteJSON). Writing an
// empty schema would have meant "something is returned but its shape is
// unknown" and the client generator would have produced a method expecting a
// body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
