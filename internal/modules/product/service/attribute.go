package service

import (
	"context"
	"math"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// prefixAttribute and prefixAttributeOption are the ids of the typed
// attributes (ADR 0219).
const (
	prefixAttribute       = "pattr_"
	prefixAttributeOption = "pattropt_"
)

// AttributeInput defines a store-wide attribute.
type AttributeInput struct {
	// Handle is how the storefront names it; derived from the title when empty.
	Handle string
	Title  string
	// Kind is models.AttributeNumber, AttributeBoolean or AttributeSelect.
	Kind string
	Rank int32
	// Options are a select attribute's choices; the other kinds take none.
	Options []AttributeOptionInput
}

// AttributeOptionInput is one choice of a select attribute.
type AttributeOptionInput struct {
	// Handle is how the storefront names it; derived from the value when empty.
	Handle string
	Value  string
	Rank   int32
}

// AttributePatch changes a definition's title or order; nil keeps either.
type AttributePatch struct {
	Title *string
	Rank  *int32
}

// ProductAttributeInput is a product's value of one attribute, named by its
// handle: the options of a select attribute by their handles, a number, or a
// boolean.
type ProductAttributeInput struct {
	Attribute string
	Options   []string
	Number    *float64
	Boolean   *bool
}

// AttributeCriterion keeps the products whose value of one attribute matches:
// any of the options of a select attribute, a number within the bounds (each
// inclusive, nil open), or the boolean.
type AttributeCriterion struct {
	Attribute string
	Options   []string
	Min, Max  *float64
	Boolean   *bool
}

// Facet is how many of a listing's products hold each value of one attribute.
//
// A select attribute counts its options, a boolean its two values, and a
// number the products holding one with their smallest and largest. An
// attribute the listing filters on is counted without its own filter, so a
// shopper sees what the other options would give; the rest are counted within
// every filter.
type Facet struct {
	Handle   string        `json:"handle"`
	Title    string        `json:"title"`
	Kind     string        `json:"kind"`
	Options  []FacetOption `json:"options,omitempty"`
	True     int64         `json:"true,omitempty"`
	False    int64         `json:"false,omitempty"`
	Products int64         `json:"products"`
	Min      *float64      `json:"min,omitempty"`
	Max      *float64      `json:"max,omitempty"`
}

// FacetOption is one option of a select facet and how many products hold it.
type FacetOption struct {
	Handle   string `json:"handle"`
	Value    string `json:"value"`
	Products int64  `json:"products"`
}

// CreateAttribute defines a store-wide attribute, with its options when it is
// a select, while fewer than models.MaxAttributes stand.
func (s *Service) CreateAttribute(ctx context.Context, in AttributeInput) (models.Attribute, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" || len(title) > models.MaxAttributeTitleLen {
		return models.Attribute{}, invalid("an attribute's title is 1 to %d bytes", models.MaxAttributeTitleLen)
	}
	handle, err := attributeHandle(in.Handle, title)
	if err != nil {
		return models.Attribute{}, err
	}
	switch in.Kind {
	case models.AttributeNumber, models.AttributeBoolean:
		if len(in.Options) > 0 {
			return models.Attribute{}, invalid("a %s attribute takes no options", in.Kind)
		}
	case models.AttributeSelect:
		if len(in.Options) > models.MaxAttributeOptions {
			return models.Attribute{}, invalid("a select attribute has at most %d options", models.MaxAttributeOptions)
		}
	default:
		return models.Attribute{}, invalid("an attribute's kind is number, boolean or select, not %q", in.Kind)
	}

	a := models.Attribute{ID: newID(prefixAttribute), Handle: handle, Title: title, Kind: in.Kind, Rank: in.Rank}
	seen := map[string]bool{}
	for _, o := range in.Options {
		option, err := newAttributeOption(a.ID, o)
		if err != nil {
			return models.Attribute{}, err
		}
		if seen[option.Handle] {
			return models.Attribute{}, invalid("the option handle %q is given twice", option.Handle)
		}
		seen[option.Handle] = true
		a.Options = append(a.Options, option)
	}

	return s.repo.CreateAttribute(ctx, a, models.MaxAttributes)
}

// ListAttributes returns the store-wide attributes with their options, in the
// operator's order.
func (s *Service) ListAttributes(ctx context.Context) ([]models.Attribute, error) {
	return s.repo.ListAttributes(ctx, models.MaxAttributes)
}

