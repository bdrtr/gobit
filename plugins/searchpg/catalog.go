package searchpg

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/bdrtr/gobit/internal/core/container"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Bu dosya eklentinin KATALOĞA bakan tek yüzüdür (ADR 0001, ADR 0006).
//
// Eklenti hiçbir modülü import edemez, dolayısıyla product'ın tiplerini
// adlandıramaz. Erişim üç parçadan oluşur ve üçü de burada durur:
//
//  1. İhtiyaç duyulan DAR arayüz bu pakette tanımlanır ([StoreProductReader]).
//  2. Somut yüzey container'dan ADLA çözülür ("product.interop").
//  3. Taşınan veri JSON'dur; şema aşağıda AÇIKÇA yazılıdır.
//
// Ürünün vitrin gösterimi burada YENİDEN TANIMLANMAZ. Arama ucu kayıtları ham
// JSON olarak geçirir (bkz. [katalog.urunler]) ve yalnızca indekslenecek
// alanlar ayrıştırılır ([urunGosterimi]). Gösterimin ikinci bir kopyasını
// tutmak, product'a eklenen bir alanın aramada sessizce kaybolması demekti.

// Hata kodları.
const (
	codeCatalogMissing  = "searchpg_catalog_unavailable"
	codeCatalogRead     = "searchpg_catalog_read_failed"
	codeCatalogResponse = "searchpg_catalog_response_invalid"
)

// StoreProductReader eklentinin katalogdan istediği DAR yüzeydir.
//
// Tüketici tarafında tanımlanır ve product'ın "product.interop" kaydı onu
// YAPISAL olarak karşılar; iki taraf arasında derleme zamanı bağı YOKTUR ve
// olamaz (Prensip 2.4). İmzanın ilkel ve stdlib tipleriyle konuşması bu yüzden
// zorunludur: product'ın bir tipi adlandırılsaydı, o tip burada tanımlanmış
// BAŞKA bir tip olur ve somut yüzey bu arayüzü karşılamazdı.
//
// İstek ve yanıt şemaları [katalogIstegi] ve [katalogYaniti] belgelerindedir.
type StoreProductReader interface {
	StoreProductsByIDsJSON(ctx context.Context, request json.RawMessage) (json.RawMessage, error)
}

// katalogIstegi "product.interop" isteğinin JSON şemasıdır.
//
//	{"ids": ["prod_..."], "sales_channel_ids": ["sc_..."]}
//
// # sales_channel_ids'in nil olması ANLAMLIDIR
//
// Alan katalogta tanımlıdır ve burada YENİDEN YORUMLANMAZ: null (nil dilim)
// "istek kanal kimliği taşımıyor" demektir ve süzgeç uygulanmaz; boş dizi
// "kimlik var ama kanalı yok" demektir ve süzgeç uygulanır. İki durum arasında
// omitempty ile kaybolacak bir fark vardır, bu yüzden alan HER ZAMAN yazılır.
//
// İki çağıran iki farklı değer verir ve ikisi de doğrudur:
//
//   - Arama ucu isteğin KİMLİĞİNDEN gelen kanalları geçirir (bkz. [kanallar]).
//   - Olay işleyicisi nil geçirir: indeks kanaldan BAĞIMSIZDIR, çünkü süzme
//     okuma anında yapılır. Kanal başına ayrı indeks tutmak, aynı ürünü
//     kanal sayısı kadar yazmak ve kanal ataması değiştiğinde indeksi yeniden
//     kurmak demekti.
type katalogIstegi struct {
	IDs             []string `json:"ids"`
	SalesChannelIDs []string `json:"sales_channel_ids"`
}

// katalogYaniti "product.interop" yanıtının JSON şemasıdır.
//
//	{"products": [ <vitrin ürün kaydı>, ... ]}
//
// Kayıtlar HAM bırakılır: arama ucu onları olduğu gibi yazar, indeksleme ise
// yalnızca ihtiyaç duyduğu alanları ayrıştırır. Kaydın tam şeklini bu pakette
// tanımlamak, vitrin gösteriminin ikinci bir kopyasını üretirdi.
type katalogYaniti struct {
	Products []json.RawMessage `json:"products"`
}

