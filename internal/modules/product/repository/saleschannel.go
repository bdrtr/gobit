package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository/productdb"
)

// Bu dosya ürün listelemesinin SATIŞ KANALI süzgecini ve o süzgeci taşıyan iki
// sorguyu (liste + sayaç) barındırır.
//
// # Süzgeç neden veritabanında
//
// Kural iki yarımdır: ataması OLMAYAN ürün tüm kanallarda görünür, ataması OLAN
// ürün yalnızca atandığı kanallarda görünür. İlk yarım, core/link'in sunduğu
// ListManyByTo yüzeyiyle VERİMLİ çıkarılamaz — "bu kanallara atanmış ürünler"
// tek sorguda gelir ama "hiç ataması olmayan ürünler" ancak tüm bağları çekip
// tersini alarak bulunur. Bunun iki bedeli vardır ve ikincisi ölümcüldür:
// katalog büyüdükçe tüm link tablosu belleğe girer ve — asıl mesele — LIMIT ile
// OFFSET veritabanında uygulandığı için Go tarafında yapılan bir eleme sayfayı
// eksik doldurur, toplam sayacı da süzülmemiş kümeyi gösterirdi. Yani listeleme
// sessizce YANLIŞ sayfalanırdı.
//
// Bu yüzden süzgeç ürünün KENDİ sorgusuna, link tablosuna karşı bir
// EXISTS/NOT EXISTS koşulu olarak girer; sayfalama ve sayaç süzülmüş küme
// üzerinde çalışır.
//
// # Bu bir cross-module foreign key DEĞİLDİR
//
// Okuyan ilk kişi haklı olarak "başka bir modülün tablosuna dokunuluyor mu"
// diye sorar. Hayır: [SalesChannelLinkTable] auth'un değil, PRODUCT'IN
// bildirdiği link'in tablosudur (bkz. service.LinkProductSalesChannel) ve
// içinde iki serbest kimlik dizgesinden başka bir şey yoktur. Sorgu auth'un
// hiçbir tablosunu görmez, hiçbir REFERENCES eklenmez ve auth şemasını
// değiştirse bu koşul etkilenmez — Prensip 2.2'nin yasakladığı bağ tam olarak
// budur ve burada kurulmaz.
//
// # Sorgular neden elle yazılmış
//
// sqlc şemayı modülün migration dizininden okur; link tablosunun şemasını ise
// core/link çalışma anında (link.Define) kurar, dolayısıyla o dizinde YOKTUR ve
// sqlc üretimi "relation does not exist" ile reddeder. Şemayı sqlc'ye tanıtmak
// için sahte bir kopya bırakmak, core/link'in şemasını product içinde ikinci
// kez tanımlamak olurdu; kopya sessizce ayrışabilir. Bu yüzden bu iki sorgu
// elle yazılır ve — daha önemlisi — sqlc karşılıkları SİLİNMİŞTİR: süzgecin tek
// bir tanımı vardır ([productFilterSQL]). İki tanım kalsaydı, bir gün eklenen
// bir süzgeç birine yazılıp diğerine yazılmaz ve vitrin ile yönetim listesi
// SESSİZCE farklı kümeler dönerdi.
//
// # İndeks ve neden TEK alt sorgu
//
// Ek indeks GEREKMEZ ve product zaten ekleyemez (şema core/link'indir).
// core/link ManyToMany bir link için PRIMARY KEY (from_id, to_id) ve to_id
// üzerinde bir arama indeksi kurar (bkz. core/link registry.go ddl). Alt sorgu
// from_id ile başlar, yani birincil anahtarın önekini kullanır.
//
// Kural bir zamanlar İKİ alt sorguyla yazılıydı ("hiç ataması yok VEYA istenen
// kanalda ataması var") ve buradaki yorum aday satır başına bir indeks
// yoklaması yapıldığını iddia ediyordu. İDDİA YANLIŞTI ve yanlışlığı ancak
// gerçek hacimde göründü: planlayıcı iki bağımsız EXISTS'i gördüğünde ikisini
// de HASH'e çeviriyor, yani ilk satırı dönmeden ÖNCE link tablosunun tamamını
// iki kez tarıyor. Ölçüldü — 52.000 ürün ve 52.000 kanal ataması, vitrinin
// LIMIT 20'lik liste sorgusu:
//
//	iki EXISTS (eski)          26,8 ms
//	tek bool_or (bugünkü)       0,8 ms
//
// Maliyet sayfa boyutuyla değil KATALOG boyutuyla büyüyordu, üstelik vitrinin
// en sıcak ucunda. Tek korelasyonlu alt sorgu planlayıcıya satır başına indeks
// yoklaması yaptırır ve LIMIT'te gerçekten durabilir.
//
// # IS TRUE bir süs değil
//
// bool_or'un içindeki "IS TRUE" olmadan kural DEĞİŞİR: kanal dizisi bir NULL
// eleman taşıdığında "to_id = ANY(...)" o satır için NULL döner, bool_or NULL'ı
// yutar ve COALESCE onu "hiç ataması yok" sanıp ürünü GÖRÜNÜR yapar. Yani
// eksik yazılmış hâli AÇIĞA düşer: bir kanala atanmış ürün, o kanalı istemeyen
// bir isteme görünür. Sekiz senaryoda ölçüldü; "IS TRUE" ile eski kuralla
// birebir aynı, onsuz ikisinde ayrışıyor.
//
// Ve HİÇBİR TEST bunu yakalayamaz — bu da ölçüldü. Kanal dizisi buraya Go'dan
// []string olarak gelir, dolayısıyla bugün NULL eleman ÜRETİLEMEZ; "IS TRUE"
// silindiğinde entegrasyon paketinin on üç testinin hepsi geçmeye devam eder
// (silinen öteki her parça — COALESCE'ın varsayılanı, yönetim dalı, bool_or,
// korelasyon, eşitliğin yönü — en az bir testi düşürür). Yani bu iki sözcük
// çağıranın BUGÜNKÜ tip seçimine yaslanıyor ve o seçim değişirse kural sessizce
// gevşer. Yazılmasının sebebi budur; kaldırılmak istenirse önce bu satır
// okunmalıdır.

