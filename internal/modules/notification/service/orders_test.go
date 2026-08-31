package service_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

// TestOrderContactsCozumuTEMBELDIR yüzeyin Register anında değil İLK
// KULLANIMDA çözüldüğünü doğrular.
//
// Modüllerin Register sırası garanti edilmez: notification, order'dan önce
// kaydedilebilir ve o anda "order.interop" container'da olmaz. Erken çözüm,
// hiçbir şeyin gerçekten eksik olmadığı bir hatayla açılışı düşürürdü.
func TestOrderContactsCozumuTEMBELDIR(t *testing.T) {
	c := container.New(nil)

	// Yüzey HENÜZ kayıtlı değilken kurulur; kurulum hata vermemelidir.
	okuyucu := service.NewOrderContacts(c)
	require.NotNil(t, okuyucu)

	require.NoError(t, c.Provide(service.OrderInteropName, &fakeContacts{govde: testSiparisGovdesi}))

	ham, err := okuyucu.OrderContactJSON(context.Background(), "order_01H")

	require.NoError(t, err)
	var govde map[string]string
	require.NoError(t, json.Unmarshal(ham, &govde))
	assert.Equal(t, "musteri@example.com", govde["email"])
}

// TestOrderContactsYuzeyYoksaTeshisEdilebilirHataVerir order modülü kurulu
// değilken alınan hatanın ne olduğunu söylediğini doğrular.
func TestOrderContactsYuzeyYoksaTeshisEdilebilirHataVerir(t *testing.T) {
	okuyucu := service.NewOrderContacts(container.New(nil))

	_, err := okuyucu.OrderContactJSON(context.Background(), "order_01H")

	require.Error(t, err)
	assert.Equal(t, service.CodeContactUnavailable, errors.CodeOf(err))
	assert.Contains(t, err.Error(), service.OrderInteropName,
		"hata hangi kaydın bulunamadığını söylemeli")
}

// TestOrderContactsBasarisizCozumKALICIDEGILDIR ilk çözümün düşmesinin süreç
// ömrü boyunca bildirimleri ölü bırakmadığını doğrular.
//
// sync.Once kullanılsaydı ilk çağrının SONUCU kalıcı olurdu: order henüz
// kayıtlı değilken gelen tek bir olay, sonraki tüm siparişlerin bildirimini de
// imkânsız kılardı.
func TestOrderContactsBasarisizCozumKALICIDEGILDIR(t *testing.T) {
	c := container.New(nil)
	okuyucu := service.NewOrderContacts(c)
	ctx := context.Background()

	_, err := okuyucu.OrderContactJSON(ctx, "order_01H")
	require.Error(t, err)

	require.NoError(t, c.Provide(service.OrderInteropName, &fakeContacts{govde: testSiparisGovdesi}))

	_, err = okuyucu.OrderContactJSON(ctx, "order_01H")
	assert.NoError(t, err, "kayıt sonradan geldiğinde çözüm yeniden denenmeli")
}

// TestOrderInteropAdiSozlesmeyleAynidir container adının elle tekrarlanan
// değerden kaymadığını doğrular.
//
// Ad ayrışırsa modül hiçbir siparişi okuyamaz; derleyici bunu yakalayamaz
// çünkü iki paket birbirini import edemez (Prensip 2.4).
func TestOrderInteropAdiSozlesmeyleAynidir(t *testing.T) {
	assert.Equal(t, "order.interop", service.OrderInteropName)
}