// katalog "product.interop" yüzeyine TEMBEL erişimdir.
//
// Tembellik zorunludur: eklenti Setup'ı modüllerden ÖNCE çalışır ve o anda
// container'da böyle bir kayıt yoktur. Çözüm ilk kullanıma ertelenir, yani
// ilk arama isteğine ya da ilk katalog olayına.
type katalog struct {
	// c kaydın aranacağı container'dır; nil olabilir (gömülü kullanım/test).
	c *container.Container

	// mu okuyucunun tek kez çözülmesini sağlar.
	//
	// sync.Once BİLİNÇLİ olarak kullanılmadı: Once, ilk çağrının SONUCUNU da
	// kalıcı kılar ve product henüz kayıtlı değilken düşen tek bir çözüm,
	// süreç ömrü boyunca aramayı ölü bırakırdı. Kilit yalnızca BAŞARILI sonucu
	// saklar; hata bir sonraki istekte yeniden denenir.
	mu      sync.Mutex
	okuyucu StoreProductReader
}

// newKatalog verilen container üzerinde çalışan tembel katalog erişimi kurar.
func newKatalog(c *container.Container) *katalog { return &katalog{c: c} }

// coz katalog yüzeyini container'dan çözer ve sonucu saklar.
func (k *katalog) coz() (StoreProductReader, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.okuyucu != nil {
		return k.okuyucu, nil
	}
	if k.c == nil {
		return nil, coreerrors.Unavailable(codeCatalogMissing,
			"container yok; %q yüzeyi çözülemez", catalogInteropName)
	}

	okuyucu, err := container.Resolve[StoreProductReader](k.c, catalogInteropName)
	if err != nil {
		// Sınıf KORUNUR: kayıt yoksa NotFound, tip uymuyorsa Internal gelir ve
		// ikisi farklı arızalardır — biri "product kurulu değil", öteki
		// "yüzeyin imzası değişmiş".
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeCatalogMissing,
			"katalog okuma yüzeyi %q çözülemedi; product modülü kurulu mu?", catalogInteropName)
	}

	k.okuyucu = okuyucu

	return okuyucu, nil
}

// urunler verilen kimliklerin VİTRİN kayıtlarını ham JSON olarak döner.
//
// Kayıtların sırası isteğin kimlik sırasıdır (alaka sırası); bulunamayan,
// yayında olmayan ya da isteğin kanallarında görünmeyen kimlik SESSİZCE
// atlanır. Kural kataloğa aittir ve burada tekrarlanmaz.
//
// Boş kimlik listesi için katalog HİÇ ÇAĞRILMAZ: sonuç zaten boştur ve boş bir
// tur atmak, arama hiçbir şey bulmadığında gereksiz bir gidiş-dönüş demekti.
func (k *katalog) urunler(ctx context.Context, ids, kanallar []string) ([]json.RawMessage, error) {
	if len(ids) == 0 {
		return []json.RawMessage{}, nil
	}

	okuyucu, err := k.coz()
	if err != nil {
		return nil, err
	}

	istek, err := json.Marshal(katalogIstegi{IDs: ids, SalesChannelIDs: kanallar})
	if err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogRead,
			"katalog isteği kodlanamadı (%d kimlik)", len(ids))
	}

	ham, err := okuyucu.StoreProductsByIDsJSON(ctx, istek)
	if err != nil {
		// Sınıf korunur: katalog sınırı aşan bir istek için Invalid döner ve
		// bunu Internal'a çevirmek, çağıranın düzeltebileceği bir hatayı
		// sunucu arızası gibi göstermek olurdu.
		return nil, coreerrors.Wrap(err, coreerrors.KindOf(err), codeCatalogRead,
			"katalog kayıtları okunamadı (%d kimlik)", len(ids))
	}

	var yanit katalogYaniti
	if err := json.Unmarshal(ham, &yanit); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInternal, codeCatalogResponse,
			"katalog yanıtı çözümlenemedi; %q yüzeyinin şeması değişmiş olabilir", catalogInteropName)
	}
	if yanit.Products == nil {
		return []json.RawMessage{}, nil
	}

	return yanit.Products, nil
}
