package searchpg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// Hata kodları.
const (
	codeIndexFailed = "searchpg_index_query_failed"
	codeCanceled    = "searchpg_canceled"
)

// Metin arama yapılandırması ('simple') aşağıdaki sorgu metinlerine SABİT
// yazılmıştır, parametre olarak geçmez. Sebebi teknik: regconfig argümanı sabit
// olduğunda to_tsvector/websearch_to_tsquery IMMUTABLE olur ve planlayıcı
// ifadeyi sabite katlayıp GIN indeksini kullanabilir. Sözlük seçiminin
// gerekçesi paket belgesindedir.

// belge tek bir ürünün indekslenecek metin parçalarıdır.
//
// Parçalar AYRI durur çünkü ağırlıkları farklıdır (bkz. [upsertSQL]); tek bir
// birleşik metin, başlıkta geçen bir kelime ile açıklamada geçen bir kelimeyi
// ayırt edilemez kılar ve alaka sıralaması anlamını yitirirdi.
type belge struct {
	// urunID indeksin birincil anahtarıdır.
	urunID string
	// baslik A ağırlığındadır: ürün başlığı.
	baslik string
	// anahtar B ağırlığındadır: handle, alt başlık, etiketler, varyant
	// başlıkları ve SKU'lar. Kısa ve ayırt edici alanlar buradadır.
	anahtar string
	// metin C ağırlığındadır: açıklama. Uzun metin en düşük ağırlığı alır,
	// aksi hâlde uzun açıklamalı bir ürün her aramada öne çıkardı.
	metin string
}

// depo indeks tablosunun arama akışına açtığı yüzeydir.
//
// Arayüz TÜKETİCİ tarafında tanımlıdır: HTTP uçları ve olay işleyicileri
// yalnızca burada sayılan beş çağrıyı bilir ve gerçek PostgreSQL olmadan
// sınanabilir. Somut uygulama [indeks]'tir.
type depo interface {
	// Upsert belgeleri yazar ya da günceller ve damgalarını tazeler.
	Upsert(ctx context.Context, belgeler []belge) error
	// Delete verilen kimlikleri indeksten siler ve silinen satır sayısını döner.
	Delete(ctx context.Context, urunIDs ...string) (int64, error)
	// Search sorguya uyan kimlikleri ALAKA SIRASIYLA döner.
	Search(ctx context.Context, sorgu string, limit, offset int) ([]string, error)
	// Sweep verilen andan ÖNCE damgalanmış satırları siler.
	Sweep(ctx context.Context, esik time.Time) (int64, error)
	// Now veritabanının şu anki zamanını döner.
	Now(ctx context.Context) (time.Time, error)
}

// upsertSQL belgeleri TEK ifadeyle yazar.
//
// Diziler unnest ile satırlara açılır: yüz belgelik bir sayfa da tek belge de
// TEK gidiş-dönüş eder. Belge başına ayrı INSERT, yeniden indekslemede katalog
// büyüklüğü kadar tur açardı.
//
// tsvector VERİTABANINDA üretilir, Go'da değil: aynı metnin nasıl belgeye
// döndüğü tek bir yerde yazılıdır ve sorgu tarafıyla (websearch_to_tsquery)
// aynı sözlüğü kullandığı buradan görülür.
//
// Damga da veritabanı saatinden alınır (now()): birden çok örnek yazarken
// uygulama saatleri arasındaki kayma, süpürme eşiğini (bkz. [sweepSQL])
// yanlış tarafa düşürebilirdi.
const upsertSQL = `
INSERT INTO searchpg_product (product_id, document, indexed_at)
SELECT d.product_id,
       setweight(to_tsvector('simple', d.title), 'A') ||
       setweight(to_tsvector('simple', d.keywords), 'B') ||
       setweight(to_tsvector('simple', d.body), 'C'),
       now()
FROM unnest($1::text[], $2::text[], $3::text[], $4::text[])
     AS d(product_id, title, keywords, body)
ON CONFLICT (product_id) DO UPDATE
SET document   = EXCLUDED.document,
    indexed_at = EXCLUDED.indexed_at`

// deleteSQL verilen kimlikleri indeksten siler.
//
// Bulunmayan kimlik hata DEĞİLDİR: silme olayı, hiç indekslenmemiş bir ürün
// için de gelebilir (taslakken silinen ürün) ve işleyicinin idempotent olması
// zorunludur — veri yolu aynı olayı yeniden teslim edebilir.
const deleteSQL = `DELETE FROM searchpg_product WHERE product_id = ANY($1::text[])`

// searchSQL sorguya uyan kimlikleri alaka sırasıyla döner.
//
// websearch_to_tsquery, to_tsquery yerine BİLİNÇLİ olarak seçilmiştir: kullanıcı
// metnini asla sözdizimi hatasına çevirmez. to_tsquery'ye giden bir `a & &`
// ifadesi sorguyu düşürür ve arama kutusuna ne yazıldığına bağlı 500'ler
// üretirdi; websearch ise tırnak ("tam ifade"), OR ve - (hariç tut) desteğini
// de beraberinde getirir.
//
// İfade ORDER BY içinde TEKRARLANIR. Bir CTE ya da LATERAL ile tek yere
// indirilebilirdi ama sabit regconfig'li websearch_to_tsquery IMMUTABLE'dır:
// tekrar edilen ifade planlayıcı tarafından TEK bir sabite katlanır ve GIN
// indeksi kullanılabilir kalır. CTE, sorguyu planlayıcıdan gizleyip indeksi
// devre dışı bırakma riski taşır.
//
// İkinci sıralama anahtarı product_id'dir: eşit alakalı kayıtlar arasında
// sıra deterministik olmazsa sayfalama aynı ürünü iki sayfada gösterebilir ya
// da hiç göstermeyebilir.
const searchSQL = `
SELECT product_id
FROM searchpg_product
WHERE document @@ websearch_to_tsquery('simple', $1)
ORDER BY ts_rank_cd(document, websearch_to_tsquery('simple', $1)) DESC, product_id
LIMIT $2 OFFSET $3`

