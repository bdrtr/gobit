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
