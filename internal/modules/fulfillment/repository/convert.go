package repository

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository/fulfillmentdb"
)

// This file is the SINGLE place of the pgtype <-> domain model conversions and
// of the driver error classification.
//
// The boundary being here is deliberate: driver-specific types
// (pgtype.Timestamptz, []byte for jsonb, *pgconn.PgError) DO NOT LEAVE the
// repository. The service and the API layer see time.Time, json.RawMessage and
// core/errors typed errors.

// Error codes. The calling side can look at these with errors.CodeOf; the API
// layer passes the same codes on to the client as well.
const (
	codeProfileNotFound        = "fulfillment_shipping_profile_not_found"
	codeOptionNotFound         = "fulfillment_shipping_option_not_found"
	codeRuleNotFound           = "fulfillment_shipping_option_rule_not_found"
	codeFulfillmentNotFound    = "fulfillment_not_found"
	codeManualShipmentNotFound = "fulfillment_manual_shipment_not_found"
	codeLocationNotFound       = "fulfillment_shipping_location_not_found"
	codeProfileNameExists      = "fulfillment_shipping_profile_name_exists"
	codeFulfillmentExists      = "fulfillment_idempotency_key_exists"
	codeItemExists             = "fulfillment_item_already_added"
	codeAmountOutOfRange       = "fulfillment_amount_out_of_range"
	codeQuantityOutOfRange     = "fulfillment_quantity_out_of_range"
	codeStatusInvalid          = "fulfillment_status_invalid"
	codeCurrencyInvalid        = "fulfillment_currency_invalid"
	codeDataInvalid            = "fulfillment_json_invalid"
	codeRuleInvalid            = "fulfillment_rule_invalid"
	codeStampMissing           = "fulfillment_status_stamp_missing"
	codeTxRequired             = "fulfillment_tx_required"
	codeTxBeginFailed          = "fulfillment_tx_begin_failed"
	codeTxCommitFailed         = "fulfillment_tx_commit_failed"
	codeQueryFailed            = "fulfillment_query_failed"
	codeConcurrentUpdate       = "fulfillment_concurrent_update"
)

// Constraint and index names; they are used to convert a driver error into a
// meaningful typed error. The names are IDENTICAL to the names in the migration.
const (
	constraintProfileNameUniq     = "shipping_profiles_name_uniq"
	constraintFulfillmentKeyUniq  = "fulfillments_idempotency_uniq"
	constraintFulfillmentItemUniq = "fulfillment_items_line_uniq"
	// constraintStatusSuffix is the common suffix of the CHECK constraints
	// that validate the status value.
	constraintStatusSuffix = "_status_valid"
	// constraintStampSuffix is the common suffix of the CHECK constraints that
	// require the status and the timestamp to agree with each other.
	constraintStampSuffix = "_stamp"
)

// checkConstraintMessages are the human-readable counterparts of the CHECK
// constraints that validate at the field level.
//
// Their violations are INVALID INPUT cases the client can correct; the service
// already rejects them, and the mapping here makes an intervention performed
// directly through SQL return an understandable error as well.
var checkConstraintMessages = map[string]string{
	"shipping_options_calculated_zero":             "the amount of a calculated shipping option must be zero; the fee comes from the provider",
	"shipping_options_price_type_valid":            "the price type of a shipping option must be 'flat' or 'calculated'",
	"shipping_options_name_check":                  "the name of a shipping option cannot be empty",
	"shipping_options_provider_check":              "the provider of a shipping option cannot be empty",
	"shipping_profiles_name_check":                 "the name of a shipping profile cannot be empty",
	"shipping_profiles_type_valid":                 "the type of a shipping profile must be 'default', 'gift_card' or 'custom'",
	"shipping_option_rules_attribute_check":        "the rule attribute cannot be empty",
	"shipping_option_rules_operator_check":         "unrecognized rule operator",
	"shipping_option_rules_values_check":           "the rule must contain at least one value",
	"fulfillments_reference_check":                 "the reference of a fulfillment cannot be empty",
	"fulfillments_provider_check":                  "the provider of a fulfillment cannot be empty",
	"fulfillments_key_check":                       "the idempotency key cannot be empty",
	"fulfillment_items_line_check":                 "the order line of a fulfillment item cannot be empty",
	"fulfillment_manual_shipments_key_check":       "the idempotency key cannot be empty",
	"fulfillment_manual_shipments_reference_check": "the reference of a shipment cannot be empty",
}

