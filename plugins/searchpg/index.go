package searchpg

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreerrors "github.com/bdrtr/gobit/core/errors"
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
// # Sıralama ts_rank iledir; ts_rank_cd DEĞİLDİR
//
// GIN indeksi ORDER BY'ı karşılayamaz, dolayısıyla sıralama ifadesi EŞLEŞEN HER
// BELGE için çalışır: bir sayfa dönmek için 52 bin eşleşmenin 52 bininde de
// hesaplanır. Bu yüzden ifadenin satır başına bedeli doğrudan uç noktanın
// bedelidir. Ölçüldü (postgres:16.14-alpine, 52 bin belgelik indeks, belge
// başına ~92 lexeme, her şey önbellekte, paralel işçi kapalı, LIMIT 20):
//
//	eşleşme sayısı   ts_rank_cd   ts_rank   yalnızca eşleşme
//	            10      0,23 ms   0,10 ms            0,06 ms
//	           110      1,60 ms   0,25 ms            0,18 ms
//	         1 002     13,70 ms   1,40 ms            1,10 ms
//	        10 400    148,00 ms  23,00 ms           21,70 ms
//	        52 000    663,00 ms  30,60 ms           23,80 ms
//
// Tablo LİTERAL sorgunundur. pgx hazırlanmış ifade kullandığı için altıncı
// çalıştırmadan sonra GENEL plana geçilebilir; orada $1 bilinmediğinden
// WHERE'deki çağrı sabite katlanmaz, Recheck Cond'a taşınır ve satır başına
// yeniden çalışır. GIN indeksi orada da kullanılır. SIRALAMA ifadesi bundan
// etkilenmez çünkü skaler alt sorgudur (aşağıda): 52 bin eşleşmede genel plan
// 25,4 ms, literal plan 24,3 ms.
//
// Aradaki fark ts_rank_cd'nin belge başına ~12 µs'lik bedelidir; ts_rank aynı
// satırlarda (tek terimli sorgu) ~0,07 µs harcar. Planlayıcı ikisini AYIRT
// EDEMEZ; pg_proc.procost her ikisi için de 1'dir, yani "sıralama pahalı"
// bilgisi plana hiç girmez.
//
// Bu iki sayı SORGU BİÇİMİNE bağlıdır ve hangisinin neye bağlı olduğu ölçüldü.
// ts_rank_cd'nin bedeli belge boyutuna da terim sıklığına da bağlı değildir
// (tek lexeme'lik bir tsvector sütununda 52 bin satır 623 ms, 92 lexeme'lik
// gerçek belgelerde 667 ms). ts_rank'inki bağlıdır: bedeli sorgudaki terimlerin
// belgedeki KONUM listelerinin çarpımıyla büyür. 20 bin sentetik satırda,
// `a & c` sorgusuyla ölçüldü:
//
//	belgedeki geçiş sayısı    ts_rank   ts_rank_cd
//	terim başına 1             0,21 µs      29,5 µs
//	terim başına 10            0,91 µs      30,8 µs
//	tsvector konum tavanında   315 µs       93,6 µs
//
// Yani tavanda — aynı lexeme'i 256'dan çok kez taşıyan bir belge — ilişki
// TERSİNE döner ve ts_rank 3,4 kat pahalı olur. Bu indeksin ürettiği gerçek
// belgeler (~92 lexeme) o tavanın yakınında değildir, ama sayı yazılmadan
// bırakılsaydı "ts_rank her zaman ucuzdur" ölçülmemiş bir vaat olurdu:
// makine üretimi bir açıklama (aynı kelimeyi yüzlerce kez tekrarlayan SEO
// metni) tavana yaklaşabilir.
//
// Neden bu uçta önemli: burası vitrinin sıcak yoludur, kimliği her tarayıcının
// taşıdığı publishable anahtardır ve varsayılan kota 600 istek/dakikadır.
// Kataloğun TAMAMINDA geçen tek bir kelime (marka adı, "pamuk") o kotayla
// saniyede 6,6 çekirdek yakıyordu; derin sayfalamada (LIMIT 100 OFFSET 50000)
// tek istek 1909 ms sürüyordu. Aynı istek bugünkü ifadeyle 37,6 ms'dir; genel
// planda ölçülen çift 744 ms -> 47 ms'dir.
//
// KAYBEDİLEN, yakınlığın AĞIRLIĞI YENEBİLMESİDİR — yakınlığın kendisi değil.
// ts_rank de yakınlığa duyarlıdır ve bu ölçüldü: iki A ağırlıklı kelime
// arasındaki boşluk 0'dan 6'ya çıkarken skor 0,9910 → 0,9850 → 0,9736 →
// 0,9524 → 0,9149 → 0,8530 → 0,7615 iner. Fark şudur: ts_rank'te yakınlık
// DOYAN ikincil bir çarpandır; ts_rank_cd'de ise kapak yoğunluğu ağırlık
// modelinin (A başlık, B anahtar, C açıklama; bkz. [belge]) ÜSTÜNE geçebilir.
// "mavi gomlek" sorgusunda iki kelimeyi anahtar alanında (B) yan yana taşıyan
// ürün, ikisini de BAŞLIĞINDA (A) taşıyan üründen önce geliyordu (cd 0,4 >
// 0,2; rank 0,396 < 0,915). İndeksin ağırlıklara ayrılmış olmasının tek sebebi
// o sıralamadır, dolayısıyla ağırlığın kazanması BEKLENEN davranıştır.
//
// # Sıralama sorgusundan HARİÇ TUTMALAR düşürülür
//
// ts_rank, içinde olumsuzlama (!) taşıyan bir sorguda HER belgeye 0 verir;
// ölçüldü: 'alpha' & !'zeta' sorgusunda 'alpha' taşıyan belge de 0 alır,
// 'alpha' ile 'beta'yı birlikte taşıyan belge de. Sıralama ham sorguyla
// yapılsaydı "gomlek -mavi" yazan alışverişçinin sonuçları alakaya göre DEĞİL
// product_id'ye, yani indekslenme sırasına göre gelirdi — üstelik bu godoc'un
// ilk paragrafı - desteğini websearch'ü seçmenin GEREKÇESİ olarak sayarken.
// Sessiz olurdu: sonuç kümesi doğru, sırası anlamsız.
//
// querytree tsquery'nin indekslenebilir (olumlu) kısmını döner — 'alpha' &
// !'zeta' → 'alpha' — ve sıralama o kısımla yapılır. Eleme WHERE'de olduğu
// gibi kalır, yani hariç tutma davranışı değişmez; değişen yalnızca skorun
// neye bakarak hesaplandığıdır.
//
// SINIR: yalnızca hariç tutmadan oluşan bir sorguda ("-mavi") querytree 'T'
// döner, sıralanacak olumlu sinyal yoktur ve sıra product_id'ye düşer. Bu
// gizlenmiyor: hariç tutma ALAKA taşımaz, taşıyormuş gibi yapan bir skor
// uydurmaktansa sıranın deterministik kalması yeğdir.
//
// # Bilinen sınır: eşleşme taraması hâlâ katalog kadar büyür
//
// Kalan maliyet eşleşmenin kendisidir: kataloğun tamamına uyan bir kelime yine
// 52 bin satır okur (literal planda ~24 ms, genel planda ~53 ms) ve bu sayı
// katalogla birlikte doğrusal büyür — 500 bin ürünlük bir katalogda aynı kelime
// yarım saniyeye çıkar. Bunun altına inmek indeksin ORDER BY'ı da karşılaması
// demektir (RUM) ve zorunlu bir EXTENSION eklemek ADR 0015'in tarihli
// kararıdır; burada tek satırlık bir hızlanma olarak alınamaz.
//
// Sıralama ifadesi SKALER ALT SORGUDUR ve bu bir HIZ kararıdır. Literal
// bir sorguda tekrarlanan ifade planlayıcıca tek bir sabite katlanır, ama pgx
// hazırlanmış ifade kullandığı için altıncı çalıştırmadan sonra GENEL plana
// geçilebilir ve orada $1 bilinmediğinden katlama OLMAZ: planlayıcı sorgu
// metnini satır başına yeniden ayrıştırır. Alt sorgu onu InitPlan'a çevirir,
// yani sorgu başına BİR kez hesaplanır. Ölçüldü (52 bin eşleşme, genel plan):
// tekrarlanan ifadeyle 46,7 ms, alt sorguyla 25,4 ms — literal planın 24,3
// ms'siyle aynı yerde.
//
// WHERE'deki çağrı BİLEREK olduğu gibi bırakıldı: GIN indeksinin seçilmesi ona
// bağlıdır ve alt sorguya taşımak indeksi devre dışı bırakma riski taşır. Genel
// planda indeksin hâlâ kullanıldığı EXPLAIN ile doğrulandı (Bitmap Index Scan
// on searchpg_product_document_idx).
//
// İkinci sıralama anahtarı product_id'dir: eşit alakalı kayıtlar arasında
// sıra deterministik olmazsa sayfalama aynı ürünü iki sayfada gösterebilir ya
// da hiç göstermeyebilir.
const searchSQL = `
SELECT product_id
FROM searchpg_product
WHERE document @@ websearch_to_tsquery('simple', $1)
ORDER BY ts_rank(document, (SELECT querytree(websearch_to_tsquery('simple', $1))::tsquery)) DESC,
         product_id
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