// sweepSQL eşikten önce damgalanmış satırları siler.
const sweepSQL = `DELETE FROM searchpg_product WHERE indexed_at < $1`

// nowSQL veritabanının şu anki zamanını okur.
const nowSQL = `SELECT now()`

// indeks arama tablosunun PostgreSQL uygulamasıdır.
//
// Eşzamanlı kullanıma güvenlidir; durumu yalnızca havuzdur.
type indeks struct {
	pool *pgxpool.Pool
}

// Somut deponun sözleşmeyi karşıladığı derleme zamanında sabitlenir.
var _ depo = (*indeks)(nil)

// newIndeks verilen havuz üzerinde çalışan indeks deposu üretir.
func newIndeks(pool *pgxpool.Pool) *indeks { return &indeks{pool: pool} }

// Upsert belgeleri yazar ya da günceller.
//
// Boş liste hata değildir ve hiç sorgu açmaz. Aynı kimlik iki kez verilirse
// PostgreSQL "ON CONFLICT DO UPDATE komutu aynı satırı ikinci kez etkileyemez"
// diye reddeder; bu yüzden çağıran kimlikleri tekilleştirmiş olmalıdır
// (bkz. [modul.belgeler]).
func (i *indeks) Upsert(ctx context.Context, belgeler []belge) error {
	if len(belgeler) == 0 {
		return nil
	}

	ids := make([]string, len(belgeler))
	basliklar := make([]string, len(belgeler))
	anahtarlar := make([]string, len(belgeler))
	metinler := make([]string, len(belgeler))
	for n, b := range belgeler {
		ids[n] = b.urunID
		basliklar[n] = b.baslik
		anahtarlar[n] = b.anahtar
		metinler[n] = b.metin
	}

	if _, err := i.pool.Exec(ctx, upsertSQL, ids, basliklar, anahtarlar, metinler); err != nil {
		return wrapDB(err, "arama indeksine %d belge yazılamadı", len(belgeler))
	}

	return nil
}

// Delete verilen kimlikleri indeksten siler.
func (i *indeks) Delete(ctx context.Context, urunIDs ...string) (int64, error) {
	if len(urunIDs) == 0 {
		return 0, nil
	}

	tag, err := i.pool.Exec(ctx, deleteSQL, urunIDs)
	if err != nil {
		return 0, wrapDB(err, "arama indeksinden %d kayıt silinemedi", len(urunIDs))
	}

	return tag.RowsAffected(), nil
}

// Search sorguya uyan ürün kimliklerini alaka sırasıyla döner.
//
// Kimlikten başka bir şey DÖNMEZ: gösterilecek kayıt kataloğun kendisinden
// okunur. İndekste başlık ya da fiyat saklamak, kataloğun ikinci bir kopyasını
// tutmak ve iki gösterimin ayrışmasını beklemek olurdu.
func (i *indeks) Search(ctx context.Context, sorgu string, limit, offset int) ([]string, error) {
	rows, err := i.pool.Query(ctx, searchSQL, sorgu, limit, offset)
	if err != nil {
		return nil, wrapDB(err, "arama sorgusu çalıştırılamadı")
	}

	// CollectRows satırları kapatır ve rows.Err()'i de sonuca katar; hiç satır
	// yoksa BOŞ dilim döner.
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, "arama sonuçları okunamadı")
	}

	return ids, nil
}

// Sweep verilen andan önce damgalanmış satırları siler ve sayısını döner.
//
// Yalnızca TAM bir yeniden indeksleme turundan sonra çağrılmalıdır; gerekçe
// [modul.yenidenIndeksle] belgesindedir.
func (i *indeks) Sweep(ctx context.Context, esik time.Time) (int64, error) {
	tag, err := i.pool.Exec(ctx, sweepSQL, esik)
	if err != nil {
		return 0, wrapDB(err, "bayat indeks kayıtları silinemedi")
	}

	return tag.RowsAffected(), nil
}

// Now veritabanının şu anki zamanını döner.
//
// Uygulama saati KULLANILMAZ: süpürme eşiği, yazma damgalarıyla aynı saatten
// gelmelidir. İki saat arasındaki birkaç saniyelik kayma, turun başında
// alınmış bir uygulama zamanıyla tur sırasında yazılmış satırların bayat
// sayılmasına — yani taze indeks kayıtlarının silinmesine — yol açardı.
func (i *indeks) Now(ctx context.Context) (time.Time, error) {
	var an time.Time
	if err := i.pool.QueryRow(ctx, nowSQL).Scan(&an); err != nil {
		return time.Time{}, wrapDB(err, "veritabanı saati okunamadı")
	}

	return an, nil
}

// wrapDB sürücü hatasını tipli hataya çevirir.
//
// İptal edilmiş bağlam KindUnavailable'a düşer: istemcinin koptuğu ya da
// bütçenin dolduğu bir istek sunucu arızası değildir ve 500 olarak
// raporlanması gerçek arızaları gürültüde boğardı (core/link'teki wrapDB ile
// aynı eşleme).
func wrapDB(err error, format string, a ...any) error {
	switch {
	case err == nil:
		return nil
	case coreerrors.Is(err, context.Canceled), coreerrors.Is(err, context.DeadlineExceeded):
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, codeCanceled,
			format+" (bağlam iptal edildi)", a...)
	default:
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeIndexFailed, format, a...)
	}
}