// UpdateAttribute changes a definition's title or order; its handle and kind
// do not change, since every value and filter was written against them.
func (s *Service) UpdateAttribute(ctx context.Context, id string, patch AttributePatch) (models.Attribute, error) {
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		if title == "" || len(title) > models.MaxAttributeTitleLen {
			return models.Attribute{}, invalid("an attribute's title is 1 to %d bytes", models.MaxAttributeTitleLen)
		}
		patch.Title = &title
	}
	return s.repo.UpdateAttribute(ctx, id, patch.Title, patch.Rank)
}

// DeleteAttribute removes a definition; the products holding it no longer
// carry it, and a filter naming it is refused.
func (s *Service) DeleteAttribute(ctx context.Context, id string) error {
	return s.repo.DeleteAttribute(ctx, id)
}

// AddAttributeOption adds a choice to a select attribute.
func (s *Service) AddAttributeOption(
	ctx context.Context, attributeID string, in AttributeOptionInput,
) (models.AttributeOption, error) {
	attribute, err := s.repo.GetAttribute(ctx, attributeID)
	if err != nil {
		return models.AttributeOption{}, err
	}
	if attribute.Kind != models.AttributeSelect {
		return models.AttributeOption{}, invalid("attribute %s is a %s attribute and takes no options",
			attribute.Handle, attribute.Kind)
	}
	option, err := newAttributeOption(attributeID, in)
	if err != nil {
		return models.AttributeOption{}, err
	}
	return s.repo.AddAttributeOption(ctx, option, models.MaxAttributeOptions)
}

// DeleteAttributeOption removes a choice; the products that chose it no longer
// carry it.
func (s *Service) DeleteAttributeOption(ctx context.Context, id string) error {
	return s.repo.DeleteAttributeOption(ctx, id)
}

// SetProductAttributes replaces a product's attribute values and returns them.
//
// Each value names its attribute by handle and matches its kind: the options
// of a select attribute by their handles, one number, or one boolean. An
// attribute named twice, an option the attribute does not have and a value of
// the wrong kind are refused, and nothing is written.
func (s *Service) SetProductAttributes(
	ctx context.Context, productID string, values []ProductAttributeInput,
) ([]models.ProductAttributeValue, error) {
	if _, err := s.repo.GetProduct(ctx, productID); err != nil {
		return nil, err
	}
	attributes, err := s.attributesByHandle(ctx)
	if err != nil {
		return nil, err
	}
	var rows []repository.AttributeValueRow
	named := map[string]bool{}
	for _, v := range values {
		attribute, ok := attributes[strings.TrimSpace(v.Attribute)]
		if !ok {
			return nil, invalid("there is no attribute %q", v.Attribute)
		}
		if named[attribute.ID] {
			return nil, invalid("attribute %q is given twice", attribute.Handle)
		}
		named[attribute.ID] = true
		valueRows, err := attributeValueRows(&attribute, v)
		if err != nil {
			return nil, err
		}
		rows = append(rows, valueRows...)
	}
	if err := s.repo.SetProductAttributeValues(ctx, productID, rows); err != nil {
		return nil, err
	}
	stored, err := s.repo.ListProductAttributeValues(ctx, []string{productID})
	if err != nil {
		return nil, err
	}
	return stored[productID], nil
}

// attributeValueRows checks one value against its attribute's kind.
func attributeValueRows(attribute *models.Attribute, v ProductAttributeInput) ([]repository.AttributeValueRow, error) {
	switch attribute.Kind {
	case models.AttributeSelect:
		if v.Number != nil || v.Boolean != nil || len(v.Options) == 0 {
			return nil, invalid("attribute %q takes one or more of its options", attribute.Handle)
		}
		var rows []repository.AttributeValueRow
		for _, handle := range v.Options {
			id, ok := optionID(attribute, strings.TrimSpace(handle))
			if !ok {
				return nil, invalid("attribute %q has no option %q", attribute.Handle, handle)
			}
			if slices.ContainsFunc(rows, func(r repository.AttributeValueRow) bool { return *r.OptionID == id }) {
				continue
			}
			rows = append(rows, repository.AttributeValueRow{AttributeID: attribute.ID, OptionID: &id})
		}
		return rows, nil
	case models.AttributeNumber:
		if v.Number == nil || v.Boolean != nil || len(v.Options) > 0 {
			return nil, invalid("attribute %q takes one number", attribute.Handle)
		}
		if math.IsNaN(*v.Number) || math.IsInf(*v.Number, 0) {
			return nil, invalid("attribute %q takes a finite number", attribute.Handle)
		}
		return []repository.AttributeValueRow{{AttributeID: attribute.ID, Number: v.Number}}, nil
	default:
		if v.Boolean == nil || v.Number != nil || len(v.Options) > 0 {
			return nil, invalid("attribute %q takes true or false", attribute.Handle)
		}
		return []repository.AttributeValueRow{{AttributeID: attribute.ID, Boolean: v.Boolean}}, nil
	}
}

