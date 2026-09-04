package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// sabitSaat testlerin belirlenimci zaman kaynağıdır.
var sabitSaat = time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

// yeniRouter verilen sahte servisle route'ları bağlanmış bir router üretir.
func yeniRouter(svc api.Customer) chi.Router {
	r := chi.NewRouter()
	api.New(svc).Routes(r)
	return r
}

// adminKimlik testlerin varsayılan çağıranıdır: tam yetkili yönetim kimliği.
var adminKimlik = corehttp.Principal{
	ID:     "user_test",
	Kind:   "user",
	Scopes: []string{corehttp.ScopeAdmin},
}

// istek bir HTTP isteğini TAM YETKİLİ bir kimlikle çalıştırıp yanıtı döner.
//
// Kimliğin context'e konması, yönetim uçları corehttp.RequireScope ile
// korunduğu için gereklidir: o middleware kimliği context'ten okur ve kimliği
// oraya koyan corehttp.RequireAdmin bu testte YOKTUR (router doğrudan
// kurulur). Kimlik eklenmeseydi bu dosyadaki her yönetim testi, sınadığı
// davranışa hiç ulaşamadan 401 alırdı. Testlerin ne doğruladığı değişmedi;
// yalnızca çağıranın kim olduğu belirtildi.
func istek(t *testing.T, r chi.Router, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return istekGonder(t, r, &adminKimlik, method, path, body)
}

// istekGonder isteği verilen kimlikle çalıştırır; kimlik nil ise istek
// KİMLİKSİZ gider.
func istekGonder(t *testing.T, r chi.Router, kimlik *corehttp.Principal, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	ctx := context.Background()
	if kimlik != nil {
		ctx = corehttp.WithPrincipal(ctx, *kimlik)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, reader)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// govde yanıt gövdesini haritaya çözer.
func govde(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "gövde: %s", rec.Body.String())
	return out
}

// ornekMusteri testlerde dönen örnek müşteridir.
func ornekMusteri(hasAccount bool) models.Customer {
	return models.Customer{
		ID:         "cust_0123456789ABCDEFGHJKMNPQRS",
		Email:      "ali@example.com",
		FirstName:  "Ali",
		LastName:   "Veli",
		HasAccount: hasAccount,
		CreatedAt:  sabitSaat,
		UpdatedAt:  sabitSaat,
	}
}

// TestYonetimMusteriOlusturmaHesapAcar admin ucunun KAYITLI hesap açtığını
// kanıtlar.
//
// Ayrım gövdedeki bir bayrağa bırakılsaydı yönetim isteği sessizce
// benzersizlik kuralının dışına düşerdi.
func TestYonetimMusteriOlusturmaHesapAcar(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			return ornekMusteri(true), nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/customers", `{"email":"Ali@Example.com"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	data, ok := govde(t, rec)["data"].(map[string]any)
	require.True(t, ok, "tekil yanıt {\"data\":{...}} zarfında olmalı")
	assert.Equal(t, true, data["has_account"])
	assert.Equal(t, "Ali@Example.com", svc.sonInput.Email, "gövde servise olduğu gibi iletilmeli")
}

// TestVitrinKaydiMisafirAcar store ucunun MİSAFİR kaydı açtığını kanıtlar.
func TestVitrinKaydiMisafirAcar(t *testing.T) {
	var cagrildi bool
	svc := &stubCustomer{
		registerGuestFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			cagrildi = true
			return ornekMusteri(false), nil
		},
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("vitrin ucu hesap açan yolu ÇAĞIRMAMALI")
			return models.Customer{}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/store/v1/customers", `{"email":"misafir@example.com"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.True(t, cagrildi)

	data, _ := govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, false, data["has_account"])
}

// TestHataSinifiStatusKoduna handler'ın status kodu SEÇMEDİĞİNİ, sınıfın
// çevrildiğini kanıtlar.
//
// Eşleme core/http'dedir (plan Bölüm 2.7); handler'da tekrarlansaydı iki yer
// ayrışabilirdi.
func TestHataSinifiStatusKoduna(t *testing.T) {
	durumlar := map[string]struct {
		err    error
		status int
	}{
		"bulunamadı": {errors.NotFound("customer_not_found", "yok"), http.StatusNotFound},
		"geçersiz":   {errors.Invalid("customer_invalid_input", "kötü"), http.StatusUnprocessableEntity},
		"çakışma":    {errors.Conflict("customer_email_taken", "dolu"), http.StatusConflict},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			svc := &stubCustomer{
				getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
					return models.Customer{}, durum.err
				},
			}
			r := yeniRouter(svc)

			rec := istek(t, r, http.MethodGet, "/admin/v1/customers/cust_1", "")
			assert.Equal(t, durum.status, rec.Code, rec.Body.String())

			hata, ok := govde(t, rec)["error"].(map[string]any)
			require.True(t, ok, "hata zarfı {\"error\":{...}} olmalı")
			assert.Equal(t, errors.CodeOf(durum.err), hata["code"])
		})
	}
}

