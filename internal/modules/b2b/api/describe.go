package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// The JSON Schema names parameter schemas use.
//
// The core's own constants are unexported, and the reason they are repeated
// here is not cost but SILENCE: a type name spelled "strig" compiles, the
// document is built, and it surfaces only when a client that reads the schema
// generates the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
	typeBoolean = "boolean"
)

// Describe writes b2b's endpoints into the OpenAPI document.
//
// # Why in this package
//
// The bodies described are this package's UNEXPORTED DTOs (companyRequest,
// employeeDTO …) and the schema is derived from them by reflection. Exporting
// the types to describe them would widen the module's surface only to produce
// a document. The query parameters belong here too, because the code that
// REALLY reads them ([pageParams], [boolParam], [stringParam]) is in this
// package; a description kept in another package would drift from it in
// silence.
//
// # Known limit: the "required" set of request bodies is WIDE
//
// The core derives "required" from the fields encoding/json ALWAYS writes, and
// that is the right answer for RESPONSE bodies. In a request body "required"
// means a field the client MUST SEND, which a type cannot know: this package's
// request DTOs carry no omitempty, so every field looks required — for
// instance, POST /admin/v1/b2b/companies also asks for the address fields that
// may be left empty. Field NAMES and TYPES are right; the schema only asks for
// too much. The right fix is in the CORE (a separate "required" policy for
// request bodies); sprinkling omitempty on tags would move the obligation from
// the service's validation to a json tag, and the two would drift in silence.
func Describe(d *openapi.Doc) {
	describeCompanies(d)
	describeEmployees(d)
	describeStore(d)
}