// SalesChannelLinkTable ürün ↔ satış kanalı bağının tutulduğu link tablosudur.
//
// Ad, core/link'in sözleşmesinden ("link_" + link adı) türer ama BURADA ELLE
// yazılır: link adı service paketindedir ve repository onu import edemez
// (service zaten repository'yi import eder). Elle tekrarlanan sabit sessizce
// ayrışabileceği için service paketinde bir test iki adı birbirine bağlar.
const SalesChannelLinkTable = "link_product_sales_channel"

// salesChannelVisibleTemplate satış kanalı görünürlük kuralının SQL karşılığıdır.
//
// %[1]s ürünün kimliğini veren ifade (liste sorgusunda "product.id", tekil
// sorguda "$1"), %[2]s ise kanal kimliklerini taşıyan parametredir. Şablon
// olması, kuralın TEK yerde durmasını sağlar: aynı metin hem sayfalanan listede
// hem tekil görünürlük denetiminde kullanılır, dolayısıyla ikisi ayrışamaz.
//
// Üç dal sırayla şu üç durumu karşılar:
//
//  1. Parametre NULL ise istek bir satış kanalı kimliği taşımıyordur ve süzgeç
//     hiç uygulanmaz (yönetim listesi bu daldan geçer).
//  2. Ürünün HİÇ ataması yoksa her kanalda görünür (geriye uyumluluk: mevcut
//     katalog bir gecede boşalmaz).
//  3. Ataması varsa yalnızca istenen kanallardan biriyle eşleşiyorsa görünür.
//
// Boş ama NULL olmayan bir dizi üçüncü dalı hiçbir zaman doğrulamaz; kanalsız
// bir istekte yalnızca atamasız ürünler kalır. Bu bilinçlidir: eşleşmenin
// tanımı değişmez, yalnızca eşleşecek kanal yoktur.
const salesChannelVisibleTemplate = `(
    %[2]s::text[] IS NULL
    OR COALESCE((
      SELECT bool_or(scl.to_id = ANY(%[2]s::text[]) IS TRUE)
      FROM ` + SalesChannelLinkTable + ` scl
      WHERE scl.from_id = %[1]s
    ), true)
  )`