// amountConstraints are the CHECK constraints that validate the range of the
// shipping amount. They are kept apart because their code is
// [codeAmountOutOfRange]: the client must be able to tell which field exceeded
// its bound from the code itself.
var amountConstraints = map[string]string{
	"shipping_options_amount_nonneg": "the shipping amount cannot be negative",
	"shipping_options_amount_max":    "the shipping amount exceeds the upper bound",
}

// jsonNullLiteral is the JSON body that means "no value" in a jsonb column.
// The driver gives a NULL column as an empty slice and a JSON null as this
// string.
const jsonNullLiteral = "null"

// jsonEmptyObject is an empty JSON object; it is the default of NOT NULL
// columns.
const jsonEmptyObject = "{}"

// PostgreSQL SQLSTATE codes.
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateDeadlockDetected    = "40P01"
)

// profileNotFound produces the common error for a missing shipping profile.
func profileNotFound(id string) error {
	return errors.NotFound(codeProfileNotFound, "shipping profile not found: %s", id)
}

// optionNotFound produces the common error for a missing shipping option.
func optionNotFound(id string) error {
	return errors.NotFound(codeOptionNotFound, "shipping option not found: %s", id)
}

// ruleNotFound produces the common error for a missing rule.
func ruleNotFound(id string) error {
	return errors.NotFound(codeRuleNotFound, "shipping option rule not found: %s", id)
}

// fulfillmentNotFound produces the common error for a missing fulfillment.
func fulfillmentNotFound(id string) error {
	return errors.NotFound(codeFulfillmentNotFound, "fulfillment not found: %s", id)
}

// manualShipmentNotFound produces the common error for a missing provider
// shipment.
func manualShipmentNotFound(id string) error {
	return errors.NotFound(codeManualShipmentNotFound,
		"manual provider shipment not found: %s", id)
}

// locationNotFound produces the common error for a location that has no policy.
//
// "Has no policy" and "there is no such location" ARE NOT THE SAME THING and the
// message says so: this module does not know whether a location exists (that is
// the inventory module's knowledge), it only reports that its own record is
// missing.
func locationNotFound(id string) error {
	return errors.NotFound(codeLocationNotFound,
		"shipping policy of the location not found: %s", id)
}

// classify converts a driver error into a typed error.
//
// Uniqueness, foreign key and CHECK violations are cases the client can correct;
// if they were not classified they would all appear as 500 and the real reason
// would remain only in the log. A deadlock is handled separately for the same
// reason: there is nothing wrong with the transaction itself, it CAN BE RETRIED.
func classify(err error, code, format string, a ...any) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return errors.Wrap(err, errors.KindInternal, code, format, a...)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		switch pgErr.ConstraintName {
		case constraintProfileNameUniq:
			return errors.Wrap(err, errors.KindConflict, codeProfileNameExists,
				"a shipping profile with this name already exists")
		case constraintFulfillmentKeyUniq:
			return errors.Wrap(err, errors.KindConflict, codeFulfillmentExists,
				"a fulfillment created with this idempotency key already exists")
		case constraintFulfillmentItemUniq:
			return errors.Wrap(err, errors.KindConflict, codeItemExists,
				"the same order line cannot appear twice in a fulfillment")
		}
	case sqlStateForeignKeyViolation:
		return foreignKeyError(err, pgErr.ConstraintName)
	case sqlStateCheckViolation:
		return checkError(err, pgErr.ConstraintName, code, format, a...)
	case sqlStateDeadlockDetected:
		// Because the fulfillment flows take a single row lock this does not
		// occur in normal operation; this is the last line of defense. The
		// transaction has been rolled back, the same request can be retried as
		// it is — which is why this is Conflict and not Internal (500).
		return errors.Wrap(err, errors.KindConflict, codeConcurrentUpdate,
			"conflicted with a concurrent transaction; the request can be retried")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// foreignKeyError converts a foreign key violation into a meaningful typed