// resolveAttributeCriteria turns criteria naming handles into the
// repository's filters, refusing a handle, an option or a value the catalog
// does not have: a filter that could match nothing is a mistake the caller
// should hear about, not an empty page.
func (s *Service) resolveAttributeCriteria(
	ctx context.Context, criteria []AttributeCriterion,
) ([]repository.AttributeFilter, error) {
	if len(criteria) == 0 {
		return nil, nil
	}
	if len(criteria) > models.MaxAttributeFilters {
		return nil, invalid("a listing filters on at most %d attributes, %d given", models.MaxAttributeFilters, len(criteria))
	}
	attributes, err := s.attributesByHandle(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]repository.AttributeFilter, 0, len(criteria))
	for _, c := range criteria {
		attribute, ok := attributes[strings.TrimSpace(c.Attribute)]
		if !ok {
			return nil, invalid("there is no attribute %q to filter on", c.Attribute)
		}
		if slices.ContainsFunc(out, func(f repository.AttributeFilter) bool { return f.AttributeID == attribute.ID }) {
			return nil, invalid("attribute %q is filtered twice; list its values in one filter", attribute.Handle)
		}
		filter := repository.AttributeFilter{AttributeID: attribute.ID, Kind: attribute.Kind}
		switch attribute.Kind {
		case models.AttributeSelect:
			if len(c.Options) == 0 || c.Min != nil || c.Max != nil || c.Boolean != nil {
				return nil, invalid("attribute %q is filtered by one or more of its options", attribute.Handle)
			}
			for _, handle := range c.Options {
				id, ok := optionID(&attribute, strings.TrimSpace(handle))
				if !ok {
					return nil, invalid("attribute %q has no option %q", attribute.Handle, handle)
				}
				filter.OptionIDs = append(filter.OptionIDs, id)
			}
		case models.AttributeNumber:
			if len(c.Options) > 0 || c.Boolean != nil {
				return nil, invalid("attribute %q is filtered by a range", attribute.Handle)
			}
			for _, bound := range []*float64{c.Min, c.Max} {
				if bound != nil && (math.IsNaN(*bound) || math.IsInf(*bound, 0)) {
					return nil, invalid("the range of attribute %q takes finite numbers", attribute.Handle)
				}
			}
			if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
				return nil, invalid("the range of attribute %q ends before it starts", attribute.Handle)
			}
			filter.Min, filter.Max = c.Min, c.Max
		default:
			// A query string cannot say which kind a value is, so a boolean may
			// arrive as the one option "true" or "false"; GraphQL sends it typed.
			if c.Boolean == nil && len(c.Options) == 1 && (c.Options[0] == "true" || c.Options[0] == "false") {
				value := c.Options[0] == "true"
				c.Boolean, c.Options = &value, nil
			}
			if c.Boolean == nil || len(c.Options) > 0 || c.Min != nil || c.Max != nil {
				return nil, invalid("attribute %q is filtered by true or false", attribute.Handle)
			}
			filter.Boolean = c.Boolean
		}
		out = append(out, filter)
	}
	return out, nil
}

// attributesByHandle reads the live attributes keyed by handle.
func (s *Service) attributesByHandle(ctx context.Context) (map[string]models.Attribute, error) {
	list, err := s.repo.ListAttributes(ctx, models.MaxAttributes)
	if err != nil {
		return nil, err
	}
	out := make(map[string]models.Attribute, len(list))
	for i := range list {
		out[list[i].Handle] = list[i]
	}
	return out, nil
}

