package service

import (
	"context"
	"encoding/json"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// panelChannel is a sales channel's terms as the panel reads and writes
// them; the json tags are the contract with the panel, which cannot import
// this package, and spell the fields the channel provider publishes.
type panelChannel struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	IsDisabled  bool   `json:"is_disabled"`
}

// ReviseSalesChannel corrects the channel from the terms the operator read,
// both as JSON, and refuses when another writer changed them since (ADR
// 0352).
func (s *AccountSurface) ReviseSalesChannel(ctx context.Context, id string, read, next json.RawMessage) error {
	var was, will panelChannel
	if err := json.Unmarshal(read, &was); err != nil {
		return errors.Invalid(CodeInvalidInput, "the channel read could not be read: %v", err)
	}
	if err := json.Unmarshal(next, &will); err != nil {
		return errors.Invalid(CodeInvalidInput, "the channel written could not be read: %v", err)
	}
	_, err := s.svc.ReviseSalesChannel(ctx, id,
		models.ChannelTerms(was), models.ChannelTerms(will))

	return err
}

// MakeSalesChannel makes a sales channel with the name, the description and
// whether it starts disabled, and returns its id (ADR 0353).
func (s *AccountSurface) MakeSalesChannel(ctx context.Context, name, description string, disabled bool) (string, error) {
	channel, err := s.svc.CreateSalesChannel(ctx, SalesChannelInput{
		Name: name, Description: description, IsDisabled: disabled,
	})
	if err != nil {
		return "", err
	}

	return channel.ID, nil
}
