// Package query modüller arası okumayı tek çağrıya indiren sorgu katmanıdır
// (plan Bölüm 5.3, Faz 2).
//
// Akış her zaman aynıdır: kök entity'nin kayıtları çekilir, link'ler üzerinden
// ilgili kimlikler bulunur, ilgili modüllerden BATCH olarak getirilir ve sonuç
// tek bir kayıt ağacında birleştirilir.
//
// # Modülleri tanımadan veri çekmek
//
// Çekirdek, Prensip 2.4 gereği modülleri import edemez. Bu yüzden Query hangi
// modüle sorduğunu derleme zamanında bilmez: her modül kendi [Provider]
// uygulamasını container'a "<modül adı>.query" adıyla kaydeder, Query onu
// ADLA çözer (bkz. ADR 0004). Arayüzü tüketen taraf (bu paket) tanımlar,
// sağlayan modül yalnızca imzayı karşılar ve hiçbir şey import etmez
// (bkz. ADR 0001).
//
// Sağlayıcı kaydı unutulmuşsa [Query.Graph] errors.KindNotFound döner ve
// mesaja ARANAN ADI yazar; teşhis edilebilirlik bu katmanda kritiktir.
//
// # N+1 yasağı
//
// Genişletme başına sağlayıcıya YALNIZCA BİR çağrı yapılır. O seviyedeki tüm
// kayıtların kimlikleri önce toplanır, link'ler tek [link.LinkService.ListMany]
// çağrısıyla çözülür ve hedef modülden tek bir [Provider.FetchByIDs] yapılır.
// Bu, iç içe genişletmelerde de geçerlidir: maliyet kayıt sayısıyla değil,
// genişletme sayısıyla büyür. Yüz kök kayıt da bir kök kayıt da genişletme
// başına aynı sayıda çağrı üretir.
//
// # Birleştirme anahtarı
//
// Query kayıtları [IDField] ("id") alanı üzerinden birleştirir: kök kaydın bu
// alanı link tablosuna giden kimliktir, getirilen kaydın bu alanı da geri
// eşlemede kullanılır. Sağlayıcılar birincil anahtarlarını bu adla sunmalıdır.
//
// Anahtar olduğu için korunur: bir genişletmenin çıktı anahtarı [IDField]
// OLAMAZ (errors.KindInvalid), çünkü sonuç kaydın kimliğinin üzerine yazılırdı.
// Kimliği okunamayan bir kayıt da — alan yoksa, string değilse ya da boşsa —
// sessizce atlanmaz; errors.KindInternal döner ve mesaj kimliğin NEDEN
// okunamadığını (gelen tipi) yazar.
//
// [link.LinkSide.Field] BİLİNÇLİ OLARAK kullanılmaz. O alan ("product_id" gibi)
// kimliğin sahibi modüldeki anlamını bildiren üstveridir; sağlayıcı kaydında
// böyle bir alan bulunmak zorunda değildir ve alan seçimi (Fields) yapılan bir
// istekte sağlayıcıdan istenirse tanımadığı alan için errors.KindInvalid
// dönerdi. Birleştirme bu yüzden tek ve öngörülebilir bir anahtara bağlanmıştır.
//
// # Yön
//
// Bir genişletmede kök entity'nin link'in From ucunda olduğu VARSAYILMAZ. Yön,
// link tanımının uçlarına bakılarak çözülür: kök entity From ucundaysa ileri
// yönde (From -> To), To ucundaysa ters yönde (To -> From) gidilir. Link'in iki
// ucu da kök entity değilse errors.KindInvalid döner.
//
// İki yön de [link.LinkService] sözleşmesindeki TOPLU metotlarla çözülür: ileri
// yön ListMany, ters yön ListManyByTo. Kayıt başına link sorgusu yoktur.
//
// # Sonucun şekli
//
// Genişletmenin sonuca yazılma biçimini link'in kardinalitesi ve gidilen YÖN
// birlikte belirler: tek kayıt yazılan bir uçta [Record] (eşleşme yoksa nil),
// çok kayıt yazılan bir uçta []Record (eşleşme yoksa boş dilim) yazılır.
// [link.OneToMany] yönlü bir kardinalitedir — "bir From, çok To" demektir — bu
// yüzden ileri yönde dilim, ters yönde tek kayıt yazar.
//
// # Kayıt sahipliği
//
// Sağlayıcıdan gelen kayıtlar KOPYALANIR; [Query.Graph]'ın döndürdüğü ağaçtaki
// her [Record] çağrıya aittir. Query genişletme sonucunu kaydın içine yazdığı
// için, kopyalamayan bir sağlayıcının kendi durumu aksi hâlde kirlenirdi
// (yabancı anahtar sızması, bayat alan, eşzamanlı çağrılarda veri yarışı).
// Sağlayıcı kayıtlarını paylaşsa da paylaşmasa da doğru çalışır; kopya
// yüzeyseldir, Query alan DEĞERLERİNE hiç dokunmaz.
//
// # Sınırlar
//
// Bir spec en fazla [maxExpandDepth] seviye derin ve toplam [maxExpansions]
// genişletme taşıyabilir; ikisi de errors.KindInvalid ile ve sağlayıcıya hiç
// gidilmeden uygulanır. Genişletme ağacının link adları, yönü, kardinalitesi ve
// hedef sağlayıcı kayıtları da VERİ GETİRİLMEDEN önce çözülür: bozuk bir sorgu
// tanımı, üst seviye hiç kayıt getirmese bile aynı hatayı verir.
//
// # Hata politikası
//
// Kısmi sonuç YOKTUR. Herhangi bir seviyedeki herhangi bir sağlayıcı ya da link
// çağrısı hata verirse [Query.Graph] hata döner; eksik veriyle dolu bir kayıt
// ağacı dönmez. Kök kayıt bulunamaması hata DEĞİLDİR; boş (nil olmayan) dilim
// döner.
//
// Alttaki hatanın sınıfı korunur. Tipsiz bir iptal (context.Canceled /
// context.DeadlineExceeded) errors.KindUnavailable'a eşlenir; sunucu hatasıyla
// karışmaması ve API sınırında 500 yerine 503 üretmesi içindir.
package query

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/link"
)

