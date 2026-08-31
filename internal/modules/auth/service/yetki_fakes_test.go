package service_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// sahteDepo [service.Repository]'nin yetki testleri için yazılmış uygulamasıdır.
//
// Depo veriyi SAKLAMAZ, yalnızca kendisine ne yazıldığını hatırlar. Bu
// dosyanın testleri servisin yetki kapısını sınar; kapı kapalıyken depoya
// hiçbir şey ulaşmamalı, açıkken çözülmüş yetki listesi olduğu gibi geçmelidir.
// Gerçek bir bellek içi depo, bu iki iddiaya hiçbir şey katmadan testi
// okunmaz hâle getirirdi.
type sahteDepo struct {
	// yazmaSayisi depoya inen yazma çağrılarının sayısıdır.
	yazmaSayisi int
	// sonAnahtar depoya son yazılan API anahtarıdır.
	sonAnahtar models.APIKey
	// sonKullanici depoya son yazılan kullanıcıdır.
	sonKullanici models.User
	// sonYama kullanıcıya son uygulanan kısmi güncellemedir.
	sonYama models.UserPatch
}

var _ service.Repository = (*sahteDepo)(nil)

func (d *sahteDepo) CreateUser(
	_ context.Context,
	u models.User,
	_ *models.AuthIdentity,
) (models.User, error) {
	d.yazmaSayisi++
	d.sonKullanici = u
	return u, nil
}

func (d *sahteDepo) GetUser(_ context.Context, id string) (models.User, error) {
	return models.User{ID: id}, nil
}

func (d *sahteDepo) GetUserByEmail(_ context.Context, _ string) (models.User, error) {
	return models.User{}, errors.NotFound("user_not_found", "kullanıcı bulunamadı")
}

func (d *sahteDepo) ListUsers(
	_ context.Context,
	_ models.UserFilter,
	_, _ int64,
) ([]models.User, int64, error) {
	return nil, 0, nil
}

func (d *sahteDepo) GetUsersByIDs(_ context.Context, _ []string) ([]models.User, error) {
	return nil, nil
}

func (d *sahteDepo) UpdateUser(
	_ context.Context,
	id string,
	patch models.UserPatch,
	_ time.Time,
) (models.User, error) {
	d.yazmaSayisi++
	d.sonYama = patch
	return models.User{ID: id, Scopes: patch.Scopes}, nil
}

func (d *sahteDepo) DeleteUser(_ context.Context, _ string, _ time.Time) error {
	d.yazmaSayisi++
	return nil
}

func (d *sahteDepo) GetIdentity(_ context.Context, _, _ string) (models.AuthIdentity, error) {
	return models.AuthIdentity{}, errors.NotFound("identity_not_found", "kimlik bulunamadı")
}

func (d *sahteDepo) SetPasswordHash(
	_ context.Context,
	_, _, _, _ string,
	_ time.Time,
) (models.AuthIdentity, error) {
	d.yazmaSayisi++
	return models.AuthIdentity{}, nil
}

func (d *sahteDepo) SessionAnchor(_ context.Context, _ string) (time.Time, error) {
	return time.Time{}, errors.NotFound("identity_not_found", "kimlik bulunamadı")
}

func (d *sahteDepo) RevokeSessions(
	_ context.Context,
	_ string,
	now time.Time,
) ([]models.AuthIdentity, error) {
	d.yazmaSayisi++
	return []models.AuthIdentity{{UpdatedAt: now}}, nil
}

func (d *sahteDepo) RegisterLoginFailure(
	_ context.Context,
	_ string,
	_ int,
	_, _ time.Time,
) (models.AuthIdentity, error) {
	return models.AuthIdentity{}, nil
}

func (d *sahteDepo) RegisterLoginSuccess(_ context.Context, _ string, _ time.Time) error {
	return nil
}

func (d *sahteDepo) CreateAPIKey(_ context.Context, k models.APIKey) (models.APIKey, error) {
	d.yazmaSayisi++
	d.sonAnahtar = k
	return k, nil
}

func (d *sahteDepo) GetAPIKey(_ context.Context, id string) (models.APIKey, error) {
	return models.APIKey{ID: id, Type: models.APIKeyPublishable}, nil
}

func (d *sahteDepo) GetAPIKeyByHash(_ context.Context, _ string) (models.APIKey, error) {
	return models.APIKey{}, errors.NotFound("api_key_not_found", "api anahtarı bulunamadı")
}

func (d *sahteDepo) ListAPIKeys(
	_ context.Context,
	_ models.APIKeyFilter,
	_, _ int64,
) ([]models.APIKey, int64, error) {
	return nil, 0, nil
}

func (d *sahteDepo) RevokeAPIKey(
	_ context.Context,
	id, _ string,
	_ time.Time,
) (models.APIKey, error) {
	d.yazmaSayisi++
	return models.APIKey{ID: id}, nil
}

func (d *sahteDepo) DeleteAPIKey(_ context.Context, _ string, _ time.Time) error {
	d.yazmaSayisi++
	return nil
}

func (d *sahteDepo) MarkAPIKeyUsed(_ context.Context, _ string, _, _ time.Time) error {
	return nil
}

func (d *sahteDepo) LinkSalesChannel(_ context.Context, _, _ string, _ time.Time) error {
	d.yazmaSayisi++
	return nil
}

func (d *sahteDepo) UnlinkSalesChannel(_ context.Context, _, _ string) error {
	d.yazmaSayisi++
	return nil
}

func (d *sahteDepo) ChannelIDsOfKey(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}

func (d *sahteDepo) ChannelsOfKey(_ context.Context, _ string) ([]models.SalesChannel, error) {
	return nil, nil
}

func (d *sahteDepo) CreateSalesChannel(
	_ context.Context,
	c models.SalesChannel,
) (models.SalesChannel, error) {
	d.yazmaSayisi++
	return c, nil
}

func (d *sahteDepo) GetSalesChannel(_ context.Context, id string) (models.SalesChannel, error) {
	return models.SalesChannel{ID: id}, nil
}

func (d *sahteDepo) ListSalesChannels(
	_ context.Context,
	_ models.SalesChannelFilter,
	_, _ int64,
) ([]models.SalesChannel, int64, error) {
	return nil, 0, nil
}

func (d *sahteDepo) GetSalesChannelsByIDs(
	_ context.Context,
	_ []string,
) ([]models.SalesChannel, error) {
	return nil, nil
}

func (d *sahteDepo) UpdateSalesChannel(
	_ context.Context,
	id string,
	_ models.SalesChannelPatch,
	_ time.Time,
) (models.SalesChannel, error) {
	d.yazmaSayisi++
	return models.SalesChannel{ID: id}, nil
}

func (d *sahteDepo) DeleteSalesChannel(_ context.Context, _ string, _ time.Time) error {
	d.yazmaSayisi++
	return nil
}
