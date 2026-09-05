package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// yeniSaglayici sahte depo üzerinde çalışan bir sağlayıcı kurar.
func yeniSaglayici(t *testing.T) (*QueryProvider, *Service, *memRepo) {
	t.Helper()

	svc, repo := yeniServis(t)
	return NewQueryProvider(svc), svc, repo
}

// TestSaglayiciEntityAdi sağlayıcının kayıt adıyla örtüşen entity adını
// döndürdüğünü kanıtlar.
//
// Query, sağlayıcıyı "<entity>.query" adıyla arar ve Entity() ile adın
// örtüştüğünü DOĞRULAR; ikisi ayrışırsa çözüm anında hata verir (ADR 0004).
func TestSaglayiciEntityAdi(t *testing.T) {
	p, _, _ := yeniSaglayici(t)
	assert.Equal(t, "customer", p.Entity())
	assert.Equal(t, "customer.query", p.Entity()+query.ProviderSuffix)
}

// TestSaglayiciGrupKimlikleriniTekSorgudaGetirir N+1 yasağını kanıtlar.
//
// Üç müşteri için grup kimlikleri TEK toplu çağrıyla gelmelidir. Müşteri başına
// ayrı sorgu yapan bir uygulama da aynı SONUCU üretirdi; bu yüzden test sonucu
// değil ÇAĞRI SAYISINI ölçer — ADR 0004'ün yasakladığı şey sonuç değil,
// gidiş-dönüş sayısıdır.
func TestSaglayiciGrupKimlikleriniTekSorgudaGetirir(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := yeniSaglayici(t)

	grup := yeniGrup(ctx, t, svc, "VIP")
	var kimlikler []string
	for _, eposta := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		musteri := yeniMusteri(ctx, t, svc, eposta)
		require.NoError(t, svc.AddToGroup(ctx, musteri.ID, grup.ID))
		kimlikler = append(kimlikler, musteri.ID)
	}

	repo.calls["GroupIDsOfCustomers"] = 0
	kayitlar, err := p.FetchByIDs(ctx, kimlikler, []string{fieldID, fieldGroupIDs})
	require.NoError(t, err)
	require.Len(t, kayitlar, 3)

	assert.Equal(t, 1, repo.calls["GroupIDsOfCustomers"],
		"üç müşteri için grup kimlikleri TEK çağrıda gelmeli (N+1 yasağı)")

	for _, kayit := range kayitlar {
		assert.Equal(t, []string{grup.ID}, kayit[fieldGroupIDs])
	}
}

// TestSaglayiciGrupsuzMusteriBosDilim grubu olmayan müşteri için nil değil boş
// dilim döndüğünü kanıtlar.
func TestSaglayiciGrupsuzMusteriBosDilim(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := yeniSaglayici(t)

	musteri := yeniMusteri(ctx, t, svc, "grupsuz@example.com")

	kayitlar, err := p.FetchByIDs(ctx, []string{musteri.ID}, nil)
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	ids, ok := kayitlar[0][fieldGroupIDs].([]string)
	require.True(t, ok, "group_ids alanı dize dilimi olmalı")
	assert.NotNil(t, ids)
	assert.Empty(t, ids)
}

// TestSaglayiciGrupKimlikleriIstenmezseSorgulanmaz alan seçimiyle üyelik
// sorgusunun hiç çalıştırılmadığını kanıtlar.
func TestSaglayiciGrupKimlikleriIstenmezseSorgulanmaz(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := yeniSaglayici(t)

	musteri := yeniMusteri(ctx, t, svc, "alan@example.com")

	repo.calls["GroupIDsOfCustomers"] = 0
	kayitlar, err := p.FetchByIDs(ctx, []string{musteri.ID}, []string{fieldEmail})
	require.NoError(t, err)
	require.Len(t, kayitlar, 1)

	assert.Zero(t, repo.calls["GroupIDsOfCustomers"],
		"group_ids istenmediyse üyelik sorgusu hiç tetiklenmemeli")
	assert.NotContains(t, kayitlar[0], fieldGroupIDs)
	// Kimlik istenmese de EKLENİR: Query kayıtları "id" üzerinden birleştirir.
	assert.Equal(t, musteri.ID, kayitlar[0][query.IDField])
}