// IDField sağlayıcı kayıtlarında kimliğin bulunduğu alan adıdır.
//
// Query kayıtları link'lerle bu alan üzerinden eşleştirir. Alan seçimi yapılan
// (Fields dolu) bir istekte genişletme de varsa Query bu alanı sağlayıcıya
// giden alan listesine KENDİSİ ekler; dolayısıyla genişletilen kayıtlarda bu
// alan çağıran istemese de bulunur.
const IDField = "id"

// ProviderSuffix modüllerin sağlayıcılarını container'a kaydettiği ad ekidir.
// Bir entity'nin sağlayıcısı "<entity><ProviderSuffix>" adıyla aranır (ADR 0004).
const ProviderSuffix = ".query"

// maxExpandDepth iç içe genişletme için üst sınırdır. Sınır, dışarıdan gelen
// bir sorgu tanımının çekirdeği keyfi derinlikte özyinelemeye zorlamasını
// engeller; aşılırsa errors.KindInvalid döner.
const maxExpandDepth = 10

// maxExpansions bir spec'teki TOPLAM genişletme sayısının üst sınırıdır.
//
// Maliyet derinlikle değil genişletme ADEDİYLE büyür: her genişletme sabit iki
// tur üretir (bir link çözümü + bir FetchByIDs). Yalnızca derinliği sınırlamak
// korumanın yarısıdır; sınırın altında kalan ama her seviyede onlarca kardeş
// genişletme taşıyan tek bir istek yüzlerce gidiş-dönüş açabilirdi. Ağaçtaki
// toplam sayı bu sınırı aşarsa errors.KindInvalid döner.
const maxExpansions = 50

// Record bir kaydın alan adı -> değer eşlemesidir.
//
// Gevşek tiplilik, çekirdeğin modül modellerini tanımamasının kaçınılmaz
// bedelidir (ADR 0004); tip güvenliği API sınırında yeniden kazanılır.
type Record map[string]any

