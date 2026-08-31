package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
)

// Bu dosya bildirimleri TETİKLEYEN abonelerdir.
//
// # Hata politikası: hata DÖNÜLÜR, ama bu bir "yeniden dene" isteği DEĞİLDİR
//
// Karar ve gerekçesi plugins/searchpg/events.go dosya belgesindedir; burada
// tekrarlanmaz. Özeti: [eventbus.EventBus] hata dönen bir işleyiciyi yeniden
// TESLİM ETMEZ, olayı işlenmiş sayar ve hatayı ERROR seviyesinde loglar —
// dolayısıyla hatayı dönmek yeniden deneme istemek değil, arızayı GÖRÜNÜR
// kılmaktır.
//
// İşleyicinin kendi içinde yeniden denemesi de yapılmaz ve burada bunun ikinci
// bir sebebi vardır: aynı bildirimi tekrar denemek, sağlayıcının ilk denemeyi
// işlemiş olma ihtimali yüzünden İKİNCİ bir e-posta üretebilir (bkz.
// [Service.Notify] belgesi).
//
// # İşleyici İDEMPOTENTTİR
//
// Sözleşme sırayı garanti etmez ve InMemory backend'i aynı işleyiciyi eşzamanlı
// çağırabilir. Tekillik burada koda değil, teslim günlüğündeki (şablon,
// referans) benzersizliğine dayanır: aynı olayın iki kez işlenmesi ile bir kez
// işlenmesi AYNI sayıda bildirim üretir.

// EventOrderPlaced dinlenen sipariş olayının adıdır (order service'in
// EventOrderPlaced sabitiyle AYNI değer).
//
// Ad MODÜLLER ARASI SÖZLEŞMEDİR ve elle tekrarlanmıştır; modüller birbirini
// import edemez (Prensip 2.4). Ayrışmanın bedeli sessizdir: ad değişirse bu
// abone hiçbir olay almaz ve hiçbir hata da üretmez — kimse bir şey almadığını
// fark etmez. Uyum entegrasyon testiyle kanıtlanır.
const EventOrderPlaced = "order.placed"

// TemplateOrderPlaced sipariş onayı şablonunun adıdır.
//
// Olay adıyla AYNI seçilmesi bilinçlidir (bkz. çekirdekteki
// [coreprovider.Notification] belgesi): iki ayrı ad, "hangi olay hangi şablonu
// tetikliyor" sorusunu ancak koda bakarak yanıtlanır hâle getirirdi. Ad aynı
// zamanda idempotency anahtarının yarısıdır — değişmesi, tüm siparişler için
// bildirimin İKİNCİ KEZ gönderilebilir olması demektir.
const TemplateOrderPlaced = EventOrderPlaced

// eventFieldOrderID olay yükünde okunan TEK alandır.
//
// Şablonun ihtiyaç duyduğu geri kalan her şey kimliğin işaret ettiği KAYITTAN
// okunur (bkz. [Service.OrderPlaced]).
const eventFieldOrderID = "order_id"

// Şablona geçilen veri anahtarları.
//
// Adlar sipariş yüzeyinin alan adlarıyla birebir aynıdır; çevirmek, aynı verinin
// iki adla dolaşması ve şablon yazarının hangisinin doğru olduğunu bilmemesi
// demekti.
const (
	dataKeyOrderID      = "order_id"
	dataKeyDisplayID    = "display_id"
	dataKeyCurrencyCode = "currency_code"
	dataKeyTotal        = "total"
	dataKeyItemCount    = "item_count"
)

// EventSubscriber modülün olay veri yolundan istediği DAR yüzeydir.
//
// Modül yalnızca ABONE OLUR: yayımlamaz ve veri yolunu kapatmaz.
// [eventbus.EventBus]'ın tamamına bağlanmak, kapatma yetkisini de modüle
// vermek olurdu; veri yolunun ömrünü kompozisyon kökü yönetir.
type EventSubscriber interface {
	Subscribe(eventName string, h eventbus.Handler) error
}