// error.
//
// The constraint name says which link was broken: an option is bound to a
// profile, a fulfillment to an option, an item to a fulfillment. DELETING an
// option that has fulfillments, on the other hand, hits RESTRICT and that is a
// conflict — the client must close the fulfillment first.
func foreignKeyError(err error, constraint string) error {
	switch {
	case strings.Contains(constraint, "shipping_profile_id"):
		return errors.Wrap(err, errors.KindNotFound, codeProfileNotFound,
			"shipping profile not found")
	case strings.Contains(constraint, "shipping_option_id"):
		// The RESTRICT constraint given from the fulfillment table to the
		// option carries the same name in both directions: writing to a missing
		// option and deleting an option in use both land here. Because the
		// delete path is turned into a soft delete on the service side, the
		// case left here is a missing option.
		return errors.Wrap(err, errors.KindNotFound, codeOptionNotFound,
			"shipping option not found")
	case strings.Contains(constraint, "fulfillment_id"):
		return errors.Wrap(err, errors.KindNotFound, codeFulfillmentNotFound,
			"fulfillment not found")
	default:
		return errors.Wrap(err, errors.KindNotFound, codeOptionNotFound,
			"linked record not found")
	}
}

// checkError converts a CHECK constraint violation into a meaningful typed
// error.
func checkError(err error, constraint, code, format string, a ...any) error {
	if message, ok := amountConstraints[constraint]; ok {
		return errors.Wrap(err, errors.KindInvalid, codeAmountOutOfRange, "%s", message)
	}
	if message, ok := checkConstraintMessages[constraint]; ok {
		return errors.Wrap(err, errors.KindInvalid, codeRuleInvalid, "%s", message)
	}
	switch {
	case constraint == "fulfillment_items_quantity_check":
		return errors.Wrap(err, errors.KindInvalid, codeQuantityOutOfRange,
			"the quantity of a fulfillment item must be positive")
	case strings.HasSuffix(constraint, constraintStampSuffix):
		// The status was written but the timestamp accompanying it was left
		// empty. The service does not produce this; an error landing here is
		// the module's own inconsistency and must be diagnosed.
		return errors.Wrap(err, errors.KindInternal, codeStampMissing,
			"a fulfillment status cannot be written without a timestamp")
	case strings.HasSuffix(constraint, constraintStatusSuffix):
		return errors.Wrap(err, errors.KindInvalid, codeStatusInvalid,
			"undefined status value")
	case strings.HasSuffix(constraint, "_currency_format"):
		return errors.Wrap(err, errors.KindInvalid, codeCurrencyInvalid,
			"the currency must be a three-letter ISO 4217 code")
	}
	return errors.Wrap(err, errors.KindInternal, code, format, a...)
}

// --- conversion --------------------------------------------------------------

// toTime converts a pgtype timestamp to a UTC time.Time.
func toTime(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time.UTC()
}

// toTimePtr converts a nullable timestamp to a *time.Time.
func toTimePtr(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	t := ts.Time.UTC()
	return &t
}

// fromTimePtr converts a *time.Time to a pgtype timestamp; nil becomes SQL NULL.
func fromTimePtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t.UTC(), Valid: true}
}

// toJSONRaw converts a jsonb column to raw JSON. An empty column returns nil.
func toJSONRaw(raw []byte) json.RawMessage {
	if len(raw) == 0 || string(raw) == jsonNullLiteral || string(raw) == jsonEmptyObject {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// fromJSONRaw converts raw JSON to the bytes to be written into a jsonb column.
//
// The column is NOT NULL and the distinction between "no data" and "empty data"
// means nothing in this module; a fulfillment without provider data carries an
// empty object.
func fromJSONRaw(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return []byte(jsonEmptyObject)
	}
	return raw
}

// toJSONMap converts a jsonb column to a map.
//
// An empty or JSON null value returns a nil map; that way the field does not
// appear at all in the API response instead of "metadata": null (omitempty).
func toJSONMap(raw []byte) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == jsonNullLiteral {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeDataInvalid,
			"could not decode JSON field")
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// fromJSONMap converts a map to the bytes to be written into a jsonb column.
func fromJSONMap(m map[string]any) ([]byte, error) {
	if len(m) == 0 {
		return []byte(jsonEmptyObject), nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInvalid, codeDataInvalid,
			"could not encode JSON field")
	}
	return raw, nil
}

