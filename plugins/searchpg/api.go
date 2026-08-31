package searchpg

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Eklentinin açtığı uçlar ve istedikleri yetki.
const (
	// SearchPath vitrin arama ucudur.
	SearchPath = "/store/v1/search"
	// ReindexPath tam yeniden indeksleme ucudur.
	ReindexPath = "/admin/v1/search/reindex"
	// ScopeWrite yeniden indeksleme ucunun istediği yetkidir.
	//
	// Sözlük modüllerinkiyle AYNI biçimdedir ("<modül>:write"). Okuma yetkisi
	// tanımlanmadı: bu modülün tek yönetim ucu YAZAN bir uçtur ve verilmeyen
	// bir yetki adı, ilk kez dağıtıldığı gün ne işe yaradığı bilinmeyen bir
	// addır (product api/routes.go'daki gerekçeyle aynı).
	ScopeWrite = ModuleName + ":write"
)

// Sorgu parametrelerinin adları.
const (
	paramQuery  = "q"
	paramLimit  = "limit"
	paramOffset = "offset"
)

// Arama isteğinin sınırları.
const (
	// varsayilanLimit limit verilmediğinde dönen kayıt sayısıdır.
	varsayilanLimit = 20
	// maxLimit tek istekte dönebilecek en fazla kayıt sayısıdır.
	//
	// Değer kataloğun toplu okuma sınırıyla (product service.MaxLimit) AYNIDIR
	// ve elle tekrarlanmıştır. Aşılırsa istek burada değil katalogta reddedilir
	// ve kullanıcı, aramanın kendi sınırı yerine başka bir modülün hata
	// mesajını görürdü.
	maxLimit = 100
	// maxSorguBaytlari sorgu metninin bayt sınırıdır. Sınırsız bir metin,
	// websearch_to_tsquery'yi tek istekle megabaytlarca girdiyi ayrıştırmaya
	// zorlardı; arama kutusuna sığan hiçbir sorgu bu sınıra yaklaşmaz.
	maxSorguBaytlari = 256
)

// Hata kodları.
const (
	codeQueryMissing  = "searchpg_query_missing"
	codeQueryTooLong  = "searchpg_query_too_long"
	codeBadQueryParam = "searchpg_bad_query_param"
)

// listeZarfi liste yanıtlarının zarfıdır (plan Bölüm 8).
//
// Count bu YANITTAKİ kayıt sayısıdır, indeksteki toplam eşleşme değil ve bu
// bilinçlidir: kanal süzgeci kayıtlar okunurken katalogta uygulanır, yani
// gerçek toplam ancak TÜM eşleşmeler okunup süzüldükten sonra bilinebilirdi.
// İndeksten okunan ham eşleşme sayısını yazmak ise daha kötüsü olurdu — sayı,
// istemcinin asla göremeyeceği ürünleri de sayan bir yalan olurdu.
//
// Bunun görünür sonucu: bir sayfa, daha fazla eşleşme olduğu hâlde limitten AZ
// kayıt dönebilir. İstemci "boş sayfa gelene kadar" sayfalamalıdır.
type listeZarfi struct {
	Data   []json.RawMessage `json:"data"`
	Count  int               `json:"count"`
	Offset int               `json:"offset"`
	Limit  int               `json:"limit"`
}

// tekilZarf tekil yanıtların zarfıdır.
type tekilZarf struct {
	Data any `json:"data"`
}

