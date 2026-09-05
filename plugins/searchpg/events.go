package searchpg

import (
	"context"
	"encoding/json"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
)

// Bu dosya indeksi TAZE tutan abonelerdir.
//
// # Hata politikası: hata DÖNÜLÜR, ama bu bir "yeniden dene" isteği DEĞİLDİR
//
// [eventbus.EventBus] sözleşmesi açıktır: handler hata dönerse hata loglanır ve
// olay İŞLENMİŞ SAYILIR; hiçbir backend yeniden teslim etmez. Redis backend'i
// mesajı, handler'ın sonucundan BAĞIMSIZ olarak ACK'ler (bkz. redisBus.dispatch
// içindeki defer'lı ack). Yani "hatayı dönersem veri yolu tekrar dener" cümlesi
// bu çerçevede YANLIŞTIR ve buna güvenen bir işleyici, kaçırdığı olayı sonsuza
// dek kaçırırdı.
//
// O hâlde neden yine de hata dönüyoruz? Çünkü hatayı YUTMAK (nil dönmek) tek
// gerçek maliyeti olan seçenektir: veri yolu hata dönen bir handler'ı olay adı,
// olay kimliği ve hata zinciriyle birlikte ERROR seviyesinde loglar. nil
// dönseydik indeksin geride kaldığı hiçbir yerde görünmezdi.
//
// Kendi içinde yeniden deneme de yapılmaz. Sözleşme buna izin verir ama burada
// yanlış olurdu: katalog erişilemezken bekleyen bir handler, InMemory
// backend'inde goroutine biriktirir, Redis backend'inde ise tek tüketici
// döngüsünü bloklayıp AYNI stream'deki tüm olayları geciktirir. Bir olayın
// gecikmesi, tüm katalog akışının durmasından ucuzdur.
//
// Kabul edilen bedel: kaçan olay indeksi kaydın gerisinde bırakır. Onarım yolu
// vardır ve elle çağrılır — POST /admin/v1/search/reindex (bkz.
// [modul.yenidenIndeksle]). Otomatik onarım (outbox ya da tarama işi) bilinçli
// olarak yazılmadı; eklentinin kapsamı bir arama indeksidir, ikinci bir
// güvenilir teslim mekanizması değil.
//
// # İşleyiciler İDEMPOTENT ve YENİDEN GİRİŞE UYGUNDUR
//
// Sözleşme sırayı garanti etmez ve InMemory backend'i aynı handler'ı eşzamanlı
// çağırabilir. Her iki yol da tek bir ifadeye indirgenmiştir (upsert ya da
// koşulsuz delete), yani aynı olayın iki kez işlenmesi ile bir kez işlenmesi
// AYNI sonucu verir.

// codeEventInvalid olay yükünün sözleşmeye uymadığını bildirir.
const codeEventInvalid = "searchpg_event_payload_invalid"

// urunGosterimi vitrin kaydının İNDEKSLENEN alanlarıdır.
//
// Kaydın tamamı burada tanımlanmaz ve tanınmayan alanlar SESSİZCE yok sayılır
// (json.Unmarshal'ın varsayılanı). Bu, isteklerdeki DisallowUnknownFields
// kuralının tersidir ve bilinçlidir: burada gövdeyi ÜRETEN taraf çağıran değil
// kataloğun kendisidir, yani tanınmayan bir alan bir yazım hatası değil,
// product'a eklenmiş yeni bir alandır. Onu hata saymak, katalog her
// büyüdüğünde indekslemeyi düşürürdü.
type urunGosterimi struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	Description string `json:"description"`
	Variants    []struct {
		Title string `json:"title"`
		SKU   string `json:"sku"`
	} `json:"variants"`
	Tags []struct {
		Value string `json:"value"`
	} `json:"tags"`
}

// urunYazildi "product.created" ve "product.updated" olaylarını işler.
//
// # Yükteki status BİLİNÇLİ OLARAK kullanılmaz
//
// Olay, ürünün o ANDAKİ yayın durumunu taşır ve bir aboneye "okumaya değmeyecek
// olayları ucuza eleme" hakkı verir: taslak ürünler üzerinde yapılan toplu bir
// güncellemede kaydı hiç okumadan "indeksten düşür" denebilirdi.
//
// Burada o kısayol ALINMADI çünkü değer BAYAT olabilir ve veri yolu sıra
// garantisi vermez. Somut arıza şudur: bir ürün önce taslağa alınıp hemen
// ardından yayımlanırsa, iki olay ters sırada teslim edildiğinde kısayol
// ürünü indeksten düşürür ve bir sonraki yazmaya kadar aramada GÖRÜNMEZ olur —
// üstelik hiçbir hata üretmeden. Kaydı her olayda okumak bu yarışı ortadan
// kaldırır: karar, olayın söylediğine değil kataloğun O ANKİ durumuna dayanır
// ve son çalışan işleyici her zaman en taze gerçeği yazar.
//
// Ödenen bedel, taslak ürün güncellemelerinde de bir okuma yapılmasıdır. Bu
// yol sıcaklaşırsa doğru adım status'e güvenmek değil, olayları toplu işleyen
// bir tampon eklemektir.
func (m *modul) urunYazildi(ctx context.Context, e eventbus.Event) error {
	id, err := olayUrunID(e)
	if err != nil {
		return err
	}
	if err := m.hazir(); err != nil {
		return err
	}

	belgeler, err := m.belgeler(ctx, []string{id})
	if err != nil {
		return err
	}

	// Katalog kaydı DÖNMEDİ: ürün yayından kalkmış, arşivlenmiş ya da olay ile
	// bu okuma arasında silinmiştir. İndeks vitrinin aynası olduğu için doğru
	// eylem yazmak değil DÜŞÜRMEKTİR; aksi hâlde yayından kaldırılan bir ürün
	// aramada görünmeye devam ederdi.
	if len(belgeler) == 0 {
		silinen, err := m.indeks.Delete(ctx, id)
		if err != nil {
			return err
		}
		m.log.DebugContext(ctx, "ürün vitrinde görünmüyor; indeksten düşürüldü",
			"event", e.Name, "product_id", id, "silinen", silinen)

		return nil
	}

	if err := m.indeks.Upsert(ctx, belgeler); err != nil {
		return err
	}
	m.log.DebugContext(ctx, "ürün indekslendi", "event", e.Name, "product_id", id)

	return nil
}