// describeCompanies describes the company admin endpoints.
func describeCompanies(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/b2b/companies", openapi.Operation{
		Summary: "Creates a new company.",
		Description: "The currency is REQUIRED: employees' spending limits are " +
			"expressed in that currency. The email is NOT unique.",
		RequestBody: d.RequestBody(companyRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created company", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/companies", openapi.Operation{
		Summary: "Lists the companies, filtered and paged.",
		// The parameters are what the handler READS, not what we might want:
		// [Handler.adminListCompanies] reads exactly these three.
		Parameters: []openapi.Parameter{
			queryParameter("email", typeString,
				"Filters by email; the email is not unique, so more than one record can come back."),
			queryParameter("limit", typeInteger,
				"The page size; the service's default when absent."),
			queryParameter("offset", typeInteger, "How many records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of companies", d.List(companyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Returns one company by its identifier.",
		Responses: map[string]any{
			"200": openapi.Response("The company", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Updates the given fields of the company.",
		Description: "The semantics are PARTIAL: a field absent from the body " +
			"does not change, and an empty string given for an address field " +
			"really clears it.",
		RequestBody: d.RequestBody(updateCompanyRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated company", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Soft-deletes the company and its EMPLOYEES.",
		Description: "The employee records are deleted too and their customer " +
			"links are removed: a live employee record always belongs to a " +
			"live company.",
		Responses: map[string]any{
			"204": emptyResponse("The company was deleted"),
		},
	})
}

// describeEmployees describes the employee admin endpoints.
func describeEmployees(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/b2b/employees", openapi.Operation{
		Summary: "Adds an employee to a company.",
		Description: "A customer can be the employee of at most ONE company; " +
			"for a customer already linked the answer is 409. When " +
			"spending_limit is left empty, the employee can spend without limit.",
		RequestBody: d.RequestBody(employeeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("The created employee", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/employees", openapi.Operation{
		Summary: "Lists the employees, filtered and paged.",
		Parameters: []openapi.Parameter{
			queryParameter("company_id", typeString, "Limits the employees to one company."),
			queryParameter("is_company_admin", typeBoolean,
				"true returns only the company admins, false only the others."),
			queryParameter("limit", typeInteger,
				"The page size; the service's default when absent."),
			queryParameter("offset", typeInteger, "How many records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("A page of employees", d.List(employeeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Returns one employee by its identifier.",
		Responses: map[string]any{
			"200": openapi.Response("The employee", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Updates the employee's spending authority.",
		Description: "To REMOVE the limit, send clear_spending_limit: in JSON, " +
			"a null cannot be told apart from a field that was never sent.",
		RequestBody: d.RequestBody(updateEmployeeRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The updated employee", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Soft-deletes the employee and removes the customer link.",
		Description: "Removing the link is necessary: if it stayed, the customer " +
			"could never again be added to any company as an employee.",
		Responses: map[string]any{
			"204": emptyResponse("The employee was deleted"),
		},
	})
}

// describeStore describes the storefront endpoints about the customer's own
// company.
func describeStore(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/b2b/customers/{customer_id}/company", openapi.Operation{
		Summary: "Returns the customer's OWN company.",
		Description: "The company is derived from the customer's own employee " +
			"record; there is NO endpoint that can be called with a company " +
			"identifier. When the customer is the employee of no company, the " +
			"answer is 404. Where this installation has bound a customer " +
			"identity, a path naming a customer that identity CONTRADICTS is " +
			"refused.",
		Responses: storefrontClaimResponses("200",
			openapi.Response("The customer's company", d.Item(companyDTO{}))),
	})

	d.Describe(http.MethodGet, "/store/v1/b2b/customers/{customer_id}/employee", openapi.Operation{
		Summary: "Returns the customer's OWN employee record.",
		Description: "It carries the spending limit, the reset interval and the " +
			"start of the current window. The REMAINING allowance is not " +
			"computed: the order total inside the window is the order module's " +
			"data. Where this installation has bound a customer identity, a " +
			"path naming a customer that identity CONTRADICTS is refused.",
		Responses: storefrontClaimResponses("200",
			openapi.Response("The customer's employee record", d.Item(storeEmployeeDTO{}))),
	})
}

// storefrontClaimResponses merges a storefront operation's success response
// with the refusals ADR 0057 gave both of these routes.
//
// # What an installation that bound NOTHING answers
//
// 401 identity_not_bound, on every one of these routes, since ADR 0125. Between
// ADR 0057 and that record it served the path's claim unchecked, and the
// sentence that stood here said so; the old answer is now a setting an operator
// asks for by name (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM).
//
// The 403 below is still conditional: contradicting a claim needs something to
// contradict it with.
//
// # Why FIVE statuses and not the two this module returns
//
// Because [Handler.storeCustomerID] passes the bound identity's error through
// UNWRAPPED, which is the decision rather than an accident: the embedder picks
// the status by picking its error's kind. An expired session is the embedder's
// 401, a suspended account its 403, an unreachable identity provider its 503.
// Describing only the codes this package itself returns would document half the
// surface and leave the other half to be discovered in production.
//
// The wording is this module's own rather than a shared helper's, because the
// sentence a reader needs names the surface: what these two routes hand back is
// an employer and a spending allowance, and the customer module's copy speaks
// about an address book.
func storefrontClaimResponses(success string, response any) map[string]any {
	return map[string]any{
		success: response,
		"401": openapi.ErrorResponse(
			"The publishable key was accepted and the request is not proven. Either the " +
				"customer identity this installation bound refused it with an error of its " +
				"own that asks the shopper to sign in — the code is the embedder's — or NO " +
				"identity is bound at all, which answers \"identity_not_bound\" since ADR " +
				"0125. An installation that wants the pre-0125 answer, where the path's " +
				"claim was served unchecked, sets " +
				"STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM."),
		"403": openapi.ErrorResponse(
			"Either the request proves a DIFFERENT customer than the one named in the " +
				"path — code \"identity_mismatch\", which does not depend on whether that " +
				"customer is anybody's employee, because the comparison is between the path " +
				"and the proof and no record is read to make it — or the bound identity " +
				"refused this request with a forbidding error of its own."),
		"404": openapi.ErrorResponse(
			"The customer is the employee of no company. Where an identity is bound it is " +
				"a fact about the PROVEN customer, because a contradicted claim never " +
				"reaches the lookup; where none is bound it is a fact about the customer " +
				"the path named, whoever asked."),
		"500": openapi.ErrorResponse(
			"Either the bound customer identity returned neither an identifier nor an " +
				"error — code \"identity_unproven\", a fault in the installation's " +
				"implementation rather than in this request — or something else failed on " +
				"the server."),
		"503": openapi.ErrorResponse(
			"The bound customer identity could not answer: its own dependency, such as the " +
				"identity provider it calls, is unreachable. The code is the embedder's and " +
				"the request is worth retrying."),
	}
}

// queryParameter describes a parameter read from the query string.
//
// None of them is REQUIRED: when absent, the handler applies no filter or
// carries on with the service's default (see [pageParams], [boolParam],
// [stringParam]).
func queryParameter(name, typ, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Schema:      map[string]any{schemaType: typ},
		Description: description,
	}
}

// emptyResponse builds a response definition with NO body.
//
// [openapi.Response] always writes a body schema, and a 204 has NO body (see
// the calls that hand corehttp.WriteJSON nil). Writing an empty schema would
// say "something comes back but its shape is unknown", and a client generator
// would produce a method that waits for a body to read.
func emptyResponse(description string) map[string]any {
	return map[string]any{"description": description}
}