// salesChannelVisible görünürlük koşulunu verilen ürün ifadesi ve kanal
// parametresi için üretir.
//
// Yalnızca paket içindeki SABİTLERLE çağrılır; çağıranın verdiği hiçbir dizge
// buraya girmez, kanal kimlikleri SQL'e parametre olarak gider.
func salesChannelVisible(productExpr, channelsParam string) string {
	return fmt.Sprintf(salesChannelVisibleTemplate, productExpr, channelsParam)
}

// productFilterSQL ürün listeleme ve sayma sorgularının ORTAK süzgeç gövdesidir.
//
// Parametre sırası: $1 status, $2 collection_id, $3 handle, $4 search,
// $5 sales_channel_ids. Sayfalama parametreleri ($6, $7) yalnızca liste
// sorgusunda vardır; bu yüzden sayaç sorgusu aynı gövdeyi hiç değiştirmeden
// kullanabilir.
const productFilterSQL = `WHERE deleted_at IS NULL
  AND ($1::text IS NULL OR status = $1::text)
  AND ($2::text IS NULL OR collection_id = $2::text)
  AND ($3::text IS NULL OR handle = $3::text)
  AND ($4::text IS NULL OR title ILIKE '%' || $4::text || '%')
  AND `

// productColumns ürün satırının sütunlarını [productdb.Product] alanlarının
// SIRASIYLA sayar.
//
// # Neden "SELECT *" değil
//
// Satırlar konuma göre çözülür (pgx.RowToStructByPos) ve "SELECT *" sütun
// sırasını TABLONUN fiziksel düzenine bırakır. O düzen bir gün kayarsa —
// araya sütun eklemek yeter — eşleme kayar ve kayma SESSİZDİR: bu tabloda
// handle ile title bitişik ve ikisi de text; subtitle/description/thumbnail
// üç text; weight/length/height/width dört integer. Aynı tipte komşu iki
// sütunun yer değiştirmesi hiçbir hata üretmez, yalnızca her ürünün başlığı
// ile handle'ını takas eder. Sütun SAYISI değişseydi hata alırdık; asıl
// tehlike sayı değil SIRADIR.
//
// Adlandırılmış liste bu yolu kapatır: araya eklenen sütun buraya girmediği
// için hiçbir şeyi kaydırmaz, silinen ya da adı değişen sütun ise sorguyu
// gürültüyle düşürür.
//
// Kalan tek risk, sqlc'nin ürettiği alan sırasının bu listeden ayrışmasıdır;
// onu [TestUrunSutunEslemesiKaymamis] her alana AYIRT EDİLEBİLİR bir değer
// yazıp geri okuyarak sabitler.
const productColumns = `id, handle, title, subtitle, description, thumbnail,
	status, is_giftcard, discountable, weight, length, height, width,
	material, origin_country, collection_id, metadata,
	created_at, updated_at, deleted_at`

// listProductsSQL ölçütlere uyan ürünleri sayfalı okur.
//
// Sıra (created_at DESC, id DESC) sabittir; ikinci anahtar, aynı milisaniyede
// oluşmuş iki kaydın sayfalar arasında yer değiştirmesini engeller.
var listProductsSQL = `SELECT ` + productColumns + ` FROM product
` + productFilterSQL + salesChannelVisible("product.id", "$5") + `
ORDER BY created_at DESC, id DESC
LIMIT $6::int OFFSET $7::int`

