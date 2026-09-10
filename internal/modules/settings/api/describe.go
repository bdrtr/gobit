package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
)

// Describe writes this module's endpoints into the OpenAPI document.
//
// The schemas come from the package's own unexported DTOs; exporting a type for
// the sake of the document alone would widen the module's surface.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminStoreProfile, openapi.Operation{
		Summary: "Returns who the shop is.",
		Description: "The identity printed on the documents the shop issues. " +
			"\n\n" +
			"IT ANSWERS 404 UNTIL IT IS WRITTEN, and that is the useful answer: a " +
			"client setting a shop up needs to tell \"not configured yet\" apart from " +
			"\"configured as nothing\". Whoever issues an invoice reads this record, so " +
			"an absent profile is refused there too rather than printed as blanks.",
		Responses: map[string]any{
			"200": openapi.Response("The shop's identity", d.Item(storeProfileDTO{})),
		},
	})

	d.Describe(http.MethodPut, pathAdminStoreProfile, openapi.Operation{
		Summary: "Writes who the shop is, replacing what was there.",
		Description: "PUT and not PATCH: the record is an IDENTITY, and a partial write " +
			"would let a shop end up with a legal name from one edit and a tax number " +
			"from another. The first write creates it, so the same verb serves setup and " +
			"correction. " +
			"\n\n" +
			"The legal name and the country are required; the tax number, the tax " +
			"office, the e-mail and the address may all be empty, because a shop that is " +
			"not registered for tax has no number and a country outside Turkey has no " +
			"office. Values are TRIMMED before they are stored: a name of three spaces " +
			"passes a \"not empty\" check and prints as nothing. " +
			"\n\n" +
			"There is one profile per installation (ADR 0009 puts multi-tenancy at the " +
			"installation boundary), so this endpoint takes no identifier.",
		RequestBody: d.RequestBody(storeProfileRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("The identity that was written", d.Item(storeProfileDTO{})),
		},
	})
}
