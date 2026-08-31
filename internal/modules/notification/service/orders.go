package service

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya modülün SİPARİŞE bakan tek yüzüdür (ADR 0001, ADR 0006).
//
// Modül order'ı import edemez, dolayısıyla onun tiplerini adlandıramaz. Erişim
// üç parçadan oluşur ve üçü de burada durur:
//
//  1. İhtiyaç duyulan DAR arayüz bu pakette tanımlanır ([OrderContactReader]).
//  2. Somut yüzey container'dan ADLA çözülür ("order.interop").
//  3. Taşınan veri JSON'dur; şema [orderContact] belgesinde AÇIKÇA yazılıdır.

// OrderInteropName sipariş modülünün İLKEL okuma yüzeyinin container'daki
// adıdır (order.InteropName ile AYNI değer).
//
// Değer elle tekrarlanmıştır çünkü modüller birbirini import edemez
// (Prensip 2.4); tıpkı çekirdeğin coreplugin.NotificationProvidersName'i elle
// tekrarlaması gibi. Ayrışmanın bedeli somuttur: ad değişirse bu modül hiçbir
// siparişin iletişim bilgisini okuyamaz ve her sipariş bildirimi hata
// dönerdi — derleyici bunu yakalayamaz, ancak entegrasyon testi yakalar.
const OrderInteropName = "order.interop"

// OrderContactReader modülün siparişten istediği DAR yüzeydir.
//
// Tüketici tarafında tanımlanır ve order'ın "order.interop" kaydı onu YAPISAL
// olarak karşılar; iki taraf arasında derleme zamanı bağı YOKTUR ve olamaz
// (Prensip 2.4). İmzanın ilkel ve stdlib tipleriyle konuşması bu yüzden
// zorunludur: order'ın bir tipi adlandırılsaydı, o tip burada tanımlanmış
// BAŞKA bir tip olur ve somut yüzey bu arayüzü karşılamazdı.
type OrderContactReader interface {
	OrderContactJSON(ctx context.Context, orderID string) (json.RawMessage, error)
}

// orderContact "order.interop" yanıtının JSON şemasıdır.
//
//	{
//	  "order_id":      "order_01H…",
//	  "display_id":    "1042",       // ondalıksız DİZE
//	  "email":         "a@b.com",    // BOŞ olabilir
//	  "currency_code": "TRY",
//	  "total":         "6100",       // minor unit, ondalıksız DİZE
//	  "item_count":    "2"           // ondalıksız DİZE
//	}
//
// TÜM değerler dizedir ve alan adları "order.placed" olayının yüküyle birebir
// aynıdır; gerekçe order modülünün yüzey belgesindedir. Burada tekrarlanan tek
// şey ŞEMADIR, kural değil.
//
// Bilinmeyen alanlar YOK SAYILIR (json.Unmarshal'ın varsayılanı): gövdeyi
// üreten taraf çağıran değil siparişin kendisidir, yani tanınmayan bir alan bir
// yazım hatası değil, yüzeye eklenmiş yeni bir alandır. Onu hata saymak,
// order'a eklenen her alanın tüm sipariş bildirimlerini düşürmesi demekti.
type orderContact struct {
	OrderID      string `json:"order_id"`
	DisplayID    string `json:"display_id"`
	Email        string `json:"email"`
	CurrencyCode string `json:"currency_code"`
	Total        string `json:"total"`
	ItemCount    string `json:"item_count"`
}

// NewOrderContacts container üzerinde TEMBEL çalışan bir sipariş okuyucusu
// üretir.
//
// Tembellik zorunludur: modüllerin Register sırası garanti edilmez ve bu modül
// Register olurken "order.interop" henüz container'da olmayabilir (bkz.
// module.Module belgesi). Çözüm ilk kullanıma, yani ilk "order.placed"
// olayına ertelenir.
//
// Dönüş tipi ARAYÜZDÜR: çağıranın (module.go) somut tipe ihtiyacı yoktur ve
// servis zaten bu arayüzü ister; testlerde yerine birkaç satırlık bir sahte
// konur.
func NewOrderContacts(c *container.Container) OrderContactReader {
	return &lazyOrderContacts{c: c}
}

// lazyOrderContacts "order.interop" yüzeyine tembel erişimdir.
type lazyOrderContacts struct {
	// c kaydın aranacağı container'dır; nil olabilir (gömülü kullanım/test).
	c *container.Container

	// mu okuyucunun tek kez çözülmesini sağlar.
	//
	// sync.Once BİLİNÇLİ olarak kullanılmadı: Once, ilk çağrının SONUCUNU da
	// kalıcı kılar ve order henüz kayıtlı değilken düşen tek bir çözüm, süreç
	// ömrü boyunca tüm bildirimleri ölü bırakırdı. Kilit yalnızca BAŞARILI
	// sonucu saklar; hata bir sonraki olayda yeniden denenir.
	mu      sync.Mutex
	okuyucu OrderContactReader
}

// OrderContactJSON siparişin iletişim bilgisini ham JSON olarak döner.
func (l *lazyOrderContacts) OrderContactJSON(
	ctx context.Context,
	orderID string,
) (json.RawMessage, error) {
	okuyucu, err := l.coz()
	if err != nil {
		return nil, err
	}
	return okuyucu.OrderContactJSON(ctx, orderID)
}

// coz sipariş yüzeyini container'dan çözer ve sonucu saklar.
func (l *lazyOrderContacts) coz() (OrderContactReader, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.okuyucu != nil {
		return l.okuyucu, nil
	}
	if l.c == nil {
		return nil, errors.Unavailable(CodeContactUnavailable,
			"container yok; %q yüzeyi çözülemez", OrderInteropName)
	}

	okuyucu, err := container.Resolve[OrderContactReader](l.c, OrderInteropName)
	if err != nil {
		// Sınıf KORUNUR: kayıt yoksa NotFound, tip uymuyorsa Internal gelir ve
		// ikisi farklı arızalardır — biri "order kurulu değil", öteki
		// "yüzeyin imzası değişmiş".
		return nil, errors.Wrap(err, errors.KindOf(err), CodeContactUnavailable,
			"sipariş okuma yüzeyi %q çözülemedi; order modülü kurulu mu?", OrderInteropName)
	}

	l.okuyucu = okuyucu
	return okuyucu, nil
}
