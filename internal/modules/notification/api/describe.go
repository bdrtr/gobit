package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// The JSON Schema names that appear in the parameter schemas.
//
// The core's counterparts are unexported, and the reason they are repeated here
// is not cost but SILENCE: a type name written as "strig" compiles, the
// document is produced, and it only surfaces when the client that reads the
// schema produces the parameter with the wrong type.
const (
	schemaType  = "type"
	typeString  = "string"
	typeInteger = "integer"
)

// Describe records notification's endpoint into the OpenAPI document.
//
// # Why in this package
//
// The body being described is this package's UNEXPORTED DTO ([deliveryDTO])
// and the schema is derived from it by reflection. Exporting the type so that
// it could be described would mean widening the module's surface merely for the
// sake of producing a document. The query parameters sit here for the same
// reason — the code that knows which parameter is REALLY read is inside api.go;
// had the description been moved to another package, the two would have drifted
// apart silently.
//
// # Why a package-level function
//
// The description looks at no runtime state — the schema comes from the TYPES.
// Attaching the method to [Handler] would have said that the document DEPENDS
// on the service having been constructed; yet the document can be produced, and
// has to be producible, even when [Handler.Routes] has never been called.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminDeliveries, openapi.Operation{
		Summary: "Lists the notification delivery log with paging.",
		Description: "The records DO NOT CARRY A RECIPIENT ADDRESS: the log answers not " +
			"\"who did it go to\" but \"did it go\". To see an order's notifications " +
			"the order id is given to the reference filter.",
		Parameters: []openapi.Parameter{
			queryParameter(queryReference, typeString,
				"Limits the list to the records of a single order."),
			queryParameter(queryStatus, typeString,
				"Delivery status filter: "+statusesText()+". An unrecognized value returns 422."),
			queryParameter(queryLimit, typeInteger,
				"Page size; when it is not given the service's default applies."),
			queryParameter(queryOffset, typeInteger, "Number of records to skip."),
		},
		Responses: map[string]any{
			"200": openapi.Response("The delivery log records", d.List(deliveryDTO{})),
		},
	})
}

// statusesText writes the valid delivery statuses into the document text.
//
// The list is not written by hand: the status set lives in the models package
// and a status added there has to enter the document as well. A hand-written
// list meant the added status silently dropping out of the document.
func statusesText() string {
	statuses := []models.DeliveryStatus{
		models.DeliveryPending, models.DeliverySent,
		models.DeliveryFailed, models.DeliverySkipped,
	}

	out := ""
	for i, status := range statuses {
		if i > 0 {
			out += ", "
		}
		out += status.String()
	}
	return out
}

// queryParameter defines a parameter that is read from the query string.
//
// There is NO "required" flag because there is no required query parameter in
// this module either: a list without a filter is meaningful ("the latest
// notifications"). Carrying the flag anyway would have put a branch that is
// never used into the document generation.
func queryParameter(name, valueType, description string) openapi.Parameter {
	return openapi.Parameter{
		Name:        name,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{schemaType: valueType},
		Description: description,
	}
}
