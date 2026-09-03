// Package searchpg gobit'e ürün araması ekleyen eklentidir.
//
// # Arama motoru DIŞ BİR SERVİS DEĞİLDİR
//
// İndeks ve sorgu PostgreSQL'in tam metin aramasıdır: belge bir tsvector
// sütununda yaşar, eşleşme GIN indeksiyle bulunur, sıralama ts_rank ile
// yapılır (neden ts_rank_cd değil: bkz. [searchSQL], ölçümle birlikte).
// Meilisearch/OpenSearch bilinçli olarak SEÇİLMEDİ — ikisi de yeni bir
// dış bağımlılık, yeni bir compose servisi, yeni bir sağlık kontrolü ve yeni
// bir "indeks ile veritabanı ayrıştı" arıza sınıfı getirirdi. Zaten var olan
// PostgreSQL, ölçek büyümeden önce GERÇEK bir arama verir.
//
// Karar geri alınabilirdir ve eklenti sınırının değeri tam olarak budur:
// motoru değiştirmek YALNIZCA bu paketi değiştirir. Ne çekirdek, ne product
// modülü, ne de başka bir modül bu eklentinin var olduğunu bilir; kurulum
// dosyasındaki tek satır ve PLUGINS ortam değişkeni dışında hiçbir yerde adı
// geçmez.
//
// # Kullandığı üç uzatma noktası
//
//  1. [coreplugin.Host.AddModule] — eklenti KENDİ modülünü getirir: kendi
//     tablosu, kendi migration'ı, kendi sürüm defteri ("searchpg") ve kendi
//     route'ları. Modül, çekirdek modüllerle AYNI yaşam döngüsünden geçer.
//  2. [coreplugin.Host.Subscribe] — "product.created", "product.updated" ve
//     "product.deleted" olaylarını dinleyip indeksi taze tutar.
//  3. Modülün Routes'u — GET /store/v1/search ve
//     POST /admin/v1/search/reindex uçlarını açar.
//
// # Hiçbir modülü import ETMEZ
//
// Eklenti product'ı import edemez (internal/arch TestEklentilerModulleriImportEtmez).
// Katalog kaydına, bu pakette tanımlı [StoreProductReader] dar arayüzüyle ve
// container'dan ADLA ("product.interop") ulaşır; çözüm TEMBELDİR, çünkü Setup
// anında hiçbir modül henüz ayağa kalkmamıştır (bkz. [katalog.coz]).
//
// # Kanal süzmesi burada TEKRARLANMAZ
//
// Hangi ürünün hangi satış kanalında görüneceği kataloğun kuralıdır. Eklenti
// indeksten yalnızca ALAKA SIRALI KİMLİK üretir; gösterilecek kayıtları
// "product.interop" üzerinden ister ve kanal kimliklerini isteğe ekler.
// Süzgeci burada yeniden yazmak, kuralın ikinci bir tanımını üretir ve iki
// tanım ayrıştığı gün arama, kanal süzmesinin BYPASS'ı hâline gelirdi.
//
// # Metin arama yapılandırması: 'simple'
//
// Belgeler ve sorgular 'simple' sözlüğüyle üretilir, yani kök bulma (stemming)
// ve durak kelime atma YOKTUR. Alternatif olan 'english', Türkçe bir katalogda
// kelimeleri yanlış köklere indirir ("kalemler" -> "kalemler" değil, İngilizce
// kurallarıyla budanır) ve PostgreSQL'in gömülü bir Türkçe sözlüğü yoktur.
// Kurulumun dilini bilmeyen bir çerçevede, yanlış dilde kök bulmaktansa hiç
// kök bulmamak öngörülebilirdir. Bedeli açıktır ve kabul edilmiştir: "kalem"
// araması "kalemler" yazan ürünü BULMAZ. Bu sınır aşıldığında doğru adım
// buraya bir sözlük ayarı sızdırmak değil, motoru değiştirmektir.
//
// Büyük/küçük harf katlaması PostgreSQL'in ctype ayarına bağlıdır: C locale ile
// kurulmuş bir kümede ASCII DIŞI harfler katlanmaz ve "Gömlek" araması "gömlek"
// yazan ürünü bulmaz. Kümenin UTF-8 bir locale ile kurulmuş olması bu yüzden
// aramanın bir ön koşuludur.
//
// # Kullanım
//
//	PLUGINS=search-pg
//
// Ayrı bir yapılandırma istemez; indeks tablosu açılışta migration ile kurulur.
// Boş bir indeks hiçbir şey döndürmez, bu yüzden var olan bir katalogda ilk
// adım POST /admin/v1/search/reindex çağırmaktır.
package searchpg

import (
	"context"

	coreplugin "github.com/bdrtr/gobit/internal/core/plugin"
)

// Name eklentinin kayıttaki adıdır; PLUGINS listesine bu ad yazılır.
const Name = "search-pg"