// countProductsSQL ölçütlere uyan TOPLAM ürün sayısını okur.
//
// # Bu sorgu PAHALIDIR ve biçimi DEĞİŞTİRİLMEMELİDİR
//
// LIMIT'i olmadığı için planlayıcı erken duramaz: ürün tablosunun tamamını
// gezer ve satır başına link tablosuna bir indeks yoklaması yapar. gobit_load
// üzerinde ölçüldü (52.004 ürün, 52.000 kanal ataması):
//
//	Aggregate (actual 70,655 ms)
//	  -> Seq Scan on product (rows=52.004)
//	       Filter: ... AND COALESCE((SubPlan 1), true)
//	       SubPlan 1 -> Index Only Scan ... (loops=52.004)
//	  Buffers: shared hit=156.743 (bunun 156.013'ü alt sorgunun)
//
// Aynı kümeyi sayan iki alternatif biçim ölçüldü ve İKİSİ DE REDDEDİLDİ:
// "iki EXISTS" 43-54 ms, "GROUP BY + hash join" 33-45 ms. Süzgeçsiz durumda
// daha hızlılar ama link tablosunun tamamını hash'lemek sabit bir ~30 ms taban
// koyuyor; SEÇİCİ bir süzgeçte (tek ürün eşleşen bir "q") bugünkü biçim
// 13,8 ms, hash biçimi 30,0 ms — yani takas yön değiştiriyor. Üstelik liste
// sorgusu bu biçme MECBURDUR (yukarıdaki "İndeks ve neden TEK alt sorgu"
// başlığı) ve şablon ikisi arasında PAYLAŞILIR; biçimi ayırmak, görünürlük
// kuralının ikinci bir tanımını yaratırdı.
//
// Sayım O(katalog)'dur ve hiçbir SQL biçimi onu sublineer yapmaz. Bu yüzden
// çözüm burada değil ÇAĞIRANDA arandı: sayaç artık istenmediğinde hiç
// çalıştırılmıyor (bkz. service.ListProductsOptions.SkipCount).
var countProductsSQL = `SELECT count(*) FROM product
` + productFilterSQL + salesChannelVisible("product.id", "$5")

// visibleProductIDsSQL verilen kimliklerden kanallarda GÖRÜNÜR olanları döner.
//
// Toplu sorulmasının sebebi somut: arama bir seferde onlarca kimlik getirir ve
// görünürlüğü kimlik başına sormak, sonuç sayısı kadar gidiş-dönüş demektir.
// Bu depo N+1'i yapısal olarak dışarıda tutan bir mimaride yaşıyor
// (bkz. core/query) ve arama yolunda onu geri getirmek, en sıcak uçta en pahalı
// erişim desenini kurmak olurdu.
//
// Kural [salesChannelVisibleTemplate]'ten gelir, yani tekil sorguyla AYNI
// tanımdır; ikinci bir kopya çıkarılsaydı biri değiştiğinde arama ile vitrin
// sessizce ayrışırdı.
var visibleProductIDsSQL = `SELECT id FROM product
WHERE id = ANY($1::text[]) AND deleted_at IS NULL AND ` +
	salesChannelVisible("product.id", "$2")

