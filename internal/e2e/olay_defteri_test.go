//go:build integration

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/eventbus"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
)

// olayBeklemeSuresi bir olayın veri yolundan gelmesi için tanınan süredir.
//
// Bekleme ZORUNLUDUR ve süre bir tercih değil, sözleşmenin sonucudur:
// [eventbus.EventBus].Publish handler'ları BEKLEMEZ ve bellek içi backend her
// handler'ı kendi goroutine'inde çalıştırır. Yani sipariş yazılmış olsa bile
// olay defterde henüz görünmüyor olabilir. Süre cömerttir çünkü testin amacı
// olayın NE KADAR ÇABUK geldiğini değil, GELDİĞİNİ kanıtlamaktır.
const olayBeklemeSuresi = 5 * time.Second

// orderEventLog "order.placed" olaylarının test tarafındaki kaydıdır.
//
// # Neden TEK ve SÜREÇ ÖMÜRLÜ bir abone
//
// [eventbus.EventBus] abonelikten ÇIKMA sunmaz — imzası bilinçli olarak
// context almaz, abonelik sürecin ömrüne bağlıdır. Dolayısıyla test başına
// abone olmak, koşu ilerledikçe biriken ve her olayı defalarca kaydeden bir
// handler yığını üretirdi. Tek defter TestMain'de bir kez bağlanır ve testler
// kendi siparişlerini KİMLİKLE süzer; süzme, testlerin birbirinin olayını
// görmesini yapısal olarak imkânsız kılar.
//
// Tip eşzamanlı kullanıma güvenlidir: handler'lar ayrı goroutine'lerde koşar ve
// yazma ile okuma aynı kilidi paylaşır.
type orderEventLog struct {
	mu       sync.Mutex
	kayitlar []eventbus.Event
}

// abone defteri veri yoluna bağlar.
//
// Bağlama modüller ayağa kalkmadan ÖNCE yapılmalıdır: sonradan bağlanan bir
// abone, kendisinden önce yayımlanmış olayları GÖREMEZ (bellek içi backend
// geçmişi tutmaz, EN FAZLA BİR KEZ teslim eder).
func (d *orderEventLog) abone(bus eventbus.EventBus) error {
	return bus.Subscribe(ordersvc.EventOrderPlaced, func(_ context.Context, e eventbus.Event) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.kayitlar = append(d.kayitlar, e)
		return nil
	})
}

// olaylar verilen siparişe ait kayıtları döner.
//
// Süzme order_id alanı üzerindendir; alanı olmayan ya da başka bir siparişe ait
// olaylar atlanır.
func (d *orderEventLog) olaylar(siparisID string) []eventbus.Event {
	d.mu.Lock()
	defer d.mu.Unlock()

	var bulunan []eventbus.Event
	for i := range d.kayitlar {
		if deger, ok := d.kayitlar[i].Data[ordersvc.EventFieldOrderID].(string); ok && deger == siparisID {
			bulunan = append(bulunan, d.kayitlar[i])
		}
	}
	return bulunan
}

// waitFor siparişin olayının gelmesini bekler ve TEK olayı döner.
//
// Tekillik ayrıca sınanır: aynı sipariş için iki olay, abonelerin siparişi iki
// kez işlemesi (örneğin iki kez bildirim göndermesi) demektir ve teslimat
// garantisi "en fazla bir kez" olduğu için bu bir YAYIM hatasıdır, teslim
// tekrarı değildir.
func (d *orderEventLog) waitFor(t *testing.T, siparisID string) eventbus.Event {
	t.Helper()

	var bulunan []eventbus.Event
	require.Eventually(t, func() bool {
		bulunan = d.olaylar(siparisID)
		return len(bulunan) > 0
	}, olayBeklemeSuresi, 20*time.Millisecond,
		"%q olayı %s siparişi için yayımlanmalı; olay yayımlanmazsa bildirim, muhasebe "+
			"ve arama indeksi gibi aboneler siparişten HABERSİZ kalır ve eksiklik ancak "+
			"müşteri şikâyetiyle fark edilir", ordersvc.EventOrderPlaced, siparisID)

	require.Len(t, bulunan, 1,
		"%s siparişi için TEK olay yayımlanmalı; ikinci bir olay her abonenin aynı "+
			"siparişi iki kez işlemesi demektir", siparisID)
	return bulunan[0]
}

// olayAlani olayın yükünden bir alanı DİZE olarak okur.
//
// Tip iddiası ayrı bir yardımcıda toplanmıştır çünkü sözleşme onu her alan için
// tekrarlar: yükün TÜM değerleri string'tir ve sayısal olanlar ondalıksız onluk
// dize taşır (bkz. ordersvc.EventFieldTotal). Kural veri yolunun taşıma
// biçiminden doğar — Redis backend'inde int64 olarak konan bir alan aboneye
// float64 döner. Test bu yüzden tipi de iddia eder: alan sayıya çevrilirse
// üretimdeki abone düşerdi.
func olayAlani(t *testing.T, olay eventbus.Event, anahtar string) string {
	t.Helper()

	ham, mevcut := olay.Data[anahtar]
	require.True(t, mevcut, "olay yükünde %q alanı bulunmalı", anahtar)

	deger, ok := ham.(string)
	require.True(t, ok,
		"olay yükündeki %q alanı DİZE olmalı, %T değil; sayısal tipler iki backend'de "+
			"farklı Go tipine çözülür ve sözleşmeye göre yazılmış abone üretimde düşerdi",
		anahtar, ham)
	return deger
}