// ModuleName eklentinin getirdiği modülün adıdır.
//
// Eklenti adından FARKLIDIR ve olmak zorundadır: modül adı doğrudan bir SQL
// tablo adına ("searchpg_schema_migrations") dönüşür ve core/db'nin sahip
// deseni tireye izin vermez. Ad ayrıca yönetim ucunun istediği yetkinin de
// önekidir (bkz. [ScopeWrite]).
const ModuleName = "searchpg"

// Bu blok modüller arası SÖZLEŞMEDİR ve değerleri ELLE tekrarlanmıştır.
//
// Eklenti hiçbir modülü import edemediği için (ADR 0001) bu adlar product'ın
// sabitlerine bağlanamaz; tıpkı çekirdeğin coreplugin.PaymentProvidersName'i
// elle tekrarlaması gibi. Elle tekrarlanan her sabit sessizce ayrışmaya
// açıktır ve buradaki ayrışmanın bedeli somuttur: olay adı değişirse eklenti
// hiç olay almaz, interop adı değişirse arama ucu her istekte 503 döner.
// Hiçbir derleyici bunu yakalamaz — arch testleri eklentiyi bu yönden
// denetlemiyor (bkz. paket testlerindeki not).
const (
	// catalogInteropName product modülünün İLKEL okuma yüzeyinin
	// container'daki adıdır (product.InteropName).
	catalogInteropName = "product.interop"
	// catalogEntity ürünlerin Query katmanındaki entity adıdır; yeniden
	// indeksleme kimlikleri bu adla sayfalar (product service.EntityProduct).
	catalogEntity = "product"
	// catalogStatusFilter Query sağlayıcısının yayın durumu filtresidir.
	catalogStatusFilter = "status"
	// catalogStatusPublished vitrinde görünen tek yayın durumudur.
	catalogStatusPublished = "published"
	// eventProductCreated yeni ürün olayıdır (product service.EventProductCreated).
	eventProductCreated = "product.created"
	// eventProductUpdated ürün güncelleme olayıdır.
	eventProductUpdated = "product.updated"
	// eventProductDeleted ürün silme olayıdır.
	eventProductDeleted = "product.deleted"
	// eventFieldProductID olay yükündeki ürün kimliği anahtarıdır.
	eventFieldProductID = "product_id"
)

// Container'da çözülen ÇEKİRDEK servislerin adları.
const (
	svcDB    = "core.db"
	svcQuery = "core.query"
)

// Plugin PostgreSQL tabanlı arama eklentisidir.
type Plugin struct {
	// mod eklentinin getirdiği modüldür; abonelikler de onun metodlarıdır.
	// Setup'ta kurulur, [modul.Register] ile tamamlanır.
	mod *modul
}

// Eklentinin çekirdek sözleşmesini karşıladığı derleme zamanında sabitlenir.
var _ coreplugin.Plugin = (*Plugin)(nil)

// New eklentiyi kurar.
func New() *Plugin { return &Plugin{} }

// Name eklentinin adını döner.
func (p *Plugin) Name() string { return Name }

// Setup modülü kayda ekler ve katalog olaylarına abone olur.
//
// Yapılandırma İSTEMEZ ve bu yüzden hiçbir ayarı doğrulamaz; paymentstripe'ın
// aksine burada "eksikse açılışı durdur" denecek bir ayar yoktur (bkz. paket
// belgesi).
//
// # Burada container'dan hiçbir şey ÇÖZÜLMEZ
//
// Setup, modüller ayağa kalkmadan ÖNCE çalışır: "product.interop" bu anda
// container'da yoktur ve çözmeye çalışmak açılışı, hiçbir şeyin gerçekten
// eksik olmadığı bir hatayla düşürürdü. Kayıt yalnızca modülü ve abonelikleri
// bildirir; katalog erişimi ilk kullanımda çözülür (bkz. [katalog.coz]).
//
// # Abonelik NEDEN Host üzerinden
//
// Veri yolu doğrudan alınıp Subscribe çağrılsaydı, abonelik product modülü
// Register olmadan kurulurdu ve ilk olay, indeks tablosu henüz göçürülmemişken
// gelebilirdi. [coreplugin.Host.Subscribe] kaydı KUYRUĞA alır ve modüller
// ayağa kalktıktan sonra uygular.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	p.mod = newModul(h.Container(), h.Logger())

	// 1. uzatma noktası: eklenti KENDİ modülünü getirir.
	h.AddModule(p.mod)

	// 2. uzatma noktası: katalog olayları. Yazma ve güncelleme AYNI işleyiciye
	// gider: ikisinde de doğru davranış "kaydı oku, indekse yaz"dır ve iki ayrı
	// işleyici yazmak aynı kodun ikinci kopyasını üretirdi.
	h.Subscribe(eventProductCreated, p.mod.urunYazildi)
	h.Subscribe(eventProductUpdated, p.mod.urunYazildi)
	h.Subscribe(eventProductDeleted, p.mod.urunSilindi)

	h.Logger().Info("arama eklentisi kuruldu",
		"modul", ModuleName,
		"arama_ucu", SearchPath,
		"yeniden_indeksleme_ucu", ReindexPath)

	return nil
}