// urunSilindi "product.deleted" olayını işler.
//
// Katalog HİÇ OKUNMAZ: soft silinmiş kayıt zaten hiçbir okumadan dönmez, yani
// okuma turu her zaman boş sonuçla biterdi. Silme ayrıca duruma da bakmaz —
// bu yüzden olayın yükünde status bulunmaması bir eksiklik değildir.
func (m *modul) urunSilindi(ctx context.Context, e eventbus.Event) error {
	id, err := olayUrunID(e)
	if err != nil {
		return err
	}
	if err := m.hazir(); err != nil {
		return err
	}

	silinen, err := m.indeks.Delete(ctx, id)
	if err != nil {
		return err
	}
	m.log.DebugContext(ctx, "silinen ürün indeksten düşürüldü",
		"event", e.Name, "product_id", id, "silinen", silinen)

	return nil
}

// olayUrunID olay yükünden ürün kimliğini okur.
//
// Değerin DİZE olması sözleşmedir (bkz. product service/events.go): Redis
// backend'i yükü JSON'a çevirdiği için sayısal bir alan aboneye float64 olarak
// ulaşır ve "her değer dizedir" kuralı tam da bunu önlemek için vardır. Tip
// uymuyorsa sessizce boş kimlikle devam etmek indekse çöp yazardı; hata
// dönmek, sözleşmenin bozulduğunu logda görünür kılar.
func olayUrunID(e eventbus.Event) (string, error) {
	ham, ok := e.Data[eventFieldProductID]
	if !ok {
		return "", coreerrors.Invalid(codeEventInvalid,
			"%q olayının yükünde %q alanı yok", e.Name, eventFieldProductID)
	}

	id, ok := ham.(string)
	if !ok {
		return "", coreerrors.Invalid(codeEventInvalid,
			"%q olayındaki %q alanı dize olmalı (gelen tip: %T)", e.Name, eventFieldProductID, ham)
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return "", coreerrors.Invalid(codeEventInvalid,
			"%q olayındaki %q alanı boş", e.Name, eventFieldProductID)
	}

	return id, nil
}

// belgeler verilen kimliklerin katalog kayıtlarını okuyup arama belgesine
// çevirir.
//
// Kanal kimliği GEÇİLMEZ (nil): indeks kanaldan bağımsızdır, süzme okuma
// anında katalogta yapılır (bkz. [katalogIstegi]).
//
// Katalogta bulunmayan kimlik için belge üretilmez; dönen dilim istenenden KISA
// olabilir ve bu bir hata değildir.
func (m *modul) belgeler(ctx context.Context, ids []string) ([]belge, error) {
	kayitlar, err := m.katalog.urunler(ctx, ids, nil)
	if err != nil {
		return nil, err
	}

	belgeler := make([]belge, 0, len(kayitlar))
	gorulen := make(map[string]struct{}, len(kayitlar))
	for _, kayit := range kayitlar {
		b, err := belgeKur(kayit)
		if err != nil {
			return nil, err
		}
		// Tekilleştirme ZORUNLUDUR: tek ifadelik upsert, aynı kimliği ikinci
		// kez gördüğünde PostgreSQL tarafından reddedilir. Katalog bugün her
		// kimliği bir kez döner; bu kapı, o davranışa bağlı kalmamak içindir.
		if _, tekrar := gorulen[b.urunID]; tekrar {
			continue
		}
		gorulen[b.urunID] = struct{}{}
		belgeler = append(belgeler, b)
	}

	return belgeler, nil
}

// belgeKur vitrin kaydından ağırlıklandırılmış arama belgesini kurar.
//
// Ağırlık dağılımı [belge] belgesindedir. Kimliksiz bir kayıt HATA döner:
// birincil anahtarı boş bir satır yazmak, indeksi sessizce bozar ve arama
// sonucunda hiçbir zaman çözülemeyecek bir kimlik üretirdi.
func belgeKur(ham json.RawMessage) (belge, error) {
	var urun urunGosterimi
	if err := json.Unmarshal(ham, &urun); err != nil {
		return belge{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogResponse,
			"katalog kaydı çözümlenemedi")
	}
	if strings.TrimSpace(urun.ID) == "" {
		return belge{}, coreerrors.Internal(codeCatalogResponse,
			"katalog kaydında %q alanı yok; %q yüzeyinin şeması değişmiş olabilir",
			"id", catalogInteropName)
	}

	anahtarlar := make([]string, 0, 2+len(urun.Variants)*2+len(urun.Tags))
	anahtarlar = append(anahtarlar, urun.Handle, urun.Subtitle)
	for _, varyant := range urun.Variants {
		anahtarlar = append(anahtarlar, varyant.Title, varyant.SKU)
	}
	for _, etiket := range urun.Tags {
		anahtarlar = append(anahtarlar, etiket.Value)
	}

	return belge{
		urunID:  urun.ID,
		baslik:  urun.Title,
		anahtar: strings.Join(anahtarlar, " "),
		metin:   urun.Description,
	}, nil
}