// visibleVariantIDsSQL verilen VARYANT kimliklerinden kanallarda görünür
// olanları döner.
//
// # Görünürlük varyantın değil ÜRÜNÜN özelliğidir
//
// Kanal ataması ürüne yapılır ([SalesChannelLinkTable] ürün kimliği taşır) ve
// varyant onu devralır. Bu yüzden şablona ürün ifadesi olarak varyantın
// product_id sütunu verilir; kural yeniden yazılmaz, aynı
// [salesChannelVisibleTemplate] ikinci bir ifadeyle örneklenir. Varyanta ayrı
// bir atama YOKTUR ve olmamalıdır: aynı ürünün bir varyantının bir vitrinde,
// diğerinin başka bir vitrinde satılması diye bir kavram yok — vitrin ürünü
// gösterir, varyant onun bir seçeneğidir.
//
// # Neden var
//
// Kanal kapsamı bir zamanlar YALNIZCA okuma yüzeyinde uygulanıyordu: liste,
// sayaç, tekil uç ve toplu okuma hepsi buradaki şablondan geçiyordu ama sepete
// satır ekleyen YAZMA yolu varyantı kimlikle, süzgeçsiz okuyordu. Yani B
// kanalının publishable anahtarıyla gelen istemci, yalnızca A kanalında
// satılan bir varyantın kimliğini istek gövdesine yazarak onu satın alabilirdi.
// Bu sorgu o yolun sorusunu ("bu varyant benim kanallarımda görünür mü") aynı
// tanıma bağlar; kuralı Go tarafında yeniden yazmak — varyantın ürününün
// bağlarını çekip kesişime bakmak — ikinci bir tanım olurdu ve ayrıştığı gün
// vitrin bir ürünü gizlerken sepet onu satmaya devam ederdi.
//
// # Neden toplu
//
// Gerekçe [visibleProductIDsSQL] ile aynıdır ve tekrarlanmıyor.
var visibleVariantIDsSQL = `SELECT v.id FROM product_variant v
WHERE v.id = ANY($1::text[]) AND v.deleted_at IS NULL AND ` +
	salesChannelVisible("v.product_id", "$2")

// productVisibleSQL tek bir ürünün verilen kanallarda görünür olup olmadığını
// sorar.
//
// Tekil vitrin ucu bu sorguyu kullanır. Kuralı Go tarafında yeniden yazmak
// mümkündü (ürünün bağlarını okuyup kesişime bakmak) ama o zaman aynı kural iki
// ayrı yerde ifade edilir ve biri değiştiğinde liste ile tekil uç ayrışırdı —
// listede gizlenen bir ürünün tekil ucunun onu göstermesi, gizlemeyi tümüyle
// anlamsız kılar.
var productVisibleSQL = `SELECT ` + salesChannelVisible("$1", "$2")

// ListProducts ölçütlere uyan ürünleri sayfalı döner.
//
// Satırlar KONUMA göre çözülür (pgx.RowToStructByPos). Ada göre çözüm burada
// çalışmazdı: alan adı CollectionID, sütun adı collection_id ve pgx ikisini
// eşleştirmek için bir etiket ister; sqlc üretimi etiket yazmaz.
// Konuma göre çözümün bedeli sıraya bağımlılıktır ve o bağımlılık
// [productColumns] ile açıkça yazılıp testle sabitlenmiştir.
func (r *Repo) ListProducts(ctx context.Context, f ProductFilter) ([]models.Product, error) {
	rows, err := r.db.Query(ctx, listProductsSQL,
		f.Status, f.CollectionID, f.Handle, f.Search, f.SalesChannelIDs,
		toInt32(f.Limit), toInt32(f.Offset))
	if err != nil {
		return nil, wrapDB(err, "ürünler listelenemedi")
	}
	// CollectRows satırları kapatır ve rows.Err()'i de sonuca katar; hiç satır
	// yoksa boş dilim döner (nil değil), böylece JSON'da "null" değil "[]" olur.
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[productdb.Product])
	if err != nil {
		return nil, wrapDB(err, "ürünler listelenemedi")
	}
	return toProducts(list)
}

// CountProducts ölçütlere uyan TOPLAM ürün sayısını döner.
//
// Sayı, sayfalama zarfının ("count") kaynağıdır ve limit/offset'ten
// BAĞIMSIZDIR; istemci kaç sayfa olduğunu ancak böyle bilebilir. Satış kanalı
// süzgeci burada da uygulanır: sayaç süzülmemiş kümeyi gösterseydi vitrin
// istemcisi hiç dolmayan sayfalar isterdi.
func (r *Repo) CountProducts(ctx context.Context, f ProductFilter) (int, error) {
	var n int64
	err := r.db.QueryRow(ctx, countProductsSQL,
		f.Status, f.CollectionID, f.Handle, f.Search, f.SalesChannelIDs).Scan(&n)
	if err != nil {
		return 0, wrapDB(err, "ürün sayısı okunamadı")
	}
	return int(n), nil
}

