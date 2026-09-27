package service

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// countryPattern and currencyPattern are the forms a rule's codes take once
// folded to upper case.
var (
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
)

// NormalizeSegmentRule checks a rule against the vocabulary and returns it as
// it is stored and evaluated (ADR 0217): codes in upper case, a country listed
// once, and every value in its one JSON form.
//
// A rule reads orders only through order_count and net_spend, so a window
// without either is refused, as is a currency without net_spend: a field the
// evaluation would not read is a mistake the operator should hear about.
func NormalizeSegmentRule(in models.SegmentRule) (models.SegmentRule, error) {
	if len(in.Conditions) == 0 || len(in.Conditions) > models.MaxSegmentConditions {
		return models.SegmentRule{}, invalidSegment("a rule holds 1 to %d conditions, %d given",
			models.MaxSegmentConditions, len(in.Conditions))
	}
	out := models.SegmentRule{Conditions: make([]models.SegmentCondition, 0, len(in.Conditions))}
	readsOrders, readsSpend := false, false
	for i, c := range in.Conditions {
		normalized, err := normalizeCondition(c)
		if err != nil {
			return models.SegmentRule{}, errors.Wrap(err, errors.KindInvalid, models.CodeSegmentInvalid,
				"condition %d", i+1)
		}
		readsOrders = readsOrders || c.Attribute == models.SegmentOrderCount || c.Attribute == models.SegmentNetSpend
		readsSpend = readsSpend || c.Attribute == models.SegmentNetSpend
		out.Conditions = append(out.Conditions, normalized)
	}

	currency := strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	switch {
	case readsSpend && !currencyPattern.MatchString(currency):
		return models.SegmentRule{}, invalidSegment("net_spend is summed in a currency; currency_code %q is none",
			in.CurrencyCode)
	case !readsSpend && currency != "":
		return models.SegmentRule{}, invalidSegment("currency_code is read only by net_spend, and no condition names it")
	}
	out.CurrencyCode = currency

	switch {
	case in.WindowDays < 0 || in.WindowDays > models.MaxSegmentWindowDays:
		return models.SegmentRule{}, invalidSegment("window_days is between 0 and %d, %d given",
			models.MaxSegmentWindowDays, in.WindowDays)
	case !readsOrders && in.WindowDays != 0:
		return models.SegmentRule{}, invalidSegment("window_days is read only by order_count and net_spend, and no condition names them")
	}
	out.WindowDays = in.WindowDays

	return out, nil
}

// normalizeCondition checks one condition against its attribute's operators
// and value type.
func normalizeCondition(c models.SegmentCondition) (models.SegmentCondition, error) {
	operators, known := models.SegmentOperators[c.Attribute]
	if !known {
		return models.SegmentCondition{}, invalidSegment("%q is no attribute a segment reads", c.Attribute)
	}
	if !slices.Contains(operators, c.Operator) {
		return models.SegmentCondition{}, invalidSegment("%s takes %s, not %q",
			c.Attribute, strings.Join(operators, ", "), c.Operator)
	}
	out := models.SegmentCondition{Attribute: c.Attribute, Operator: c.Operator}

	if c.Operator == models.SegmentIn || c.Operator == models.SegmentNin {
		if len(c.Value) > 0 {
			return models.SegmentCondition{}, invalidSegment("%s lists its countries in values, not value", c.Operator)
		}
		if len(c.Values) == 0 || len(c.Values) > models.MaxSegmentValues {
			return models.SegmentCondition{}, invalidSegment("%s lists 1 to %d countries, %d given",
				c.Operator, models.MaxSegmentValues, len(c.Values))
		}
		for _, v := range c.Values {
			code := strings.ToUpper(strings.TrimSpace(v))
			if !countryPattern.MatchString(code) {
				return models.SegmentCondition{}, invalidSegment("%q is no country code", v)
			}
			if !slices.Contains(out.Values, code) {
				out.Values = append(out.Values, code)
			}
		}

		return out, nil
	}
	if len(c.Values) > 0 {
		return models.SegmentCondition{}, invalidSegment("%s compares with one value, not values", c.Operator)
	}

	var value any
	switch c.Attribute {
	case models.SegmentHasAccount:
		var b bool
		if err := strictDecode(c.Value, &b); err != nil {
			return models.SegmentCondition{}, invalidSegment("has_account compares with true or false")
		}
		value = b
	case models.SegmentCountryCode:
		var text string
		if err := strictDecode(c.Value, &text); err != nil {
			return models.SegmentCondition{}, invalidSegment("country_code compares with a country code")
		}
		code := strings.ToUpper(strings.TrimSpace(text))
		if !countryPattern.MatchString(code) {
			return models.SegmentCondition{}, invalidSegment("%q is no country code", text)
		}
		value = code
	default:
		// json.Number takes a quoted number too; a count written as text is not
		// the number the evaluation compares.
		var n json.Number
		if trimmed := bytes.TrimSpace(c.Value); len(trimmed) > 0 && trimmed[0] == '"' {
			return models.SegmentCondition{}, invalidSegment("%s compares with a whole number, not text", c.Attribute)
		}
		if err := strictDecode(c.Value, &n); err != nil {
			return models.SegmentCondition{}, invalidSegment("%s compares with a whole number", c.Attribute)
		}
		whole, err := n.Int64()
		if err != nil || whole < 0 || whole > models.MaxSegmentNumber {
			return models.SegmentCondition{}, invalidSegment("%s compares with a whole number from 0 to %d, %s given",
				c.Attribute, int64(models.MaxSegmentNumber), n)
		}
		value = whole
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return models.SegmentCondition{}, errors.Wrap(err, errors.KindInternal, models.CodeSegmentInvalid,
			"the value could not be encoded")
	}
	out.Value = raw

	return out, nil
}

