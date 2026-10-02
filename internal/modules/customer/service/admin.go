package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

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

// adminContact is a customer's name and phone as the panel reads and writes
// them; the json tags are the contract with the panel, which cannot import
// this package.
type adminContact struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Phone     string `json:"phone"`
}

// ReviseCustomerContact corrects the customer's name and phone from the ones
// the operator read, and refuses when another writer changed any of them
// since (ADR 0337). Both travel as JSON, their fields named rather than
// placed.
func (a *AdminSurface) ReviseCustomerContact(ctx context.Context, id string, read, next json.RawMessage) error {
	var was, will adminContact
	if err := json.Unmarshal(read, &was); err != nil {
		return errors.Invalid(CodeInvalidInput, "the contact read could not be read: %v", err)
	}
	if err := json.Unmarshal(next, &will); err != nil {
		return errors.Invalid(CodeInvalidInput, "the contact written could not be read: %v", err)
	}
	_, err := a.service().ReviseContact(ctx, id,
		models.ContactTerms{FirstName: was.FirstName, LastName: was.LastName, Phone: was.Phone},
		models.ContactTerms{FirstName: will.FirstName, LastName: will.LastName, Phone: will.Phone})

	return err
}

// adminAddress is an address's printed fields as the panel reads and writes
// them; the json tags are the contract with the panel, which cannot import
// this package, and spell the address keys the customer provider publishes.
type adminAddress struct {
	// The printed fields of an address; Province among them is the sub-country
	// unit, an il in Turkey, and not the district (ADR 0067).
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Company     string `json:"company"`
	Address1    string `json:"address_1"`
	Address2    string `json:"address_2"`
	City        string `json:"city"`
	Province    string `json:"province"`
	CountryCode string `json:"country_code"`
	PostalCode  string `json:"postal_code"`
	Phone       string `json:"phone"`
}

// terms are the address's printed fields.
func (a adminAddress) terms() models.AddressTerms {
	return models.AddressTerms{
		FirstName: a.FirstName, LastName: a.LastName, Company: a.Company, Address1: a.Address1,
		Address2: a.Address2, City: a.City, Province: a.Province, CountryCode: a.CountryCode,
		PostalCode: a.PostalCode, Phone: a.Phone,
	}
}

// ReviseCustomerAddress corrects the customer's address from the printed
// fields the operator read, and refuses when another writer changed any of
// them since (ADR 0342). Both travel as JSON, their fields named rather than
// placed.
func (a *AdminSurface) ReviseCustomerAddress(
	ctx context.Context, customerID, addressID string, read, next json.RawMessage,
) error {
	var was, will adminAddress
	if err := json.Unmarshal(read, &was); err != nil {
		return errors.Invalid(CodeInvalidInput, "the address read could not be read: %v", err)
	}
	if err := json.Unmarshal(next, &will); err != nil {
		return errors.Invalid(CodeInvalidInput, "the address written could not be read: %v", err)
	}
	_, err := a.service().ReviseAddress(ctx, customerID, addressID, was.terms(), will.terms())

	return err
}