// ProductVisibleInSalesChannels ürünün verilen satış kanallarında görünür olup
// olmadığını bildirir.
//
// salesChannelIDs nil ise sonuç her zaman true'dur (istek kanal kimliği
// taşımıyordur); sorgu yine de veritabanına gider çünkü kararı kural veren tek
// yer [salesChannelVisibleTemplate] olmalıdır. Çağıran gereksiz turdan
// kaçınmak isterse nil durumunu kendisi kısa devre yapar.
func (r *Repo) ProductVisibleInSalesChannels(
	ctx context.Context,
	productID string,
	salesChannelIDs []string,
) (bool, error) {
	var visible bool
	if err := r.db.QueryRow(ctx, productVisibleSQL, productID, salesChannelIDs).Scan(&visible); err != nil {
		return false, wrapDB(err, "ürünün satış kanalı görünürlüğü okunamadı: %s", productID)
	}
	return visible, nil
}

// VisibleProductIDs verilen kimliklerden kanallarda görünür olanları TEK
// sorguda döner.
//
// Sonuç bir küme olarak döner çünkü çağıranın tek ihtiyacı üyelik sorusudur;
// dilim dönseydi her çağıran kendi haritasını kurardı ve sıralama, dilimin
// taşımadığı bir anlam gibi görünürdü — istek sırası çağıranın elindedir.
func (r *Repo) VisibleProductIDs(
	ctx context.Context,
	productIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	return r.visibleIDs(ctx, visibleProductIDsSQL, productIDs, salesChannelIDs, "ürün")
}

// VisibleVariantIDs verilen varyant kimliklerinden kanallarda görünür olanları
// TEK sorguda döner.
//
// Sepete satır ekleyen akış, varyantın kanal kapsamını Query katmanı üzerinden
// bu yola sorar (bkz. service/provider.go). Kuralın kendisi ve neden varyantın
// değil ürününün sorulduğu [visibleVariantIDsSQL] belgesindedir.
//
// Sonucun küme olması ve boş girdinin tur atmaması [VisibleProductIDs] ile
// aynı gerekçelerdir.
func (r *Repo) VisibleVariantIDs(
	ctx context.Context,
	variantIDs []string,
	salesChannelIDs []string,
) (map[string]struct{}, error) {
	return r.visibleIDs(ctx, visibleVariantIDsSQL, variantIDs, salesChannelIDs, "varyant")
}

// visibleIDs bir toplu görünürlük sorgusunu çalıştırır ve dönen kimlikleri
// ÜYELİK kümesine çevirir.
//
// İki toplu sorgu ([visibleProductIDsSQL] ve [visibleVariantIDsSQL]) yalnızca
// hangi tablodan seçtiklerinde ayrılır; gövdeyi paylaşmaları, birinin bir gün
// rows.Err()'i unutması ya da boş girdide gereksiz tur atması gibi sessiz bir
// ayrışmayı imkânsız kılar. Kural zaten tek şablondadır; bu, kuralı ÇAĞIRAN
// yolun da tek olmasını sağlar.
//
// kind hata mesajına giren varlık adıdır ("ürün", "varyant"); mesajı sorgudan
// türetmek, operatörün gördüğü metni SQL'in biçimine bağlardı.
func (r *Repo) visibleIDs(
	ctx context.Context,
	sql string,
	ids []string,
	salesChannelIDs []string,
	kind string,
) (map[string]struct{}, error) {
	if len(ids) == 0 {
		return map[string]struct{}{}, nil
	}

	rows, err := r.db.Query(ctx, sql, ids, salesChannelIDs)
	if err != nil {
		return nil, wrapDB(err, "%s görünürlüğü okunamadı (%d kimlik)", kind, len(ids))
	}

	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, wrapDB(err, "%s görünürlüğü okunamadı (%d kimlik)", kind, len(ids))
	}

	gorunur := make(map[string]struct{}, len(found))
	for _, id := range found {
		gorunur[id] = struct{}{}
	}

	return gorunur, nil
}