// TestBilinmeyenAlanReddedilir sessizce yok sayılan alanın olmadığını kanıtlar.
//
// Sessizce atlanan bir alan, istemcinin gönderdiğini sandığı değerin hiç
// yazılmaması demektir.
func TestBilinmeyenAlanReddedilir(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("bilinmeyen alan içeren gövde servise ULAŞMAMALI")
			return models.Customer{}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/customers",
		`{"email":"ali@example.com","has_account":true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestBosVeCiftGovde bozuk gövdelerin reddedildiğini kanıtlar.
func TestBosVeCiftGovde(t *testing.T) {
	r := yeniRouter(&stubCustomer{})

	rec := istek(t, r, http.MethodPost, "/admin/v1/customers", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, "boş gövde reddedilmeli")

	rec = istek(t, r, http.MethodPost, "/admin/v1/customers",
		`{"email":"a@b.co"}{"email":"c@d.co"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"tek JSON belgesi beklenir; ikincisi sessizce yok sayılmamalı")
}

// TestListeZarfi liste yanıtının sayfalama alanlarını kanıtlar.
func TestListeZarfi(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, in service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{
				Items:  []models.Customer{ornekMusteri(true)},
				Count:  42,
				Limit:  in.Limit,
				Offset: in.Offset,
			}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/customers?limit=10&offset=20", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := govde(t, rec)
	assert.Equal(t, float64(42), body["count"])
	assert.Equal(t, float64(10), body["limit"])
	assert.Equal(t, float64(20), body["offset"])
	items, ok := body["data"].([]any)
	require.True(t, ok, "liste yanıtı {\"data\":[...]} zarfında olmalı")
	assert.Len(t, items, 1)
}

// TestListeSuzgecleriServiseIletilir sorgu parametrelerinin doğru okunduğunu
// kanıtlar.
//
// has_account için nil ile false AYRIDIR: parametrenin hiç verilmemesi
// süzmemek, "false" verilmesi misafirleri süzmektir.
func TestListeSuzgecleriServiseIletilir(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{Items: []models.Customer{}}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/customers", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Nil(t, svc.sonListInput.HasAccount, "parametre verilmediyse süzgeç kurulmamalı")
	assert.Nil(t, svc.sonListInput.Email)

	rec = istek(t, r, http.MethodGet, "/admin/v1/customers?has_account=false&email=A%40B.co", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, svc.sonListInput.HasAccount)
	assert.False(t, *svc.sonListInput.HasAccount, "false gerçek bir süzgeçtir")
	require.NotNil(t, svc.sonListInput.Email)
	assert.Equal(t, "A@B.co", *svc.sonListInput.Email)
}

