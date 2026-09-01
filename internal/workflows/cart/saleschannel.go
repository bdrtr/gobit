package cart

import (
	"context"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Bu dosya sepet YAZMA yolunun satış kanalı kapsamını taşır.
//
// # Kapatılan açık
//
// Kanal kapsamı bir süre YALNIZCA okuma yüzeyinde uygulandı: vitrin listesi,
// sayacı, tekil ucu ve toplu okuması tek bir SQL şablonundan geçiyordu
// (product/repository/saleschannel.go) ama sepete satır ekleyen bu akış
// varyantı yalnızca KİMLİKLE, süzgeçsiz okuyordu. Sonuç, kuralın kendisini
// anlamsız kılıyordu: B kanalının publishable anahtarıyla gelen bir istemci,
// yalnızca A kanalında satılan bir varyantın kimliğini
// POST /store/v1/carts/{id}/line-items gövdesine yazarak onu sepete ekleyip
// satın alabiliyordu. Vitrinde gizlenen ürün, sepette satılabiliyordu.
//
// # Kural BURADA TANIMLI DEĞİL
//
// Bu paket "kanal ataması olmayan ürün her yerde görünür, ataması olan yalnızca
// atandığı kanallarda görünür" kuralını BİLMEZ ve bilmemelidir: kural product
// modülünün verisidir ve orada tek bir yerde yaşar. Akışın yaptığı tek şey,
// katalog okumasına isteğin kanallarını EKLEMEK ve cevabı olduğu gibi kabul
// etmektir (bkz. [Workflows.variantTitle]). Kuralı burada yeniden ifade etmek —
// varyantın ürününün bağlarını okuyup kesişime bakmak — ikinci bir tanım
// olurdu; ayrıştığı gün vitrin bir ürünü gizlerken sepet onu satmaya devam
// ederdi, yani kapatılan açık kendi aynasında geri açılırdı.
//
// # Kanallar İSTEMCİDEN GELMEZ
//
// Kaynak yalnızca corehttp.Principal'dır: publishable anahtarın kaydından
// gelen kanal listesi. Bir parametre olarak taşınsaydı, çağıranın onu
// doldurmasının önünde hiçbir şey kalmazdı — aynı gerekçe bu akışın FİYAT ve
// BAŞLIK parametrelerini de reddetmesinin sebebidir (bkz. [Interop]). Depoda
// aynı karar iki kez daha verilmiştir: GraphQL şemasında kanal argümanı YOKTUR
// ve ADR 0008 istemci beyanını yetki kararının dışında tutar.
//
// # Neden context'ten okunuyor
//
// Kanal kümesi akışın imzasına eklenmedi; context'ten okunur. Örüntü depoda
// yenidir ama yalnız değildir: auth modülü de yetki yükseltmeyi bir servis
// metodunun içinde, corehttp.PrincipalFromContext ile denetler
// (bkz. authsvc.requireGrantableScopes) ve orada da "kimlik yoksa denetim
// uygulanmaz" der.
//
// Alternatif — kanalları handler'dan ilkel bir parametreyle taşımak — üç yeni
// sınır açardı (cart modülünün api.LinePricing arayüzü, bu paketin [Interop]
// yüzeyi ve aradaki adaptör) ve o sınırların hiçbirini derleyici denetlemiyor
// (ADR 0006). Daha önemlisi, kapsam kararı O ZAMAN çağıranın elinde olurdu:
// yeni bir yazma yolu parametreyi geçirmeyi unuttuğunda hiçbir şey patlamaz,
// yalnızca kapsam sessizce kaybolurdu. Context'ten okumak kararı katalog
// okumasının kendisine bağlar — sepete varyant sokan her yol o okumadan geçer.
//
// Bedeli dürüstçe: bağımlılık imzada GÖRÜNMEZ. Bu yüzden yeni bir varyant
// okumasının kanal kararını atlaması derleme hatası değildir ve internal/arch
// altındaki bir değişmez, yapıyı gezerek o boşluğu kapatır
// (bkz. TestVaryantOkumalariKanalKararindanGecer).
//
// # Kapsam GİRİŞTE uygulanır
//
// Denetim varyantın sepete GİRDİĞİ yerdedir. Satır adedini güncelleyen yol
// (bkz. [Workflows.UpdateLineItem]) ve sepeti siparişe çeviren akış
// (internal/workflows/checkout) katalog kapsamını YENİDEN sormaz ve bu bir
// eksiklik değil, verilmiş bir karardır:
//
//   - Sepete varyant sokabilen tek yol satır eklemedir; o kapı kapalıyken
//     yabancı bir kanalın varyantı sepete hiç giremez, dolayısıyla adet
//     güncelleme ve tamamlama yollarında kapatılacak bir şey kalmaz.
//   - Sepete GİRMİŞ bir satırın kataloğun sonradan değişmesinden etkilenmemesi
//     product modülünün YAZILI kararıdır ("sepete eklenmiş bir ürünün adı, o
//     ürün sonradan başka bir kanala taşınsa bile çözülebilmelidir",
//     bkz. productsvc.productProvider.List). Tamamlamada yeniden süzmek o
//     kararla çelişir ve müşterinin dolu sepetini, yöneticinin bir katalog
//     düzenlemesi yüzünden ödenemez hâle getirirdi.
//
// Sınırın nerede durduğu README'de de yazılıdır; yazılmamış bir sınır, olmayan
// bir sınırdır.

// SalesChannelIDsFromContext isteğin bağlı olduğu satış kanallarını
// DOĞRULANMIŞ KİMLİKTEN okur.
//
// Dönüş değerinin nil olup olmaması ANLAMLIDIR ve ayrım okuma yüzeyindekiyle
// BİREBİR aynıdır (bkz. product/graph.SalesChannelIDsFromContext):
//
//   - Kimlik YOKSA nil döner ve süzgeç uygulanmaz. Bu, mağaza kimlik
//     doğrulamasının hiç bağlanmadığı kurulumun karşılığıdır; akışlar süreç
//     içinden de çağrılabilir (tohum verisi, testler, ileride bir toplu iş) ve
//     o çağrıların kataloğu boş görmesi, olmayan bir kimliğe göre süzmek
//     olurdu.
//   - Kimlik VARSA nil ASLA dönmez: kanalsız bir kimlik BOŞ KÜMEDİR, "süzme
//     yok" değil. İkisini bir tutmak, kanalsız bir anahtara tüm kanalların
//     kataloğunu açardı — ve bu, kapatılan açığın tam olarak kendisidir.
//
// İki yüzeyin aynı üç durumu ayırdığı bir arch testiyle çivilenmiştir; ayrışma
// sessiz olurdu, çünkü ancak belirli bir kimlik biçiminde görülürdü.
func SalesChannelIDsFromContext(ctx context.Context) []string {
	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		return nil
	}

	if principal.SalesChannelIDs == nil {
		return []string{}
	}

	return principal.SalesChannelIDs
}

// salesChannelFilter katalog sorgusuna eklenecek kanal süzgecini üretir.
//
// İkinci dönüş değeri, süzgecin UYGULANIP uygulanmayacağını söyler; filtre
// haritasına "yok" anlamında bir nil yazmak, sağlayıcı tarafında boş kümeden
// (kanalsız kimlik) ayırt edilemezdi — Query filtreleri map[string]any'dir ve
// orada anlamı taşıyan şey ANAHTARIN VARLIĞIDIR.
func salesChannelFilter(ctx context.Context) (channels []string, apply bool) {
	channels = SalesChannelIDsFromContext(ctx)
	return channels, channels != nil
}