// toProfile converts a database row to the domain model.
func toProfile(row fulfillmentdb.ShippingProfile) (models.ShippingProfile, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.ShippingProfile{}, err
	}
	return models.ShippingProfile{
		ID:        row.ID,
		Name:      row.Name,
		Type:      models.ProfileType(row.Type),
		Metadata:  meta,
		CreatedAt: toTime(row.CreatedAt),
		UpdatedAt: toTime(row.UpdatedAt),
		DeletedAt: toTimePtr(row.DeletedAt),
	}, nil
}

// toOption converts a database row to the domain model.
//
// Rules ARE NOT FILLED IN: the rules come from a separate query and the caller
// attaches them in bulk (without doing N+1).
func toOption(row fulfillmentdb.ShippingOption) (models.ShippingOption, error) {
	data, err := toJSONMap(row.Data)
	if err != nil {
		return models.ShippingOption{}, err
	}
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.ShippingOption{}, err
	}
	return models.ShippingOption{
		ID:                row.ID,
		Name:              row.Name,
		ProviderID:        row.ProviderID,
		ShippingProfileID: row.ShippingProfileID,
		PriceType:         models.PriceType(row.PriceType),
		Amount:            row.Amount,
		CurrencyCode:      row.CurrencyCode,
		RegionID:          row.RegionID,
		IsReturn:          row.IsReturn,
		AdminOnly:         row.AdminOnly,
		Data:              data,
		Metadata:          meta,
		CreatedAt:         toTime(row.CreatedAt),
		UpdatedAt:         toTime(row.UpdatedAt),
		DeletedAt:         toTimePtr(row.DeletedAt),
	}, nil
}

// toRule converts a database row to the domain model.
func toRule(row fulfillmentdb.ShippingOptionRule) models.ShippingOptionRule {
	values := make([]string, len(row.RuleValues))
	copy(values, row.RuleValues)
	return models.ShippingOptionRule{
		ID:               row.ID,
		ShippingOptionID: row.ShippingOptionID,
		Attribute:        row.Attribute,
		Operator:         models.RuleOperator(row.Operator),
		Values:           values,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
		DeletedAt:        toTimePtr(row.DeletedAt),
	}
}

// toFulfillment converts a database row to the domain model.
//
// Items ARE NOT FILLED IN: the items come from a separate query and the caller
// attaches them in bulk.
func toFulfillment(row fulfillmentdb.Fulfillment) (models.Fulfillment, error) {
	meta, err := toJSONMap(row.Metadata)
	if err != nil {
		return models.Fulfillment{}, err
	}
	return models.Fulfillment{
		ID:               row.ID,
		Reference:        row.Reference,
		ShippingOptionID: row.ShippingOptionID,
		ProviderID:       row.ProviderID,
		ExternalID:       row.ExternalID,
		Status:           models.FulfillmentStatus(row.Status),
		TrackingNumber:   row.TrackingNumber,
		TrackingURL:      row.TrackingUrl,
		IdempotencyKey:   row.IdempotencyKey,
		ShippedAt:        toTimePtr(row.ShippedAt),
		DeliveredAt:      toTimePtr(row.DeliveredAt),
		CanceledAt:       toTimePtr(row.CanceledAt),
		Data:             toJSONRaw(row.Data),
		Metadata:         meta,
		CreatedAt:        toTime(row.CreatedAt),
		UpdatedAt:        toTime(row.UpdatedAt),
	}, nil
}

// toItem converts a database row to the domain model.
func toItem(row fulfillmentdb.FulfillmentItem) models.FulfillmentItem {
	return models.FulfillmentItem{
		ID:            row.ID,
		FulfillmentID: row.FulfillmentID,
		LineItemID:    row.LineItemID,
		Quantity:      row.Quantity,
		CreatedAt:     toTime(row.CreatedAt),
		UpdatedAt:     toTime(row.UpdatedAt),
	}
}

// toManualShipment converts a database row to the provider's ledger model.
func toManualShipment(row fulfillmentdb.FulfillmentManualShipment) models.ManualShipment {
	return models.ManualShipment{
		ID:             row.ID,
		IdempotencyKey: row.IdempotencyKey,
		Reference:      row.Reference,
		OptionID:       row.OptionID,
		Status:         models.FulfillmentStatus(row.Status),
		TrackingNumber: row.TrackingNumber,
		TrackingURL:    row.TrackingUrl,
		Data:           toJSONRaw(row.Data),
		CreatedAt:      toTime(row.CreatedAt),
		UpdatedAt:      toTime(row.UpdatedAt),
	}
}