// TestBozukSorguParametresi sayıya çevrilemeyen limitin sessizce sıfıra
// düşmediğini kanıtlar.
func TestBozukSorguParametresi(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			t.Fatal("bozuk parametreyle servise gidilmemeli")
			return service.Page[models.Customer]{}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/customers?limit=abc", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	rec = istek(t, r, http.MethodGet, "/admin/v1/customers?has_account=belki", "")
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

// TestMisafirDonusumuGuncelKaydiDoner dönüşüm sonrası kaydın yeniden
// okunduğunu kanıtlar.
func TestMisafirDonusumuGuncelKaydiDoner(t *testing.T) {
	var donusturuldu bool
	svc := &stubCustomer{
		convertGuestFn: func(_ context.Context, _ string) error {
			donusturuldu = true
			return nil
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			return ornekMusteri(donusturuldu), nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/customers/cust_1/convert-to-account", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	data, _ := govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["has_account"], "yanıt dönüşüm SONRASI hâli göstermeli")
}

// TestMisafirDonusumuCakismasi çakışmanın 409'a çevrildiğini kanıtlar.
func TestMisafirDonusumuCakismasi(t *testing.T) {
	svc := &stubCustomer{
		convertGuestFn: func(_ context.Context, _ string) error {
			return errors.Conflict("customer_email_taken", "dolu")
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			t.Fatal("dönüşüm hata verdiyse kayıt yeniden OKUNMAMALI")
			return models.Customer{}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/admin/v1/customers/cust_1/convert-to-account", "")
	assert.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
}

// TestSilmeIcerikDondurmez silme uçlarının 204 döndüğünü kanıtlar.
func TestSilmeIcerikDondurmez(t *testing.T) {
	svc := &stubCustomer{
		deleteCustomerFn: func(_ context.Context, _ string) error { return nil },
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodDelete, "/admin/v1/customers/cust_1", "")
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

// TestGruptanCikarmaYolParametreleri yol parametrelerinin karışmadığını
// kanıtlar.
//
// Yol /customer-groups/{id}/customers/{customer_id} biçimindedir; ikisinin yer
// değiştirmesi, var olmayan bir üyeliği silme girişimine dönüşürdü.
func TestGruptanCikarmaYolParametreleri(t *testing.T) {
	svc := &stubCustomer{
		removeFromGroupFn: func(_ context.Context, _, _ string) error { return nil },
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodDelete,
		"/admin/v1/customer-groups/custgrp_9/customers/cust_7", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())

	assert.Equal(t, "cust_7", svc.sonCustomerID)
	assert.Equal(t, "custgrp_9", svc.sonGroupID)
}

// TestSayfalanmayanListeZarfi sayfalanmayan listelerin de aynı zarf şeklini
// kullandığını kanıtlar.
func TestSayfalanmayanListeZarfi(t *testing.T) {
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, _ string) ([]models.CustomerAddress, error) {
			return []models.CustomerAddress{
				{ID: "addr_1", CustomerID: "cust_1", Address1: "Cad. 1", City: "İstanbul", CountryCode: "TR"},
				{ID: "addr_2", CustomerID: "cust_1", Address1: "Cad. 2", City: "Ankara", CountryCode: "TR"},
			}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/customers/cust_1/addresses", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	body := govde(t, rec)
	assert.Equal(t, float64(2), body["count"])
	assert.Equal(t, float64(2), body["limit"], "sayfa yoksa limit kayıt sayısına eşittir")
	assert.Equal(t, float64(0), body["offset"])
}

// TestBosListeNullDegilDizidir boş listenin JSON'da [] olarak göründüğünü
// kanıtlar.
func TestBosListeNullDegilDizidir(t *testing.T) {
	svc := &stubCustomer{
		listAddressesFn: func(_ context.Context, _ string) ([]models.CustomerAddress, error) {
			return nil, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodGet, "/admin/v1/customers/cust_1/addresses", "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"data":[]`, "boş liste null değil [] olmalı")
}

// TestVitrinAdresUclari vitrin uçlarının customer idni yoldan aldığını
// kanıtlar.
//
// FAZ 8 NOTU: kimlik şimdilik istemcinin bildirdiği değerdir ve doğrulanmaz;
// koruma auth middleware ile gelecektir (bkz. api paket belgesi).
func TestVitrinAdresUclari(t *testing.T) {
	svc := &stubCustomer{
		createAddressFn: func(_ context.Context, _ string, in service.AddressInput) (models.CustomerAddress, error) {
			return models.CustomerAddress{
				ID: "addr_1", CustomerID: "cust_1",
				Address1: in.Address1, City: in.City, CountryCode: "TR",
			}, nil
		},
		setDefaultShipFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultShipping: true}, nil
		},
		setDefaultBillFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultBilling: true}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost, "/store/v1/customers/cust_1/addresses",
		`{"address_1":"Cad. 1","city":"İstanbul","country_code":"tr"}`)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Equal(t, "cust_1", svc.sonCustomerID)

	rec = istek(t, r, http.MethodPost,
		"/store/v1/customers/cust_1/addresses/addr_1/default-shipping", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_shipping"])
	assert.Equal(t, "addr_1", svc.sonAddressID)

	rec = istek(t, r, http.MethodPost,
		"/store/v1/customers/cust_1/addresses/addr_1/default-billing", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_billing"])
}

// TestYonetimVarsayilanAdresUclari varsayılan adresi belirleyen uçların YÖNETİM
// tarafında da bulunduğunu kanıtlar.
//
// Uçlar olmasaydı yönetici mevcut bir adresi varsayılan yapmak için onu
// yeniden OLUŞTURMAK zorunda kalırdı: güncelleme gövdesinde işaret bilinçli
// olarak yoktur ve yeniden oluşturma adresin kimliğini değiştirirdi.
func TestYonetimVarsayilanAdresUclari(t *testing.T) {
	svc := &stubCustomer{
		setDefaultShipFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultShipping: true}, nil
		},
		setDefaultBillFn: func(_ context.Context, _, addressID string) (models.CustomerAddress, error) {
			return models.CustomerAddress{ID: addressID, CustomerID: "cust_1", IsDefaultBilling: true}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPost,
		"/admin/v1/customers/cust_1/addresses/addr_1/default-shipping", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_shipping"])
	assert.Equal(t, "cust_1", svc.sonCustomerID, "customer id yol parametresinden gelmeli")
	assert.Equal(t, "addr_1", svc.sonAddressID)

	rec = istek(t, r, http.MethodPost,
		"/admin/v1/customers/cust_1/addresses/addr_1/default-billing", "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ = govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, true, data["is_default_billing"])
	assert.Equal(t, "cust_1", svc.sonCustomerID)
}

// TestYonetimGrupGuncellemeVeSilme grup düzeltme ve silme uçlarını kanıtlar.
//
// Ad canlı gruplar arasında benzersizdir; bu uçlar olmasaydı yanlış girilmiş
// bir ad ne düzeltilebilir ne de serbest bırakılabilirdi.
func TestYonetimGrupGuncellemeVeSilme(t *testing.T) {
	var sonInput service.UpdateGroupInput
	svc := &stubCustomer{
		updateGroupFn: func(_ context.Context, id string, in service.UpdateGroupInput) (models.CustomerGroup, error) {
			sonInput = in
			ad := "VIP"
			if in.Name != nil {
				ad = *in.Name
			}
			return models.CustomerGroup{
				ID: id, Name: ad, Metadata: in.Metadata,
				CreatedAt: sabitSaat, UpdatedAt: sabitSaat,
			}, nil
		},
		deleteGroupFn: func(_ context.Context, _ string) error { return nil },
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPut, "/admin/v1/customer-groups/custgrp_1", `{"name":"B2B"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	data, _ := govde(t, rec)["data"].(map[string]any)
	assert.Equal(t, "B2B", data["name"])
	assert.Equal(t, "custgrp_1", svc.sonGroupID)

	// Ad gönderilmezse servise nil gider: "dokunma" ile "boşa çek" ayrılır.
	rec = istek(t, r, http.MethodPut, "/admin/v1/customer-groups/custgrp_1",
		`{"metadata":{"indirim":"10"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Nil(t, sonInput.Name, "gönderilmeyen ad servise nil gitmeli")
	assert.Equal(t, "10", sonInput.Metadata["indirim"])

	rec = istek(t, r, http.MethodDelete, "/admin/v1/customer-groups/custgrp_1", "")
	require.Equal(t, http.StatusNoContent, rec.Code, rec.Body.String())
	assert.Empty(t, rec.Body.String(), "204 boş gövdeyle dönmeli")
	assert.Equal(t, "custgrp_1", svc.sonGroupID)
}

// TestAdresGuncellemeVarsayilanIsaretiTasimaz gövdede varsayılan işaretinin
// KABUL EDİLMEDİĞİNİ kanıtlar.
//
// İşaret değiştirmek müşterinin diğer adreslerini de ilgilendirir; tek satırlık
// bir güncellemeyle yapılsaydı müşteri iki varsayılan adresle kalabilirdi.
func TestAdresGuncellemeVarsayilanIsaretiTasimaz(t *testing.T) {
	svc := &stubCustomer{
		updateAddressFn: func(_ context.Context, _, _ string, _ service.UpdateAddressInput) (models.CustomerAddress, error) {
			t.Fatal("varsayılan işareti taşıyan gövde servise ULAŞMAMALI")
			return models.CustomerAddress{}, nil
		},
	}
	r := yeniRouter(svc)

	rec := istek(t, r, http.MethodPut, "/store/v1/customers/cust_1/addresses/addr_1",
		`{"city":"Ankara","is_default_shipping":true}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
}

// TestVitrindeMusteriListelemesiYok müşteri listesinin store tarafında
// bulunmadığını kanıtlar.
//
// Vitrine açılan bir müşteri listesi, tüm müşterilerin e-postalarını herkese
// açık hâle getirirdi.
func TestVitrindeMusteriListelemesiYok(t *testing.T) {
	r := yeniRouter(&stubCustomer{})

	rec := istek(t, r, http.MethodGet, "/store/v1/customers", "")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code,
		"vitrinde müşteri LİSTELEME ucu olmamalı")
}

// TestDarYetkiliKimlikYazmaUcunda403Alir yalnızca [api.ScopeRead] taşıyan bir
// kimliğin yönetim YAZMA uçlarından geçemediğini kanıtlar.
//
// Sınanan senaryo somuttur: okuma yetkisiyle giriş yapan bir yönetim kimliği,
// yetki zorlaması olmasaydı DELETE /admin/v1/customers/{id} ile müşteri
// kayıtlarını silebilirdi. Servis HİÇ çağrılmamalıdır; sahtenin içindeki
// t.Fatal bunu ayrıca kanıtlar.
func TestDarYetkiliKimlikYazmaUcunda403Alir(t *testing.T) {
	svc := &stubCustomer{
		createCustomerFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			t.Fatal("yetkisiz istek servise ULAŞMAMALI")
			return models.Customer{}, nil
		},
		deleteCustomerFn: func(_ context.Context, _ string) error {
			t.Fatal("yetkisiz istek servise ULAŞMAMALI")
			return nil
		},
		updateCustomerFn: func(_ context.Context, _ string, _ service.UpdateCustomerInput) (models.Customer, error) {
			t.Fatal("yetkisiz istek servise ULAŞMAMALI")
			return models.Customer{}, nil
		},
	}
	r := yeniRouter(svc)
	darKimlik := corehttp.Principal{ID: "user_dar", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, uc := range []struct{ method, path, body string }{
		{http.MethodPost, "/admin/v1/customers", `{"email":"a@b.co"}`},
		{http.MethodPut, "/admin/v1/customers/cust_1", `{"first_name":"Ali"}`},
		{http.MethodDelete, "/admin/v1/customers/cust_1", ""},
		{http.MethodPost, "/admin/v1/customer-groups", `{"name":"VIP"}`},
	} {
		rec := istekGonder(t, r, &darKimlik, uc.method, uc.path, uc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code,
			"%s %s okuma yetkisiyle açılmamalı: %s", uc.method, uc.path, rec.Body.String())
	}
}

// TestDarYetkiliKimlikOkumaUcundaGecer aynı dar kimliğin yönetim OKUMA
// uçlarından GEÇTİĞİNİ kanıtlar.
//
// Yetki zorlamasının değeri, dar yetkiyi de gerçekten kabul etmesindedir:
// yalnızca reddetseydi kimse dar yetki dağıtmaz, herkese admin verilirdi.
func TestDarYetkiliKimlikOkumaUcundaGecer(t *testing.T) {
	svc := &stubCustomer{
		listCustomersFn: func(_ context.Context, _ service.ListCustomersInput) (service.Page[models.Customer], error) {
			return service.Page[models.Customer]{Items: []models.Customer{ornekMusteri(true)}, Count: 1}, nil
		},
		getCustomerFn: func(_ context.Context, _ string) (models.Customer, error) {
			return ornekMusteri(true), nil
		},
	}
	r := yeniRouter(svc)
	darKimlik := corehttp.Principal{ID: "user_dar", Kind: "user", Scopes: []string{api.ScopeRead}}

	for _, path := range []string{"/admin/v1/customers", "/admin/v1/customers/cust_1"} {
		rec := istekGonder(t, r, &darKimlik, http.MethodGet, path, "")
		assert.Equal(t, http.StatusOK, rec.Code, "GET %s: %s", path, rec.Body.String())
	}
}

// TestKimliksizYonetimIstegi401Alir kimliği hiç olmayan isteğin 401 aldığını
// kanıtlar.
//
// Ayrım bilinçlidir: 401 "kim olduğunu söyle", 403 "kim olduğunu biliyorum
// ama yetkin yok" demektir. İkisi karışsaydı istemci, kimliğini yenileyerek
// çözülmeyecek bir sorun için oturum tazelemeyi denerdi.
func TestKimliksizYonetimIstegi401Alir(t *testing.T) {
	r := yeniRouter(&stubCustomer{})

	rec := istekGonder(t, r, nil, http.MethodGet, "/admin/v1/customers", "")

	assert.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
}

// TestMagazaUclariYetkiIstemez store yüzeyine yetki EKLENMEDİĞİNİ kanıtlar.
//
// Mağaza yüzeyinin kimliği publishable anahtardır ve o anahtar tanımı gereği
// yetki taşımaz; store uçlarına yanlışlıkla bir scope takılırsa vitrin
// tamamen çalışmaz hâle gelir ve bu test onu hemen yakalar.
func TestMagazaUclariYetkiIstemez(t *testing.T) {
	svc := &stubCustomer{
		registerGuestFn: func(_ context.Context, _ service.CustomerInput) (models.Customer, error) {
			return ornekMusteri(false), nil
		},
	}
	r := yeniRouter(svc)

	rec := istekGonder(t, r, nil, http.MethodPost, "/store/v1/customers", `{"email":"a@b.co"}`)

	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}