// StoreFacets counts the published products a storefront listing with these
// criteria would hold, per value of every attribute (ADR 0219).
//
// The criteria are the listing's own, sales channel included; an attribute the
// criteria filter on is counted without its own filter, one query per such
// attribute, and every other attribute in one query within every filter. The
// in-stock and price filters are answered after the catalog is enriched, so a
// facet request cannot carry them.
func (s *Service) StoreFacets(ctx context.Context, opts StoreListOptions) ([]Facet, error) {
	if opts.InStock != nil || opts.Price != nil {
		return nil, invalid("facets are counted over the catalog's own filters; in_stock and the price are not among them")
	}
	attributes, err := s.repo.ListAttributes(ctx, models.MaxAttributes)
	if err != nil {
		return nil, err
	}
	resolved, err := s.resolveAttributeCriteria(ctx, opts.Attributes)
	if err != nil {
		return nil, err
	}
	published := models.StatusPublished.String()
	base := repository.ProductFilter{
		Status: &published, CollectionID: opts.CollectionID, CategoryID: opts.CategoryID, TagID: opts.TagID,
		VariantIDs: opts.VariantIDs, Search: opts.Search, SalesChannelIDs: opts.SalesChannelIDs,
		Attributes: resolved,
	}
	if opts.OptionValue != nil {
		folded := models.FoldOptionValue(*opts.OptionValue)
		base.OptionValueFolded = &folded
	}

	var rows []repository.FacetRow
	filtered := map[string]bool{}
	for i := range resolved {
		id := resolved[i].AttributeID
		filtered[id] = true
		others := base
		others.Attributes = slices.Delete(slices.Clone(resolved), i, i+1)
		counted, err := s.repo.AttributeFacets(ctx, others, []string{id})
		if err != nil {
			return nil, err
		}
		rows = append(rows, counted...)
	}
	var rest []string
	for i := range attributes {
		if !filtered[attributes[i].ID] {
			rest = append(rest, attributes[i].ID)
		}
	}
	if len(rest) > 0 {
		counted, err := s.repo.AttributeFacets(ctx, base, rest)
		if err != nil {
			return nil, err
		}
		rows = append(rows, counted...)
	}

	return buildFacets(attributes, rows), nil
}

// buildFacets lays the counted rows out per attribute in the operator's order,
// every option listed, a zero where no product holds it.
func buildFacets(attributes []models.Attribute, rows []repository.FacetRow) []Facet {
	out := make([]Facet, 0, len(attributes))
	for i := range attributes {
		a := &attributes[i]
		facet := Facet{Handle: a.Handle, Title: a.Title, Kind: a.Kind}
		counts := map[string]int64{}
		for j := range rows {
			row := &rows[j]
			if row.AttributeID != a.ID {
				continue
			}
			switch {
			case row.OptionID != nil:
				counts[*row.OptionID] = row.Products
				facet.Products += row.Products
			case row.Boolean != nil && *row.Boolean:
				facet.True = row.Products
				facet.Products += row.Products
			case row.Boolean != nil:
				facet.False = row.Products
				facet.Products += row.Products
			default:
				facet.Products, facet.Min, facet.Max = row.Products, row.Min, row.Max
			}
		}
		for _, o := range a.Options {
			facet.Options = append(facet.Options, FacetOption{Handle: o.Handle, Value: o.Value, Products: counts[o.ID]})
		}
		out = append(out, facet)
	}
	return out
}

// newAttributeOption builds and checks one option.
func newAttributeOption(attributeID string, in AttributeOptionInput) (models.AttributeOption, error) {
	value := strings.TrimSpace(in.Value)
	if value == "" || len(value) > models.MaxAttributeTitleLen {
		return models.AttributeOption{}, invalid("an option's value is 1 to %d bytes", models.MaxAttributeTitleLen)
	}
	handle, err := attributeHandle(in.Handle, value)
	if err != nil {
		return models.AttributeOption{}, err
	}
	return models.AttributeOption{
		ID: newID(prefixAttributeOption), AttributeID: attributeID, Handle: handle, Value: value, Rank: in.Rank,
	}, nil
}

// attributeHandle resolves a handle as a product's is resolved, within the
// attributes' shorter bound.
func attributeHandle(handle, from string) (string, error) {
	resolved, err := resolveHandle(handle, from)
	if err != nil {
		return "", err
	}
	if len(resolved) > models.MaxAttributeHandleLen || !models.AttributeHandlePattern.MatchString(resolved) {
		return "", invalid("a handle is at most %d lowercase letters, digits and single hyphens, not %q",
			models.MaxAttributeHandleLen, resolved)
	}
	return resolved, nil
}

// optionID finds a live option of an attribute by handle.
func optionID(attribute *models.Attribute, handle string) (string, bool) {
	for _, o := range attribute.Options {
		if o.Handle == handle {
			return o.ID, true
		}
	}
	return "", false
}
