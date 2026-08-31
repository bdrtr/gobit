package api_test

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/internal/modules/auth/api"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// sahteAuth [api.Auth] yüzeyinin testlerdeki uygulamasıdır.
//
// Sahte, İŞ MANTIĞINI taklit ETMEZ ve etmemelidir: bu dosyanın testleri yetki
// katmanını sınar, yani handler'a HİÇ ULAŞILMAMASI gereken durumları. Her
// çağrı sayılır ve testler "403 dönmüş ama servis yine de çalışmış" hâlini bu
// sayaçla yakalar — yalnızca status koduna bakan bir test, hatayı yazma
// yapıldıktan sonra döndüren bir handler'ı fark edemezdi.
type sahteAuth struct {
	// cagriSayisi servise ulaşan çağrıların sayısıdır.
	cagriSayisi int
	// sonCikisKimligi çıkış ucunun servise geçirdiği kimliktir.
	sonCikisKimligi string
	// sonCikisTuru çıkış ucunun servise geçirdiği kimlik TÜRÜDÜR.
	//
	// Alan ayrı tutulur çünkü "api anahtarı çıkış yapamaz" kararını servis
	// verir ve o kararı verebilmesi için türü GÖRMESİ gerekir; handler türü
	// geçirmezse servis her çağıranı kullanıcı sanardı.
	sonCikisTuru string
	// cikisHatasi doluysa Logout bu hatayı döner.
	cikisHatasi error
}

var _ api.Auth = (*sahteAuth)(nil)

// cikisAni sahtenin çıkış ucundan döndüğü sabit iptal anıdır.
//
// Sabit olması bilinçlidir: yanıt gövdesinin bu değeri OLDUĞU GİBİ taşıdığı
// ancak bilinen bir anla doğrulanabilir.
var cikisAni = time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

// gecti bir servis çağrısını sayar.
func (f *sahteAuth) gecti() { f.cagriSayisi++ }

func (f *sahteAuth) Login(_ context.Context, _, _ string) (string, time.Time, error) {
	f.gecti()
	return "jeton", time.Unix(0, 0).UTC(), nil
}

func (f *sahteAuth) Logout(_ context.Context, principalID, principalKind string) (time.Time, error) {
	f.gecti()
	f.sonCikisKimligi = principalID
	f.sonCikisTuru = principalKind
	if f.cikisHatasi != nil {
		return time.Time{}, f.cikisHatasi
	}
	return cikisAni, nil
}

func (f *sahteAuth) CreateUser(_ context.Context, _ service.CreateUserInput, _ string) (models.User, error) {
	f.gecti()
	return models.User{ID: "usr_1"}, nil
}

func (f *sahteAuth) GetUser(_ context.Context, id string) (models.User, error) {
	f.gecti()
	return models.User{ID: id}, nil
}

func (f *sahteAuth) ListUsers(_ context.Context, _ service.ListUsersInput) (service.Page[models.User], error) {
	f.gecti()
	return service.Page[models.User]{}, nil
}

func (f *sahteAuth) UpdateUser(_ context.Context, id string, _ service.UpdateUserInput) (models.User, error) {
	f.gecti()
	return models.User{ID: id}, nil
}

func (f *sahteAuth) DeleteUser(_ context.Context, _ string) error {
	f.gecti()
	return nil
}

func (f *sahteAuth) SetPassword(_ context.Context, _, _ string) error {
	f.gecti()
	return nil
}

func (f *sahteAuth) CreateAPIKey(_ context.Context, _ service.CreateAPIKeyInput) (models.APIKey, string, error) {
	f.gecti()
	return models.APIKey{ID: "apk_1", Type: models.APIKeySecret}, "sk_duz", nil
}

func (f *sahteAuth) GetAPIKey(_ context.Context, id string) (models.APIKey, error) {
	f.gecti()
	return models.APIKey{ID: id, Type: models.APIKeySecret}, nil
}

func (f *sahteAuth) ListAPIKeys(_ context.Context, _ service.ListAPIKeysInput) (service.Page[models.APIKey], error) {
	f.gecti()
	return service.Page[models.APIKey]{}, nil
}

func (f *sahteAuth) RevokeAPIKey(_ context.Context, id, _ string) (models.APIKey, error) {
	f.gecti()
	return models.APIKey{ID: id, Type: models.APIKeySecret}, nil
}

func (f *sahteAuth) DeleteAPIKey(_ context.Context, _ string) error {
	f.gecti()
	return nil
}

func (f *sahteAuth) LinkSalesChannel(_ context.Context, _, _ string) error {
	f.gecti()
	return nil
}

func (f *sahteAuth) UnlinkSalesChannel(_ context.Context, _, _ string) error {
	f.gecti()
	return nil
}

func (f *sahteAuth) SalesChannelsOfAPIKey(_ context.Context, _ string) ([]models.SalesChannel, error) {
	f.gecti()
	return nil, nil
}

func (f *sahteAuth) CreateSalesChannel(_ context.Context, _ service.SalesChannelInput) (models.SalesChannel, error) {
	f.gecti()
	return models.SalesChannel{ID: "sc_1"}, nil
}

func (f *sahteAuth) GetSalesChannel(_ context.Context, id string) (models.SalesChannel, error) {
	f.gecti()
	return models.SalesChannel{ID: id}, nil
}

func (f *sahteAuth) ListSalesChannels(
	_ context.Context,
	_ service.ListSalesChannelsInput,
) (service.Page[models.SalesChannel], error) {
	f.gecti()
	return service.Page[models.SalesChannel]{}, nil
}

func (f *sahteAuth) UpdateSalesChannel(
	_ context.Context,
	id string,
	_ service.UpdateSalesChannelInput,
) (models.SalesChannel, error) {
	f.gecti()
	return models.SalesChannel{ID: id}, nil
}

func (f *sahteAuth) DeleteSalesChannel(_ context.Context, _ string) error {
	f.gecti()
	return nil
}
