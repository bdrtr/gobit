package service

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/repository"
)

// The segment half of memRepo imitates queries/customer_group.sql and
// customer.sql; the real SQL is proven in segment_integration_test.go.

func (m *memRepo) SetGroupSegment(
	_ context.Context, id string, rule models.SegmentRule, limit int64, now time.Time,
) (models.CustomerGroup, error) {
	m.record("SetGroupSegment")
	var others int64
	for gid := range m.groups {
		if g := m.groups[gid]; gid != id && g.DeletedAt == nil && g.Segment != nil {
			others++
		}
	}
	if others >= limit {
		return models.CustomerGroup{}, errors.Conflict(models.CodeSegmentLimit, "%d segments", others)
	}
	g, ok := m.liveGroup(id)
	if !ok {
		return models.CustomerGroup{}, errors.NotFound(repository.CodeGroupNotFound, "no group %s", id)
	}
	g.Segment, g.SegmentSetAt, g.SegmentEvaluatedAt, g.UpdatedAt = &rule, &now, nil, now
	m.groups[id] = g

	return g, nil
}

func (m *memRepo) ClearGroupSegment(_ context.Context, id string, now time.Time) (models.CustomerGroup, error) {
	m.record("ClearGroupSegment")
	g, ok := m.liveGroup(id)
	if !ok {
		return models.CustomerGroup{}, errors.NotFound(repository.CodeGroupNotFound, "no group %s", id)
	}
	g.Segment, g.SegmentSetAt, g.SegmentEvaluatedAt, g.UpdatedAt = nil, nil, nil, now
	m.groups[id] = g

	return g, nil
}

func (m *memRepo) ListSegments(_ context.Context, limit int32) ([]models.CustomerGroup, error) {
	m.record("ListSegments")
	var out []models.CustomerGroup
	for id := range m.groups {
		if g := m.groups[id]; g.DeletedAt == nil && g.Segment != nil {
			out = append(out, g)
		}
	}
	slices.SortFunc(out, func(a, b models.CustomerGroup) int { return cmpString(a.ID, b.ID) })
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (m *memRepo) ListSegmentFacts(_ context.Context, afterID string, limit int32) ([]models.SegmentFacts, error) {
	m.record("ListSegmentFacts")
	var out []models.SegmentFacts
	for id := range m.customers {
		c := m.customers[id]
		if c.DeletedAt != nil || id <= afterID {
			continue
		}
		facts := models.SegmentFacts{CustomerID: id, HasAccount: c.HasAccount, CreatedAt: c.CreatedAt}
		for aid := range m.addresses {
			if a := m.addresses[aid]; a.CustomerID == id && a.IsDefaultShipping && a.DeletedAt == nil {
				facts.CountryCode = a.CountryCode
			}
		}
		out = append(out, facts)
	}
	slices.SortFunc(out, func(a, b models.SegmentFacts) int { return cmpString(a.CustomerID, b.CustomerID) })
	if len(out) > int(limit) {
		out = out[:limit]
	}
	return out, nil
}

func (m *memRepo) ApplySegmentPage(
	_ context.Context, groupID string, setAt time.Time, afterID, lastID string, members []string, _ time.Time,
) (added, removed int64, applied bool, err error) {
	m.record("ApplySegmentPage")
	g, ok := m.liveGroup(groupID)
	if !ok || g.Segment == nil || g.SegmentSetAt == nil || !g.SegmentSetAt.Equal(setAt) {
		return 0, 0, false, nil
	}
	for customerID, groups := range m.members {
		inRange := customerID > afterID && (lastID == "" || customerID <= lastID)
		if groups[groupID] && inRange && !slices.Contains(members, customerID) {
			delete(groups, groupID)
			removed++
		}
	}
	for _, customerID := range members {
		if _, live := m.liveCustomer(customerID); !live || m.members[customerID][groupID] {
			continue
		}
		if m.members[customerID] == nil {
			m.members[customerID] = map[string]bool{}
		}
		m.members[customerID][groupID] = true
		added++
	}

	return added, removed, true, nil
}

func (m *memRepo) FinishSegment(_ context.Context, groupID string, setAt, evaluatedAt time.Time) (bool, error) {
	m.record("FinishSegment")
	g, ok := m.liveGroup(groupID)
	if !ok || g.SegmentSetAt == nil || !g.SegmentSetAt.Equal(setAt) {
		return false, nil
	}
	g.SegmentEvaluatedAt = &evaluatedAt
	m.groups[groupID] = g

	return true, nil
}
