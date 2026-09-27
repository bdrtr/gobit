package service_test

import (
	"context"
	"slices"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// The attribute half of memStore (ADR 0219). It imitates queries/attribute.sql
// and the filter and facet SQL; the real statements are proven in the
// integration package.

// attributeState is created on first use so the other tests' constructor stays
// as it is.
type attributeState struct {
	attributes map[string]models.Attribute
	values     map[string][]repository.AttributeValueRow
}

// attrs returns the attribute state; the caller holds m.mu.
func (m *memStore) attrs() *attributeState {
	if m.attributeState == nil {
		m.attributeState = &attributeState{
			attributes: map[string]models.Attribute{}, values: map[string][]repository.AttributeValueRow{},
		}
	}
	return m.attributeState
}

func (m *memStore) CreateAttribute(_ context.Context, a models.Attribute, limit int) (models.Attribute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("CreateAttribute"); err != nil {
		return models.Attribute{}, err
	}
	state := m.attrs()
	if len(state.attributes) >= limit {
		return models.Attribute{}, errors.Conflict("product_attribute_limit_reached", "too many")
	}
	for id := range state.attributes {
		if state.attributes[id].Handle == a.Handle {
			return models.Attribute{}, errors.Conflict("product_handle_taken", "taken")
		}
	}
	state.attributes[a.ID] = a
	return a, nil
}

func (m *memStore) ListAttributes(_ context.Context, limit int) ([]models.Attribute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("ListAttributes"); err != nil {
		return nil, err
	}
	var out []models.Attribute
	for id := range m.attrs().attributes {
		out = append(out, m.attrs().attributes[id])
	}
	slices.SortFunc(out, func(a, b models.Attribute) int {
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return compareStrings(a.Handle, b.Handle)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	for i := range out {
		out[i].Options = sortedOptions(out[i].Options)
	}
	return out, nil
}

// sortedOptions orders options as queries/attribute.sql does: rank, then handle.
func sortedOptions(options []models.AttributeOption) []models.AttributeOption {
	out := slices.Clone(options)
	slices.SortFunc(out, func(a, b models.AttributeOption) int {
		if a.Rank != b.Rank {
			return int(a.Rank - b.Rank)
		}
		return compareStrings(a.Handle, b.Handle)
	})
	return out
}

func (m *memStore) GetAttribute(_ context.Context, id string) (models.Attribute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attrs().attributes[id]
	if !ok {
		return models.Attribute{}, errors.NotFound("product_not_found", "no attribute %s", id)
	}
	a.Options = sortedOptions(a.Options)
	return a, nil
}

func (m *memStore) UpdateAttribute(_ context.Context, id string, title *string, rank *int32) (models.Attribute, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attrs().attributes[id]
	if !ok {
		return models.Attribute{}, errors.NotFound("product_not_found", "no attribute %s", id)
	}
	if title != nil {
		a.Title = *title
	}
	if rank != nil {
		a.Rank = *rank
	}
	m.attrs().attributes[id] = a
	return a, nil
}

func (m *memStore) DeleteAttribute(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.attrs().attributes[id]; !ok {
		return errors.NotFound("product_not_found", "no attribute %s", id)
	}
	delete(m.attrs().attributes, id)
	return nil
}

func (m *memStore) AddAttributeOption(_ context.Context, o models.AttributeOption, limit int) (models.AttributeOption, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.attrs().attributes[o.AttributeID]
	if !ok {
		return models.AttributeOption{}, errors.NotFound("product_not_found", "no attribute %s", o.AttributeID)
	}
	if len(a.Options) >= limit {
		return models.AttributeOption{}, errors.Conflict("product_attribute_limit_reached", "too many")
	}
	a.Options = append(a.Options, o)
	m.attrs().attributes[a.ID] = a
	return o, nil
}

func (m *memStore) DeleteAttributeOption(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for aid := range m.attrs().attributes {
		a := m.attrs().attributes[aid]
		for i := range a.Options {
			if a.Options[i].ID == id {
				a.Options = slices.Delete(a.Options, i, i+1)
				m.attrs().attributes[aid] = a
				return nil
			}
		}
	}
	return errors.NotFound("product_not_found", "no option %s", id)
}

func (m *memStore) SetProductAttributeValues(_ context.Context, productID string, rows []repository.AttributeValueRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("SetProductAttributeValues"); err != nil {
		return err
	}
	m.attrs().values[productID] = slices.Clone(rows)
	return nil
}

func (m *memStore) ListProductAttributeValues(
	_ context.Context, productIDs []string,
) (map[string][]models.ProductAttributeValue, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string][]models.ProductAttributeValue{}
	state := m.attrs()
	for _, pid := range productIDs {
		for _, row := range state.values[pid] {
			a, ok := state.attributes[row.AttributeID]
			if !ok {
				continue
			}
			values := out[pid]
			if n := len(values); n == 0 || values[n-1].AttributeID != a.ID {
				values = append(values, models.ProductAttributeValue{
					AttributeID: a.ID, Handle: a.Handle, Title: a.Title, Kind: a.Kind,
				})
			}
			last := &values[len(values)-1]
			switch {
			case row.OptionID != nil:
				for _, o := range a.Options {
					if o.ID == *row.OptionID {
						last.Options = append(last.Options, o)
					}
				}
			case row.Number != nil:
				last.Number = row.Number
			default:
				last.Boolean = row.Boolean
			}
			out[pid] = values
		}
	}
	return out, nil
}

func (m *memStore) AttributeFacets(
	_ context.Context, f repository.ProductFilter, attributeIDs []string,
) ([]repository.FacetRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.track("AttributeFacets"); err != nil {
		return nil, err
	}
	type key struct {
		attribute, option string
		boolean           string
	}
	counts := map[key]*repository.FacetRow{}
	var order []key
	all := m.liveProducts()
	for i := range all {
		if !m.matches(&all[i], f) {
			continue
		}
		for _, row := range m.attrs().values[all[i].ID] {
			if !slices.Contains(attributeIDs, row.AttributeID) {
				continue
			}
			k := key{attribute: row.AttributeID}
			switch {
			case row.OptionID != nil:
				k.option = *row.OptionID
			case row.Boolean != nil:
				k.boolean = map[bool]string{true: "t", false: "f"}[*row.Boolean]
			}
			if counts[k] == nil {
				counts[k] = &repository.FacetRow{AttributeID: row.AttributeID, OptionID: row.OptionID, Boolean: row.Boolean}
				order = append(order, k)
			}
			c := counts[k]
			c.Products++
			if row.Number != nil {
				if c.Min == nil || *row.Number < *c.Min {
					c.Min = row.Number
				}
				if c.Max == nil || *row.Number > *c.Max {
					c.Max = row.Number
				}
			}
		}
	}
	out := make([]repository.FacetRow, 0, len(order))
	for _, k := range order {
		out = append(out, *counts[k])
	}
	return out, nil
}

// holdsAttributes is the fake counterpart of attributeFilterSQL: every filter
// has to be met by one of the product's value rows.
func (m *memStore) holdsAttributes(productID string, filters []repository.AttributeFilter) bool {
	rows := m.attrs().values[productID]
	for i := range filters {
		f := &filters[i]
		if !slices.ContainsFunc(rows, func(r repository.AttributeValueRow) bool {
			switch f.Kind {
			case models.AttributeSelect:
				return r.OptionID != nil && slices.Contains(f.OptionIDs, *r.OptionID)
			case models.AttributeBoolean:
				return r.AttributeID == f.AttributeID && r.Boolean != nil && *r.Boolean == *f.Boolean
			default:
				return r.AttributeID == f.AttributeID && r.Number != nil &&
					(f.Min == nil || *r.Number >= *f.Min) && (f.Max == nil || *r.Number <= *f.Max)
			}
		}) {
			return false
		}
	}
	return true
}

// compareStrings orders two strings.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