// ListOptions kök kayıtların çekilmesi için sağlayıcıya verilen seçeneklerdir.
type ListOptions struct {
	// Fields döndürülecek alanlardır. Boş bırakılırsa sağlayıcının varsayılan
	// alan kümesi döner.
	Fields []string
	// Filters alan adı -> beklenen değer biçiminde filtrelerdir. Yorumu
	// sağlayıcıya aittir; desteklemediği bir filtre için sağlayıcı
	// errors.KindInvalid dönmelidir.
	Filters map[string]any
	// Limit döndürülecek en fazla kayıt sayısıdır; 0 sınırsız demektir.
	Limit int
	// Offset atlanacak kayıt sayısıdır.
	Offset int
}

// Provider bir modülün Query katmanına açtığı okuma yüzeyidir.
//
// Modül bunu Register sırasında "<modül adı>.query" adıyla container'a koyar
// (ADR 0004). Arayüz bu pakette tanımlıdır; sağlayan modül bu paketi import
// etmek zorunda değildir, yalnızca imzayı karşılar.
//
// Query dönen kayıtları KOPYALAR ve genişletme sonucunu kopyaya yazar; bu
// yüzden sağlayıcı döndürdüğü haritaları kendi durumuyla paylaşabilir
// (bkz. paket yorumundaki "Kayıt sahipliği").
type Provider interface {
	// Entity sağlayıcının sunduğu entity adıdır (örn. "product"). Kayıt adının
	// öneki ile aynı olmalıdır; Query bunu doğrular.
	Entity() string

	// List kök kayıtları döner. Query bunu YALNIZCA kök entity için çağırır.
	List(ctx context.Context, opts ListOptions) ([]Record, error)

	// FetchByIDs verilen kimliklere karşılık gelen kayıtları döner.
	// Bulunamayan kimlik için kayıt DÖNMEZ; bu bir hata değildir.
	// Query bunu link'lerden çıkan kimlik kümesiyle BATCH olarak çağırır.
	FetchByIDs(ctx context.Context, ids []string, fields []string) ([]Record, error)
}

// Expansion bir link üzerinden yapılan tek bir genişletmedir.
type Expansion struct {
	// Link genişletmede kullanılacak link tanımının adıdır (örn. "product_price").
	Link string
	// As sonucun kök kayda hangi anahtarla yazılacağıdır; boşsa Link kullanılır.
	As string
	// Fields genişletilen kayıtlardan istenen alanlardır; boşsa sağlayıcının
	// varsayılanı gelir.
	Fields []string
	// Expand bu genişletmenin üstüne uygulanacak iç içe genişletmelerdir.
	// Her seviye kendi içinde yine BATCH çözülür.
	Expand []Expansion
}

// GraphSpec tek bir cross-module okumanın tanımıdır.
type GraphSpec struct {
	// Entity kök entity adıdır; sağlayıcısı "<Entity>.query" adıyla aranır.
	Entity string
	// Fields kök kayıtlardan istenen alanlardır; boşsa sağlayıcının varsayılanı.
	Fields []string
	// Filters kök kayıtlara uygulanacak filtrelerdir.
	Filters map[string]any
	// Limit kök kayıt sayısı üst sınırıdır; 0 sınırsız demektir.
	Limit int
	// Offset atlanacak kök kayıt sayısıdır.
	Offset int
	// Expand kök kayıtlar üzerine uygulanacak genişletmelerdir.
	Expand []Expansion
}

// Query modüllerden veriyi alıp link'ler üzerinden birleştiren okuma katmanıdır.
type Query interface {
	// Graph spec'e göre kök kayıtları çeker ve genişletmeleri uygular.
	// Kök kayıt yoksa boş (nil olmayan) dilim ve nil hata döner.
	Graph(ctx context.Context, spec GraphSpec) ([]Record, error)
}

// resolver [Query]'nin tek uygulamasıdır.
type resolver struct {
	links link.LinkService
	c     *container.Container
	log   *slog.Logger
}

var _ Query = (*resolver)(nil)

// New verilen link servisi ve container üzerinde çalışan bir [Query] üretir.
//
// links link tanımlarını ve bağları çözer; c sağlayıcıları "<entity>.query"
// adıyla barındırır. log nil verilirse loglar atılır.
//
// links veya c nil ise bu, kurulumda değil ilk [Query.Graph] çağrısında tipli
// bir hata olarak bildirilir; kurulum yolu panik üretmez.
func New(links link.LinkService, c *container.Container, log *slog.Logger) Query {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &resolver{links: links, c: c, log: log}
}