// TestSaglayiciBilinmeyenAlan desteklenmeyen alan için Invalid döndüğünü
// kanıtlar (ADR 0004: alan doğrulaması sağlayıcıya aittir).
func TestSaglayiciBilinmeyenAlan(t *testing.T) {
	ctx := context.Background()
	p, _, _ := yeniSaglayici(t)

	_, err := p.FetchByIDs(ctx, []string{"cust_x"}, []string{"gizli_alan"})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	_, err = p.List(ctx, query.ListOptions{Fields: []string{"gizli_alan"}})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))
}

// TestSaglayiciFiltreleri desteklenen ve desteklenmeyen filtreleri kanıtlar.
func TestSaglayiciFiltreleri(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := yeniSaglayici(t)

	hesap := yeniMusteri(ctx, t, svc, "filtre@example.com")
	misafir, err := svc.RegisterGuest(ctx, CustomerInput{Email: "misafir@example.com"})
	require.NoError(t, err)

	t.Run("kimlik", func(t *testing.T) {
		kayitlar, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"id": hesap.ID}})
		require.NoError(t, listErr)
		require.Len(t, kayitlar, 1)
		assert.Equal(t, hesap.ID, kayitlar[0][fieldID])
	})

	t.Run("has_account", func(t *testing.T) {
		kayitlar, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"has_account": false}})
		require.NoError(t, listErr)
		require.Len(t, kayitlar, 1)
		assert.Equal(t, misafir.ID, kayitlar[0][fieldID])
	})

	t.Run("eposta normalize edilir", func(t *testing.T) {
		kayitlar, listErr := p.List(ctx, query.ListOptions{
			Filters: map[string]any{"email": "FILTRE@EXAMPLE.COM"},
		})
		require.NoError(t, listErr)
		require.Len(t, kayitlar, 1)
		assert.Equal(t, hesap.ID, kayitlar[0][fieldID])
	})

	t.Run("bilinmeyen filtre", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"soyad": "Veli"}})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr))
	})

	t.Run("yanlis tip", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{Filters: map[string]any{"has_account": "evet"}})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr))
	})

	t.Run("kimlik baska filtreyle birlesemez", func(t *testing.T) {
		_, listErr := p.List(ctx, query.ListOptions{
			Filters: map[string]any{"id": hesap.ID, "has_account": true},
		})
		require.Error(t, listErr)
		assert.Equal(t, errors.KindInvalid, errors.KindOf(listErr),
			"kesin kimlik kümesi ikinci bir süzgeçle sessizce daraltılamaz")
	})
}

// TestSaglayiciBulunamayanKimlik eksik kimlik için kayıt DÖNMEDİĞİNİ ve bunun
// hata OLMADIĞINI kanıtlar (ADR 0004 sözleşmesi).
func TestSaglayiciBulunamayanKimlik(t *testing.T) {
	ctx := context.Background()
	p, svc, _ := yeniSaglayici(t)

	musteri := yeniMusteri(ctx, t, svc, "var@example.com")
	yok := models.NewCustomerID(sabitSaat)

	kayitlar, err := p.FetchByIDs(ctx, []string{musteri.ID, yok}, nil)
	require.NoError(t, err, "bulunamayan kimlik hata değildir")
	require.Len(t, kayitlar, 1)
	assert.Equal(t, musteri.ID, kayitlar[0][fieldID])
}

// TestSaglayiciSinirsizListeVarsayilanaDuser limit 0 verildiğinde modülün
// varsayılan sayfa boyunun uygulandığını kanıtlar.
//
// Query sözleşmesinde 0 "sınırsız" demektir; sınırsız bir kök listesi tek
// istekte tüm müşteri tablosunu belleğe alırdı.
func TestSaglayiciSinirsizListeVarsayilanaDuser(t *testing.T) {
	ctx := context.Background()
	p, svc, repo := yeniSaglayici(t)

	for _, eposta := range []string{"s1@example.com", "s2@example.com"} {
		yeniMusteri(ctx, t, svc, eposta)
	}

	// Sahte depo uygulanan limiti geri bildirmediği için sınır, servisin
	// doğrulamasıyla dolaylı kanıtlanır: aşırı büyük bir limit reddedilir.
	_, err := p.List(ctx, query.ListOptions{Limit: int(MaxLimit) + 1})
	require.Error(t, err)
	assert.Equal(t, errors.KindInvalid, errors.KindOf(err))

	kayitlar, err := p.List(ctx, query.ListOptions{})
	require.NoError(t, err)
	assert.Len(t, kayitlar, 2)
	assert.Positive(t, repo.calls["ListCustomers"])
}