// strictDecode reads a JSON value into dst, numbers as json.Number, and refuses
// an absent value.
func strictDecode(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.Invalid(models.CodeSegmentInvalid, "no value")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	return dec.Decode(dst)
}

// invalidSegment is a rule the vocabulary does not allow.
func invalidSegment(format string, a ...any) error {
	return errors.Invalid(models.CodeSegmentInvalid, format, a...)
}

// SetGroupSegment makes the group a segment whose members the rule decides, or
// gives a segment a new rule (ADR 0217).
//
// The members are written by the segment job's next pass; until then the group
// keeps the ones it has. A group becomes a segment only while fewer than
// models.MaxSegments others are.
func (s *Service) SetGroupSegment(ctx context.Context, groupID string, rule models.SegmentRule) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return models.CustomerGroup{}, err
	}
	normalized, err := NormalizeSegmentRule(rule)
	if err != nil {
		return models.CustomerGroup{}, err
	}
	return s.repo.SetGroupSegment(ctx, groupID, normalized, models.MaxSegments, s.clock())
}

// ClearGroupSegment hands a segment back to the operator: the rule goes, the
// members it wrote stay, and hand edits are taken again.
func (s *Service) ClearGroupSegment(ctx context.Context, groupID string) (models.CustomerGroup, error) {
	if err := s.ready(); err != nil {
		return models.CustomerGroup{}, err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return models.CustomerGroup{}, err
	}
	return s.repo.ClearGroupSegment(ctx, groupID, s.clock())
}

// interopSegment is one segment as the segment flow reads it:
//
//	{"group_id": "custgrp_...", "set_at": "RFC 3339", "rule": {...}}
type interopSegment struct {
	GroupID string             `json:"group_id"`
	SetAt   time.Time          `json:"set_at"`
	Rule    models.SegmentRule `json:"rule"`
}

// SegmentsJSON reads the live segments in id order as a JSON array of
// [interopSegment].
func (s *Service) SegmentsJSON(ctx context.Context) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	groups, err := s.repo.ListSegments(ctx, models.MaxSegments)
	if err != nil {
		return nil, err
	}
	out := make([]interopSegment, 0, len(groups))
	for i := range groups {
		if groups[i].Segment == nil || groups[i].SegmentSetAt == nil {
			continue
		}
		out = append(out, interopSegment{
			GroupID: groups[i].ID, SetAt: *groups[i].SegmentSetAt, Rule: *groups[i].Segment,
		})
	}

	return json.Marshal(out)
}

// MaxSegmentFactsPage is the most customers one facts read returns.
const MaxSegmentFactsPage = 500

// interopSegmentFacts is one customer as a segment rule reads them:
//
//	{"customer_id": "cus_...", "has_account": true, "created_at": "RFC 3339", "country_code": "TR"}
type interopSegmentFacts struct {
	CustomerID  string    `json:"customer_id"`
	HasAccount  bool      `json:"has_account"`
	CreatedAt   time.Time `json:"created_at"`
	CountryCode string    `json:"country_code,omitempty"`
}

// SegmentFactsJSON pages the live customers after the given id, in id order,
// as a JSON array of [interopSegmentFacts].
func (s *Service) SegmentFactsJSON(ctx context.Context, afterCustomerID string, limit int) (json.RawMessage, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxSegmentFactsPage {
		return nil, errors.Invalid(CodeInvalidInput,
			"a page of segment facts holds between 1 and %d customers, %d asked", MaxSegmentFactsPage, limit)
	}
	facts, err := s.repo.ListSegmentFacts(ctx, afterCustomerID, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]interopSegmentFacts, 0, len(facts))
	for _, f := range facts {
		out = append(out, interopSegmentFacts{
			CustomerID: f.CustomerID, HasAccount: f.HasAccount, CreatedAt: f.CreatedAt, CountryCode: f.CountryCode,
		})
	}

	return json.Marshal(out)
}

// ApplySegmentPage writes a segment's members among the customer ids in
// (afterCustomerID, lastCustomerID] — to the end of the ids when
// lastCustomerID is empty: the given customers are in, every other member in
// the range is out. It writes nothing and says so when the segment's rule is no
// longer the one set at setAt, or the group is gone.
func (s *Service) ApplySegmentPage(
	ctx context.Context, groupID string, setAt time.Time, afterCustomerID, lastCustomerID string, members []string,
) (added, removed int, applied bool, err error) {
	if err := s.ready(); err != nil {
		return 0, 0, false, err
	}
	if err := requireID(groupID, models.CustomerGroupIDPrefix, "group id"); err != nil {
		return 0, 0, false, err
	}
	if lastCustomerID != "" && lastCustomerID <= afterCustomerID {
		return 0, 0, false, errors.Invalid(CodeInvalidInput,
			"a page of customers ends after it starts: %q is not after %q", lastCustomerID, afterCustomerID)
	}
	for _, id := range members {
		if id <= afterCustomerID || (lastCustomerID != "" && id > lastCustomerID) {
			return 0, 0, false, errors.Invalid(CodeInvalidInput,
				"member %q is outside the page (%q, %q]", id, afterCustomerID, lastCustomerID)
		}
	}
	a, r, applied, err := s.repo.ApplySegmentPage(ctx, groupID, setAt, afterCustomerID, lastCustomerID, members, s.clock())
	if err != nil {
		return 0, 0, false, err
	}
	return int(a), int(r), applied, nil
}

// FinishSegment records that a pass wrote the members of the rule set at setAt,
// only while it is still the segment's rule, and says whether it did.
func (s *Service) FinishSegment(ctx context.Context, groupID string, setAt, evaluatedAt time.Time) (bool, error) {
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.repo.FinishSegment(ctx, groupID, setAt, evaluatedAt)
}