// OrderPlaced "order.placed" olayını işler: siparişin iletişim bilgisini
// okur ve sipariş onayı bildirimini gönderir.
//
// # E-posta OLAYDAN değil KAYITTAN okunur
//
// Olayın yükünde e-posta BİLİNÇLİ OLARAK yoktur: olaylar Redis'e yazılır ve
// orada kalıcıdır; kişisel veriyi kalıcı bir akışa koymak, siparişin kendisinde
// zaten duran bir bilgi için gereksiz bir yayılımdır (order modülünün olay
// belgesi). Bu işleyici bu yüzden olaydan YALNIZCA sipariş kimliğini alır ve
// gerisini "order.interop" üzerinden okur.
//
// Okumanın ikinci bir faydası da vardır: olay yükü BAYAT olabilir (veri yolu
// sıra garantisi vermez), kayıt ise o anki gerçeği verir.
func (s *Service) OrderPlaced(ctx context.Context, e eventbus.Event) error {
	orderID, err := olayOrderID(e)
	if err != nil {
		return err
	}

	ham, err := s.contacts.OrderContactJSON(ctx, orderID)
	if err != nil {
		// Sınıf KORUNUR: sipariş bulunamadıysa NotFound, yüzey yoksa
		// Unavailable gelir ve ikisi farklı arızalardır.
		return errors.Wrap(err, errors.KindOf(err), CodeContactUnavailable,
			"%q olayı için sipariş iletişim bilgisi okunamadı: %s", e.Name, orderID)
	}

	kisi, err := cozContact(ham, orderID)
	if err != nil {
		return err
	}

	return s.Notify(ctx, NotifyInput{
		Template:  TemplateOrderPlaced,
		Channel:   coreprovider.ChannelEmail,
		Reference: orderID,
		To:        kisi.Email,
		Data: map[string]string{
			dataKeyOrderID:      kisi.OrderID,
			dataKeyDisplayID:    kisi.DisplayID,
			dataKeyCurrencyCode: kisi.CurrencyCode,
			dataKeyTotal:        kisi.Total,
			dataKeyItemCount:    kisi.ItemCount,
		},
	})
}

// olayOrderID olay yükünden sipariş kimliğini okur.
//
// Değerin DİZE olması sözleşmedir (bkz. order service/events.go): Redis
// backend'i yükü JSON'a çevirdiği için sayısal bir alan aboneye float64 olarak
// ulaşır ve "her değer dizedir" kuralı tam da bunu önlemek için vardır. Tip
// uymuyorsa sessizce boş kimlikle devam etmek, hiç var olmayan bir sipariş için
// bildirim denemesi üretirdi; hata dönmek sözleşmenin bozulduğunu logda görünür
// kılar.
func olayOrderID(e eventbus.Event) (string, error) {
	ham, ok := e.Data[eventFieldOrderID]
	if !ok {
		return "", errors.Invalid(CodeEventInvalid,
			"%q olayının yükünde %q alanı yok", e.Name, eventFieldOrderID)
	}

	id, ok := ham.(string)
	if !ok {
		return "", errors.Invalid(CodeEventInvalid,
			"%q olayındaki %q alanı dize olmalı (gelen tip: %T)", e.Name, eventFieldOrderID, ham)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.Invalid(CodeEventInvalid,
			"%q olayındaki %q alanı boş", e.Name, eventFieldOrderID)
	}
	return id, nil
}

// cozContact sipariş yüzeyinin yanıtını çözer.
//
// Boş bir order_id, yüzeyin şemasının değiştiğinin işaretidir ve HATA verir:
// kimliksiz bir gövdeyle devam etmek, şablonu boş alanlarla doldurup müşteriye
// göndermek olurdu. E-postanın boş olması ise hata DEĞİLDİR; kararı
// [Service.Notify] verir.
func cozContact(ham json.RawMessage, orderID string) (orderContact, error) {
	var kisi orderContact
	if err := json.Unmarshal(ham, &kisi); err != nil {
		return orderContact{}, errors.Wrap(err, errors.KindInternal, CodeContactInvalid,
			"sipariş iletişim yanıtı çözümlenemedi (%s); %q yüzeyinin şeması değişmiş olabilir",
			orderID, OrderInteropName)
	}
	if strings.TrimSpace(kisi.OrderID) == "" {
		return orderContact{}, errors.Internal(CodeContactInvalid,
			"sipariş iletişim yanıtında %q alanı yok (%s); %q yüzeyinin şeması değişmiş olabilir",
			"order_id", orderID, OrderInteropName)
	}
	return kisi, nil
}
