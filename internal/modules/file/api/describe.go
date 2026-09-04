package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// The JSON Schema names that appear in the schemas.
//
// The core's counterparts are unexported and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and it only surfaces when the client that reads the
// schema produces the parameter with the wrong type.
const (
	schemaType        = "type"
	schemaFormat      = "format"
	schemaProperties  = "properties"
	schemaRequired    = "required"
	schemaDescription = "description"
	typeString        = "string"
	typeObject        = "object"
	typeInteger       = "integer"
	formatBinary      = "binary"
)

// contentMultipart is the media type of the upload endpoint's request body.
const contentMultipart = "multipart/form-data"

// Describe records the endpoints of file into the OpenAPI document.
//
// # Why in this package
//
// The body being described is this package's UNEXPORTED DTO ([uploadDTO]) and
// the schema is derived from it by reflection. Exporting the type just to be
// able to describe it would be widening the module's surface for the sake of
// producing a document. The query parameters stand here for the same reason —
// the code that knows which parameter is REALLY read is in api.go.
//
// # Why a package-level function
//
// The description looks at no run-time state — the schema comes FROM THE TYPES.
// Binding the method to [Handler] would say that the document DEPENDS on the
// service having been built; whereas the document can be produced even when
// [Handler.Routes] has never been called.
//
// # THE SERVING ENDPOINT IS NOT IN THE DOCUMENT
//
// GET /files/{key} is not described and cannot be: the core takes only the
// /admin/v1 and /store/v1 prefixes into the document (see the openapi package).
// The omission is deliberate and it is right — that endpoint is not an API call
// but the target of an <img> tag; there would be no point in a client generator
// producing a method for it. Its address is already given by the "url" field in
// the upload response.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminUploads, openapi.Operation{
		Summary: "Uploads a single file and returns its reachable address.",
		Description: "The body is NOT JSON but " + contentMultipart + " and it carries a " +
			"single \"" + fieldFile + "\" field. The content type is detected not from " +
			"the client's Content-Type header but from the CONTENT of the file; " +
			"if the detected type is not in the allow list it returns 422. The size " +
			"limit is configured with FILE_MAX_UPLOAD_BYTES and exceeding it is a 422 " +
			"as well (code: file_upload_too_large). The client's file name is stored " +
			"for display only; its place in the storage is produced by the server.",
		RequestBody: fileRequestBody(),
		Responses: map[string]any{
			"201": openapi.Response("The uploaded file", d.Item(uploadDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminUploads, openapi.Operation{
		Summary: "Lists the upload ledger, paged from the newest to the oldest.",
		Parameters: []openapi.Parameter{
			queryParameter(queryLimit, typeInteger,
				"The page size; when it is not given the service's default is applied."),
			queryParameter(queryOffset, typeInteger, "The number of records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The upload records", d.List(uploadDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminUpload, openapi.Operation{
		Summary: "Deletes the upload from the storage and from the ledger.",
		Description: "IT IS IDEMPOTENT: an identifier that is already deleted (or that " +
			"never existed) also returns 204. A delete is a claim about an end state " +
			"and a retried cleanup flow must not get an error on its second round.",
		Responses: map[string]any{
			// A response without a body: writing a content schema for a 204
			// would be promising the client generator a body to read.
			"204": map[string]any{schemaDescription: "The upload was deleted; there is no body."},
		},
	})
}

// fileRequestBody describes the multipart request body of the upload endpoint.
//
// [openapi.Doc.RequestBody] CANNOT BE USED HERE: that helper writes the body as
// "application/json" and this endpoint reads no JSON. Calling it for
// convenience would be lying outright in the most visible place of the schema —
// the generated client would try to send the file in a JSON body and every
// request would be rejected.
func fileRequestBody() map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			contentMultipart: map[string]any{
				"schema": map[string]any{
					schemaType:     typeObject,
					schemaRequired: []string{fieldFile},
					schemaProperties: map[string]any{
						fieldFile: map[string]any{
							schemaType:        typeString,
							schemaFormat:      formatBinary,
							schemaDescription: "The raw content of the file to upload.",
						},
					},
				},
			},
		},
	}
}

// queryParameter defines a parameter that is read from the query string.
//
// There is NO "required" flag because there is no required query parameter in
// this module either: when the pagination parameters are not given the service
// applies its default.
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}
