package service

import "context"

// AdminSurface is the customer module's panel surface (ADR 0322): a
// customer's membership of the groups. Only primitives cross it, as across
// every surface the panel resolves (ADR 0001); the panel reads the customers
// and the groups through the read layer.
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// service is the surface's service, nil for a surface that was never built,
// which the service's own readiness check refuses.
func (a *AdminSurface) service() *Service {
	if a == nil {
		return nil
	}

	return a.svc
}

// AddCustomerToGroup puts the customer into the group; a customer already in
// it is left there, as a second press of the same button should leave it.
func (a *AdminSurface) AddCustomerToGroup(ctx context.Context, customerID, groupID string) error {
	return a.service().AddToGroup(ctx, customerID, groupID)
}

// RemoveCustomerFromGroup takes the customer out of the group, and refuses
// when the customer is not in it.
func (a *AdminSurface) RemoveCustomerFromGroup(ctx context.Context, customerID, groupID string) error {
	return a.service().RemoveFromGroup(ctx, customerID, groupID)
}

// CreateGroup writes a customer group and returns its id (ADR 0323): the name
// is unique among live groups, and the rank is any whole number, the smaller
// ranking first (ADR 0049).
func (a *AdminSurface) CreateGroup(ctx context.Context, name string, rank int32) (string, error) {
	group, err := a.service().CreateGroup(ctx, GroupInput{Name: name, Rank: rank})
	if err != nil {
		return "", err
	}

	return group.ID, nil
}

// ReviseGroup renames and re-ranks the group from the name and rank the
// operator read, and refuses when another writer changed either since (ADR
// 0329).
func (a *AdminSurface) ReviseGroup(ctx context.Context, id, readName string, readRank int32, name string, rank int32) error {
	_, err := a.service().ReviseGroup(ctx, id, readName, readRank, name, rank)
	return err
}