// ara GET /store/v1/search
//
// Akış: indeksten ALAKA SIRALI kimlikler bulunur, kayıtlar kataloğun
// "product.interop" yüzeyinden AYNI SIRAYLA okunur. Yanıttaki her kayıt,
// vitrinin ürün ucunun yazdığı gövdeyle birebir aynı şekildedir; eklenti onu
// yeniden biçimlendirmez.
//
// Yayın durumu ve satış kanalı süzgeci katalogta uygulanır (bkz. paket
// belgesi). Bu yüzden indekste kalmış bayat bir kayıt bile başkasının
// kanalındaki ürünü SIZDIRAMAZ: süzgeci geçemeyen kimlik yanıta hiç girmez.
func (m *modul) ara(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sorgu, err := aramaSorgusu(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	limit, offset, err := sayfalama(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ids, err := m.indeks.Search(ctx, sorgu, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	urunler, err := m.katalog.urunler(ctx, ids, kanallar(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, listeZarfi{
		Data:   urunler,
		Count:  len(urunler),
		Offset: offset,
		Limit:  limit,
	})
}

// yenidenIndeksleUcu POST /admin/v1/search/reindex
//
// Tüm katalogu baştan indeksler ve SENKRON çalışır: yanıt, iş bittiğinde
// sayılarla birlikte döner. Arka plana atmak (202 Accepted) çağırana yalnızca
// "başladı" demek olurdu ve sonucu görebileceği bir yer olmadığı için
// başarısız bir tur sessizce kaybolurdu. Bedeli, büyük katalogda uzun süren
// bir istektir; bu uç bir operasyon aracıdır ve müşteri yolunda değildir.
//
// # Bilinen sınır: sunucu yazma zaman aşımı
//
// WRITE_TIMEOUT (varsayılan 30s) yeterince büyük bir katalogda dolabilir. O
// durumda YANIT istemciye ulaşmaz ama İŞ tamamlanır: zaman aşımı bağlantının
// yazma süresini keser, handler'ı durdurmaz — indeks yine kurulur, çağıran
// yalnızca sayıları göremez. Ölçek buraya geldiğinde doğru adım ucu arka plana
// atmak değil (sonucun görülebileceği bir yer yok), turu operatörün
// çalıştırdığı sayfa aralıklarına bölmektir.
func (m *modul) yenidenIndeksleUcu(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sonuc, err := m.yenidenIndeksle(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, tekilZarf{Data: sonuc})
}

// aramaSorgusu istekten arama metnini okur.
//
// Boş sorgu REDDEDİLİR. "Tüm katalogu döndür" anlamına gelseydi, aramanın
// listeleme ucunun ikinci bir kopyası olması ve boş bir arama kutusunun
// yanlışlıkla tüm katalogu çekmesi demekti; kataloğu listelemenin yolu zaten
// GET /store/v1/products'tır.
func aramaSorgusu(r *http.Request) (string, error) {
	sorgu := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if sorgu == "" {
		return "", coreerrors.Invalid(codeQueryMissing,
			"%s parametresi zorunludur ve boş olamaz", paramQuery)
	}
	if len(sorgu) > maxSorguBaytlari {
		return "", coreerrors.Invalid(codeQueryTooLong,
			"%s parametresi en fazla %d bayt olabilir (verilen: %d)",
			paramQuery, maxSorguBaytlari, len(sorgu))
	}

	return sorgu, nil
}

// sayfalama limit ve offset parametrelerini okur.
//
// Sınırı aşan limit KIRPILMAZ, reddedilir: sessizce kırpılan bir limit,
// istemcinin istediğinden az kayıt almasına ve bunu hiç görmemesine yol açar.
func sayfalama(r *http.Request) (limit, offset int, err error) {
	limit, err = tamSayiParam(r, paramLimit, varsayilanLimit)
	if err != nil {
		return 0, 0, err
	}
	offset, err = tamSayiParam(r, paramOffset, 0)
	if err != nil {
		return 0, 0, err
	}

	switch {
	case limit < 1:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s en az 1 olmalı (verilen: %d)", paramLimit, limit)
	case limit > maxLimit:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s en fazla %d olabilir (verilen: %d)", paramLimit, maxLimit, limit)
	case offset < 0:
		return 0, 0, coreerrors.Invalid(codeBadQueryParam,
			"%s negatif olamaz (verilen: %d)", paramOffset, offset)
	}

	return limit, offset, nil
}

// tamSayiParam sorgu parametresini tam sayı olarak okur; yoksa varsayılanı döner.
func tamSayiParam(r *http.Request, ad string, varsayilan int) (int, error) {
	ham := r.URL.Query().Get(ad)
	if ham == "" {
		return varsayilan, nil
	}

	deger, err := strconv.Atoi(ham)
	if err != nil {
		return 0, coreerrors.Wrap(err, coreerrors.KindInvalid, codeBadQueryParam,
			"%s parametresi tam sayı olmalı (verilen: %q)", ad, ham)
	}

	return deger, nil
}

// kanallar isteğin bağlı olduğu satış kanallarını DOĞRULANMIŞ KİMLİKTEN okur.
//
// Sorgu dizesine HİÇ BAKILMAZ ve bu bir güvenlik kararıdır: "?sales_channel_id="
// kabul edilseydi, elindeki herhangi bir publishable anahtarla gelen bir
// istemci başka bir kanalın katalogunda arama yapabilirdi. Kimliği çekirdeğin
// corehttp.RequireStore middleware'i koyar.
//
// nil ile boş dilim farkı ANLAMLIDIR ve katalogta tanımlıdır; burada product
// modülünün vitrin ucuyla (api/store.go salesChannelIDs) BİREBİR aynı eşleme
// yapılır:
//
//   - Kimlik yoksa nil: mağaza kimlik doğrulaması bu kurulumda bağlı değildir
//     ve süzgeç uygulanmaz. Aksi hâlde auth'suz bir kurulumda arama sessizce
//     hiçbir sonuç döndürmezdi.
//   - Kimlik varsa nil ASLA dönülmez: kanalsız bir kimlik BOŞ KÜME demektir,
//     "süzme yok" demek değil.
func kanallar(r *http.Request) []string {
	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok {
		return nil
	}
	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}
