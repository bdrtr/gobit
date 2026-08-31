//go:build integration

package e2e

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	inventorymodels "github.com/bdrtr/gobit/internal/modules/inventory/models"
	inventorysvc "github.com/bdrtr/gobit/internal/modules/inventory/service"
	ordermodels "github.com/bdrtr/gobit/internal/modules/order/models"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	cartwf "github.com/bdrtr/gobit/internal/workflows/cart"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Bu dosya ÇOK DEPOLU sipariş tamamlamayı gerçek modüllerle, gerçek Postgres'le
// ve gerçek saga motoruyla kanıtlar.
//
// # Neden checkout'un birim testleri yetmez
//
// checkout paketinin birim testleri lokasyon seçimini SAHTE yüzeylerle sınar:
// "adaylar stoktan geldiği gibi geçti mi", "seçimi kargo mu yaptı". Doğru
// sorulardır ama iddiaları çağrıların ŞEKLİDİR. Buradaki iddia ise SONUÇTUR:
// iki satırın rezervasyonu stok modülünün defterinde GERÇEKTEN iki ayrı depoda
// açılmış mıdır. Bir sahte, "hepsini aynı depodan ayır" diyen bir uygulamayı
// aday listesiyle yakalayabilir; ama defterdeki adedin hangi depodan düştüğünü
// yalnızca gerçek stok modülü söyler.
//
// # Depolar ve kalemler test BAŞINA kurulur
//
// Paket testleri SIRAYLA koşar ve tek bir veritabanını paylaşır; stok seviyesi
// (kalem, lokasyon) çiftine yazıldığı için her testin kendi kalemini kurması
// zaten kuraldır (bkz. [yeniStokluVaryant]). Bu dosya bir adım ileri gider ve
// DEPOLARINI da kendisi kurar: senaryolar depoların İÇERİĞİNE bakar ve
// paylaşılan [stokLokasyonID] üzerinde çalışsalardı, aday listesi başka
// testlerin kalemlerine değil ama başka testlerin depo kurulumuna bağımlı
// hâle gelirdi.
//
// # Beklenen tutarlar neden elle yazılıyor
//
// Paket yorumundaki gerekçenin aynısı geçerlidir: her senaryonun ara toplamı,
// vergisi ve genel toplamı testin İÇİNDE kâğıt üstünde hesaplanmış
// SABİTLERDİR. Vergi satır başına ve aşağı yuvarlanarak hesaplanır.

// Çok depolu mutlu yol senaryosunun ELLE hesaplanmış tutarları.
//
// Bölge %20 (2000 baz puan) vergilidir, kargo yöntemi seçilmemiştir ve vergi
// SATIR BAŞINA hesaplanır:
//
//	A satırı: 7_500 × 2 = 15_000 ara toplam, 15_000 × %20 = 3_000 vergi
//	B satırı: 11_000 × 3 = 33_000 ara toplam, 33_000 × %20 = 6_600 vergi
//	sepet:    15_000 + 33_000 = 48_000 ara toplam
//	          3_000 + 6_600 = 9_600 vergi
//	          48_000 - 0 + 9_600 + 0 = 57_600 genel toplam
const (
	cokDepoFiyatA    int64 = 7_500
	cokDepoAdetA     int64 = 2
	cokDepoFiyatB    int64 = 11_000
	cokDepoAdetB     int64 = 3
	cokDepoAraToplam int64 = 48_000
	cokDepoVergi     int64 = 9_600
	cokDepoToplam    int64 = 57_600
	// cokDepoStokA ve cokDepoStokB kalemlerin TEK depolarındaki fiziksel
	// adetleridir; ikisi de sepetin istediğinden fazladır, yani senaryonun
	// durduğu yer stok yetersizliği DEĞİLDİR.
	cokDepoStokA int64 = 6
	cokDepoStokB int64 = 9
	// cokDepoKalanA ve cokDepoKalanB tahsilat sonrası beklenen FİZİKSEL
	// adetlerdir: 6 - 2 ve 9 - 3.
	cokDepoKalanA int64 = 4
	cokDepoKalanB int64 = 6
)

// TestLokasyonBossaSatirlarFarkliDepolardanAyrilir çok depolu bir sepetin
// siparişe döndüğünü ve satırlarının FARKLI depolardan ayrıldığını kanıtlar.
//
// Kurgu tek lokasyon varsayımını imkânsız kılar: A kaleminin stoğu yalnızca
// birinci depoda, B kaleminin stoğu yalnızca ikinci depodadır. Tek bir depodan
// ayırmaya çalışan bir uygulama satırlardan birinde MUTLAKA patlar, yani
// "sipariş oluştu" iddiası tek başına bile bir şey söyler — ama yeterli
// değildir ve test orada durmaz: her rezervasyon GERÇEK stok modülünden
// okunup lokasyonu doğrulanır. Yalnızca siparişin oluştuğuna bakan bir test,
// rezervasyonları sessizce tek depoya toplayan bir uygulamayı da geçirirdi.
//
// [CompleteCartInput.LocationID] bilinçli olarak BOŞ verilir: bu, akışa "depoyu
// sen belirle" demenin tek yoludur ve iş bölümü ancak o zaman devreye girer —
// "hangi depolarda yeterli stok var" olgusunu stok modülü, "hangisinden
// gönderelim" kararını kargo modülü verir.
func TestLokasyonBossaSatirlarFarkliDepolardanAyrilir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	depoA := yeniDepo(ctx, t, "E2E Çok Depo A")
	depoB := yeniDepo(ctx, t, "E2E Çok Depo B")

	varyantA, kalemA := depolaraDagilmisVaryant(ctx, t, "E2E Çok Depo Ürünü A",
		map[string]int64{vergiliParaBirimi: cokDepoFiyatA},
		map[string]int64{depoA: cokDepoStokA})
	varyantB, kalemB := depolaraDagilmisVaryant(ctx, t, "E2E Çok Depo Ürünü B",
		map[string]int64{vergiliParaBirimi: cokDepoFiyatB},
		map[string]int64{depoB: cokDepoStokB})

	sepetID, toplamlar := ikiSatirliSepet(ctx, t, musteriID, varyantA, cokDepoAdetA, varyantB, cokDepoAdetB)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: cokDepoAraToplam,
		indirim:   0,
		vergi:     cokDepoVergi,
		kargo:     0,
		toplam:    cokDepoToplam,
	}, "çok depolu sepet hazırlandıktan sonra")

	// --- akışın kendisi ---

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID: sepetID,
		// Lokasyon BİLİNÇLİ OLARAK boştur; senaryonun tamamı bu boşluğa dayanır.
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     cokDepoToplam,
	})
	require.NoError(t, err,
		"lokasyon bildirilmeden de sipariş verilebilmeli; hata, akışın hâlâ bir "+
			"lokasyon bildirilmesini şart koştuğunu gösterir")
	require.NotEmpty(t, sonuc.OrderID, "sonuç oluşan siparişin kimliğini taşımalı")
	require.True(t, sonuc.CartCompleted, "sepet tamamlanmış damgalanmalı")
	require.True(t, sonuc.ReservationsConfirmed, "rezervasyonlar kesinleştirilmeli")
	require.Empty(t, sonuc.Warnings,
		"mutlu yolda uyarı OLMAMALI; uyarı, pivot'tan sonra bir modülün patladığını "+
			"ve elle onarım gerektiğini bildirir")

	// --- 1) sipariş GERÇEKTEN oluştu mu ---

	siparis, err := siparisSvc.GetOrder(ctx, sonuc.OrderID)
	require.NoError(t, err, "oluşan sipariş sipariş modülünden okunabilmeli")
	require.Equal(t, ordermodels.OrderPending, siparis.Status,
		"sipariş bu akıştan 'pending' çıkmalı")
	require.Equal(t, cokDepoToplam, siparis.Total,
		"siparişin genel toplamı sepetinkiyle AYNI olmalı")
	require.Len(t, siparis.Items, 2,
		"sepetin İKİ satırı siparişe iki satır olarak geçmeli; satırların farklı "+
			"depolardan ayrılması siparişin şeklini DEĞİŞTİRMEZ — depo bir sevkiyat "+
			"ayrıntısıdır, faturanın değil")

	// --- 2) rezervasyonlar FARKLI depolarda mı ---
	//
	// Asıl iddia budur. Rezervasyonlar akışın döndürdüğü bir alandan değil,
	// GERÇEK stok modülünden okunur: sınanan şey akışın ne söylediği değil,
	// stok defterinde adedin hangi depodan düştüğüdür.
	require.Len(t, sonuc.ReservationIDs, 2,
		"her sepet satırı için bir rezervasyon alınmalı")
	rezervasyonlar := rezervasyonlariOku(ctx, t, sonuc.ReservationIDs)

	rezA, bulunduA := rezervasyonlar[kalemA]
	require.True(t, bulunduA, "A kaleminin rezervasyonu bulunmalı")
	rezB, bulunduB := rezervasyonlar[kalemB]
	require.True(t, bulunduB, "B kaleminin rezervasyonu bulunmalı")

	require.Equal(t, depoA, rezA.LocationID,
		"A satırı stoğunun BULUNDUĞU depodan ayrılmalı; başka bir depo, akışın "+
			"adayları hiç sormadan bir lokasyon uydurduğu anlamına gelir")
	require.Equal(t, depoB, rezB.LocationID,
		"B satırı da kendi deposundan ayrılmalı")
	require.NotEqual(t, rezA.LocationID, rezB.LocationID,
		"İKİ SATIR AYNI DEPODAN AYRILMAMALI. Bu, dosyanın var oluş sebebidir: "+
			"tek lokasyon varsayımı kaldırılmışsa bir siparişin satırları farklı "+
			"depolardan çıkabilmelidir. Eşitlik, akışın hâlâ tek bir depo seçip "+
			"tüm satırlara uyguladığını gösterir")

	require.Equal(t, cokDepoAdetA, rezA.Quantity, "A rezervasyonu satırın adedi kadar olmalı")
	require.Equal(t, cokDepoAdetB, rezB.Quantity, "B rezervasyonu satırın adedi kadar olmalı")
	require.Equal(t, inventorymodels.ReservationConfirmed, rezA.Status,
		"A rezervasyonu 'confirmed' olmalı: satılan mal fiziksel stoktan düşülmüş demektir")
	require.Equal(t, inventorymodels.ReservationConfirmed, rezB.Status,
		"B rezervasyonu da 'confirmed' olmalı")

	// --- 3) stok defteri DEPO BAZINDA doğru mu ---
	//
	// Satılabilir toplam tek başına yetmez: iki kalem ayrı ayrı sayıldığı için
	// "hepsi tek depodan düştü" hatası toplamda görünmezdi. Seviyeler bu yüzden
	// depo başına okunur.
	seviyeA := depoSeviyesi(ctx, t, kalemA, depoA)
	require.Equal(t, cokDepoKalanA, seviyeA.StockedQuantity,
		"A kaleminin FİZİKSEL adedi kendi deposunda azalmalı (%d - %d)",
		cokDepoStokA, cokDepoAdetA)
	require.Equal(t, int64(0), seviyeA.ReservedQuantity,
		"onaydan sonra A kaleminin rezerve adedi sıfırlanmalı")

	seviyeB := depoSeviyesi(ctx, t, kalemB, depoB)
	require.Equal(t, cokDepoKalanB, seviyeB.StockedQuantity,
		"B kaleminin FİZİKSEL adedi kendi deposunda azalmalı (%d - %d)",
		cokDepoStokB, cokDepoAdetB)
	require.Equal(t, int64(0), seviyeB.ReservedQuantity,
		"onaydan sonra B kaleminin rezerve adedi sıfırlanmalı")

	// Kalemlerin BAŞKA bir depoda seviyesi hiç doğmamalı: rezervasyon var
	// olmayan bir seviyeyi yaratamaz ve yaratsaydı stok yoktan var edilmiş
	// olurdu.
	require.Len(t, stokSeviyeleri(ctx, t, kalemA), 1,
		"A kalemi yalnızca kendi deposunda seviyelenmiş kalmalı")
	require.Len(t, stokSeviyeleri(ctx, t, kalemB), 1,
		"B kalemi yalnızca kendi deposunda seviyelenmiş kalmalı")
}

// Telafi senaryosunun sabitleri.
//
// İlk satırın stoğu TEK depoda ve yeterlidir. İkinci satırın stoğu İKİ depoya
// dağılmıştır ama hiçbiri tek başına yetmez: 1 + 1 = 2 < 3. Kurgu bilinçlidir
// ve çok depolu bir kurulumda en kolay gözden kaçan durumu seçer — toplam stok
// yeterli GÖRÜNÜR, ama rezervasyon tek bir depodan yapıldığı için sipariş
// karşılanamaz. Toplama bakan bir uygulama burada sipariş açardı.
//
// Tutarlar bu senaryonun konusu değildir: akış hesabı yaptıktan sonra ilk
// adımda, ikinci satırda durur.
const (
	telafiFiyat1          int64 = 9_000
	telafiAdet1           int64 = 2
	telafiStok1           int64 = 5
	telafiFiyat2          int64 = 4_000
	telafiAdet2           int64 = 3
	telafiDepoBasinaStok2 int64 = 1
)

// TestDeposuOlmayanSatirOncekiRezervasyonuBirakir hiçbir depoda yeterli stoğu
// olmayan bir satırın siparişi düşürdüğünü VE önceki satırın rezervasyonunun
// geri bırakıldığını kanıtlar.
//
// Bu, çok depoda en kolay bozulan yoldur. Tek depolu bir kurulumda ilk satır
// zaten patlardı ve telafi edilecek bir şey olmazdı; çok depoda ise ayırma
// satır satır ilerler ve ortasında durabilir. Geri bırakılmayan bir rezervasyon
// burada sessizce asılı kalır: satılmamış bir mal ayrılmış görünür, hiçbir
// sipariş onu tüketmediği için hata ancak sayımda fark edilir.
//
// Sınanan şey "hata döndü" değil, hatanın ARDINDA BIRAKTIĞIDIR: sipariş hiç
// açılmamalı ve ilk kalemin satılabilir adedi ESKİ değerine dönmelidir.
func TestDeposuOlmayanSatirOncekiRezervasyonuBirakir(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	depoA := yeniDepo(ctx, t, "E2E Telafi Depo A")
	depoB := yeniDepo(ctx, t, "E2E Telafi Depo B")

	varyant1, kalem1 := depolaraDagilmisVaryant(ctx, t, "E2E Telafi Ürünü 1",
		map[string]int64{vergiliParaBirimi: telafiFiyat1},
		map[string]int64{depoA: telafiStok1})
	// İkinci kalemin stoğu İKİ depoya dağıtılır ve hiçbiri tek başına yetmez.
	// Kalemi hiç seviyelendirmemek de listeyi boşaltırdı ama daha zayıf bir
	// kurgu olurdu: "stoğu hiç yok" ile "stoğu var ama hiçbir depoda yetmiyor"
	// farklı durumlardır ve bozulan yalnızca ikincisidir.
	varyant2, kalem2 := depolaraDagilmisVaryant(ctx, t, "E2E Telafi Ürünü 2",
		map[string]int64{vergiliParaBirimi: telafiFiyat2},
		map[string]int64{depoA: telafiDepoBasinaStok2, depoB: telafiDepoBasinaStok2})

	sepetID, _ := ikiSatirliSepet(ctx, t, musteriID, varyant1, telafiAdet1, varyant2, telafiAdet2)

	// Satır sırası bu testin ÖN KOŞULUDUR ve bu yüzden iddia edilir: "önceki
	// satırın rezervasyonu bırakıldı" ancak karşılanabilir satır ÖNCE
	// işlendiğinde bir şey kanıtlar. Sıra tersine dönerse akış ilk satırda
	// durur, hiç rezervasyon almaz ve testin geri kalanı sessizce boş bir
	// iddiaya dönüşürdü.
	sepet, err := sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.Len(t, sepet.Items, 2, "sepette iki satır olmalı")
	require.Equal(t, varyant1, sepet.Items[0].VariantID,
		"karşılanabilen satır sepette İLK sırada olmalı; sepet satırları "+
			"(created_at, id) sırasıyla okunur ve plan o sırayı devralır")

	oncekiSatilabilir1 := satilabilirAdet(ctx, t, kalem1)
	require.Equal(t, telafiStok1, oncekiSatilabilir1,
		"fikstür ilk kalemi beklenen adetle kurmuş olmalı")
	oncekiSatilabilir2 := satilabilirAdet(ctx, t, kalem2)
	require.Equal(t, 2*telafiDepoBasinaStok2, oncekiSatilabilir2,
		"ikinci kalemin TOPLAM satılabilir adedi sepetin istediğinden azdır ama "+
			"sıfır değildir; kurgunun anlamı budur")

	// --- akış PATLAMALI ---

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        "",
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
	})
	require.Error(t, err,
		"hiçbir depoda karşılanamayan bir satır varken sipariş verilememeli; "+
			"verilebilseydi mağaza teslim edemeyeceği bir malın parasını tahsil ederdi")
	require.Equal(t, checkoutwf.CompleteCartResult{}, sonuc,
		"hata dönen bir akış yarım bir sonuç SIZDIRMAMALI")

	require.True(t, errors.IsConflict(err),
		"sonuç errors.Conflict olmalı: girdi geçerlidir, dünyanın durumu elverişsizdir "+
			"ve istemci adedi düşürüp TEKRAR deneyebilir. Yeni bir hata sınıfı, "+
			"istemcinin bugün stok yetersizliği için yazdığı dalı bozardı. Dönen hata: %v", err)
	require.ErrorContains(t, err, checkoutwf.StepReserveInventory,
		"hata PATLAYAN ADIMI adlandırmalı; adım adı yürütme kaydına da yazılır")
	require.ErrorContains(t, err, checkoutwf.CodeReservationFailed,
		"hata kodu ayırmanın patladığı durumla AYNI olmalı; aday yokluğu için ayrı bir "+
			"kod uydurmak, aynı sonucu iki isimle raporlamak olurdu")
	require.ErrorContains(t, err, kalem2,
		"mesaj HANGİ kalemin karşılanamadığını yazmalı; ikinci kalemi adlandırması "+
			"aynı zamanda akışın İLK satırı geçtiğinin de kanıtıdır — ilk satırda "+
			"durulsaydı mesajda onun kimliği olurdu")

	// --- 1) sipariş HİÇ oluşmamalı ---
	//
	// create_order, reserve_inventory'den SONRA gelir; stok adımı patladığı için
	// hiç çalışmamalıdır. İptal edilmiş bile olsa bir sipariş kaydı, hiç
	// denenmemiş bir siparişin var olduğu anlamına gelirdi.
	siparisler, toplamSayi, err := siparisSvc.ListOrders(ctx, ordersvc.ListOrdersInput{CustomerID: &musteriID})
	require.NoError(t, err, "müşterinin siparişleri okunabilmeli")
	require.Empty(t, siparisler, "sipariş HİÇ oluşmamalı")
	require.Zero(t, toplamSayi, "sayaç da sıfır olmalı")

	// --- 2) ÖNCEKİ SATIRIN REZERVASYONU BIRAKILDI MI ---
	//
	// Testin çekirdeği burasıdır. İlk satır başarıyla ayrılmıştı; ikinci satır
	// hiçbir depoda karşılanamayınca adım kendi temizliğini yapmak zorundadır.
	// Yapmazsa 2 adet mal sonsuza kadar ayrılmış kalır.
	require.Equal(t, oncekiSatilabilir1, satilabilirAdet(ctx, t, kalem1),
		"ilk kalemin satılabilir adedi ESKİ değerine dönmeli (%d). Dönmezse ayrılan "+
			"stok asılı kalır ve satılmamış mal satılamaz hâle gelir", oncekiSatilabilir1)

	seviye1 := depoSeviyesi(ctx, t, kalem1, depoA)
	require.Equal(t, telafiStok1, seviye1.StockedQuantity,
		"FİZİKSEL adet hiç değişmemeli: geri bırakma onaylanmamış bir sözü siler, "+
			"stoktan düşme yalnızca onayla olur")
	require.Equal(t, int64(0), seviye1.ReservedQuantity,
		"ilk kalemin rezerve adedi SIFIRA dönmeli; sıfırdan farklı kalması, sözün "+
			"hâlâ ayakta olduğu — yani telafinin çalışmadığı — anlamına gelir")

	// --- 3) ikinci kaleme HİÇ dokunulmamış olmalı ---
	//
	// Aday listesi boşken kısmi bir ayırma (eldeki 1 adet) denenmiş olsaydı,
	// sepetin tamamı karşılanamadığı hâlde stok geçici olarak kilitlenirdi.
	require.Equal(t, oncekiSatilabilir2, satilabilirAdet(ctx, t, kalem2),
		"ikinci kalemin satılabilir adedi hiç değişmemeli; ayırma ya TAMAMEN olur "+
			"ya hiç olmaz")
	for _, depo := range []string{depoA, depoB} {
		require.Equal(t, int64(0), depoSeviyesi(ctx, t, kalem2, depo).ReservedQuantity,
			"%s deposunda ikinci kalem için rezerve adet doğmamalı", depo)
	}

	// --- 4) sepet açık kalmalı ---
	sepet, err = sepetSvc.GetCart(ctx, sepetID)
	require.NoError(t, err, "sepet modülünden okunabilmeli")
	require.False(t, sepet.Completed(),
		"sepet tamamlanmış damgalanMAMALI; müşteri adedi düşürüp yeniden "+
			"deneyebilmelidir")
}

// Bildirilen lokasyon senaryosunun ELLE hesaplanmış tutarları.
//
//	15_000 × 2 = 30_000 ara toplam
//	30_000 × %20 = 6_000 vergi
//	30_000 - 0 + 6_000 + 0 = 36_000 genel toplam
const (
	bildirilenFiyat          int64 = 15_000
	bildirilenAdet           int64 = 2
	bildirilenAraToplam      int64 = 30_000
	bildirilenVergi          int64 = 6_000
	bildirilenToplam         int64 = 36_000
	bildirilenDepoBasinaStok int64 = 4
	// bildirilenKalan tahsilat sonrası bildirilen depodaki fiziksel adettir:
	// 4 - 2.
	bildirilenKalan int64 = 2
)

// TestBildirilenLokasyonSecimYaptirmaz lokasyonu BİLDİREN çağrının eski
// davranışının birebir korunduğunu kanıtlar.
//
// Geriye uyumluluk testi, çok depolu kurulumda anlamlı olacak şekilde
// kurgulanır: kalemin stoğu İKİ depoda da yeterlidir, yani seçim yapılsaydı
// gerçekten bir seçim olurdu. Bildirilen depo, kargo modülünün bugünkü
// politikasının SEÇMEYECEĞİ depodur (politika kimliği en küçük adayı seçer);
// böylece rezervasyonun bildirilen depoda açılması, akışın seçimi hiç
// yapmadığının kanıtı olur. İki deponun ikisinde de stok bulunması şarttır —
// tek depoda stok olsaydı "bildirileni kullandı" ile "tek adayı seçti" ayırt
// edilemezdi.
//
// Bildirilen lokasyon bir tercih değil TALİMATTIR: belirli bir depodan çıkacak
// bir yönetim siparişi ya da tek depolu bir kurulum, seçimin hiç yapılmamasını
// ister.
func TestBildirilenLokasyonSecimYaptirmaz(t *testing.T) {
	ctx := t.Context()

	musteriID, eposta := yeniMusteri(ctx, t)
	depoBir := yeniDepo(ctx, t, "E2E Bildirilen Depo 1")
	depoIki := yeniDepo(ctx, t, "E2E Bildirilen Depo 2")

	// Bildirilen depo, politikanın seçmeyeceği olan seçilir. Hangisinin
	// kimliğinin büyük olduğu KOŞU SIRASINDA karşılaştırılarak bulunur:
	// kimlikler üretilir ve "ikinci oluşturulanın kimliği büyüktür" varsayımı,
	// kimlik üreticisi değiştiği gün testi sessizce anlamsızlaştırırdı.
	bildirilenDepo, politikaninSececegi := depoBir, depoIki
	if politikaninSececegi > bildirilenDepo {
		bildirilenDepo, politikaninSececegi = politikaninSececegi, bildirilenDepo
	}

	varyantID, stokKalemID := depolaraDagilmisVaryant(ctx, t, "E2E Bildirilen Lokasyon Ürünü",
		map[string]int64{vergiliParaBirimi: bildirilenFiyat},
		map[string]int64{
			bildirilenDepo:      bildirilenDepoBasinaStok,
			politikaninSececegi: bildirilenDepoBasinaStok,
		})

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, bildirilenAdet)
	toplamlariDogrula(t, toplamlar, beklenenToplam{
		araToplam: bildirilenAraToplam,
		indirim:   0,
		vergi:     bildirilenVergi,
		kargo:     0,
		toplam:    bildirilenToplam,
	}, "bildirilen lokasyon sepeti hazırlandıktan sonra")

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        bildirilenDepo,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             eposta,
		ExpectedTotal:     bildirilenToplam,
	})
	require.NoError(t, err,
		"lokasyon bildiren çağrı eskisi gibi çalışmalı; çok depo desteği eski yolu "+
			"BOZMAMALIDIR")
	require.True(t, sonuc.ReservationsConfirmed, "rezervasyonlar kesinleştirilmeli")
	require.Len(t, sonuc.ReservationIDs, 1, "tek satır için tek rezervasyon alınmalı")

	rezervasyon, err := stokSvc.GetReservation(ctx, sonuc.ReservationIDs[0])
	require.NoError(t, err, "rezervasyon stok modülünden okunabilmeli")
	require.Equal(t, bildirilenDepo, rezervasyon.LocationID,
		"rezervasyon BİLDİRİLEN depoda açılmalı. Diğer depo (%s) da yeterli stok "+
			"taşıyor ve kargo modülünün politikası onu seçerdi; bildirilenin dışında "+
			"bir depo, akışın çağıranın talimatını 'aday' sayıp sessizce değiştirdiği "+
			"anlamına gelir", politikaninSececegi)

	seviyeBildirilen := depoSeviyesi(ctx, t, stokKalemID, bildirilenDepo)
	require.Equal(t, bildirilenKalan, seviyeBildirilen.StockedQuantity,
		"bildirilen deponun FİZİKSEL adedi azalmalı (%d - %d)",
		bildirilenDepoBasinaStok, bildirilenAdet)
	require.Equal(t, int64(0), seviyeBildirilen.ReservedQuantity,
		"onaydan sonra rezerve adet sıfırlanmalı")

	seviyeDiger := depoSeviyesi(ctx, t, stokKalemID, politikaninSececegi)
	require.Equal(t, bildirilenDepoBasinaStok, seviyeDiger.StockedQuantity,
		"diğer deponun stoğuna HİÇ dokunulmamalı")
	require.Equal(t, int64(0), seviyeDiger.ReservedQuantity,
		"diğer depoda rezerve adet doğmamalı; doğması, adedin iki depoya bölündüğü "+
			"anlamına gelirdi")
}

// yeniDepo bir stok lokasyonu oluşturur ve kimliğini döner.
//
// Depo TestMain'de değil TEST BAŞINA kurulur ([stokLokasyonID]'nin tersine):
// bu dosyanın senaryoları "hangi depoda ne kadar stok var" olgusuna bakar ve
// paylaşılan bir depo, aday listesini başka testlerin kurulumuna bağımlı
// kılardı. Ada sayaç eklenir ki aynı senaryo iki kez koşsa bile depolar
// kayıtta ayırt edilebilsin.
//
// Ülke kodu vergili bölgeninkiyle aynıdır; akış lokasyonun ülkesini bugün
// kullanmaz, fikstür yalnızca gerçekçi kalır.
func yeniDepo(ctx context.Context, t *testing.T, ad string) string {
	t.Helper()

	sira := fiksturSayaci.Add(1)
	lokasyon, err := stokSvc.CreateStockLocation(ctx, inventorysvc.CreateStockLocationInput{
		Name:        fmt.Sprintf("%s #%d", ad, sira),
		CountryCode: vergiliUlke,
	})
	require.NoError(t, err, "fikstür deposu oluşturulamadı")
	return lokasyon.ID
}

// depolaraDagilmisVaryant fiyatı olan bir varyant kurar, stoğunu VERİLEN
// depolara dağıtır ve varyant ile stok kaleminin kimliklerini döner.
//
// [yeniStokluVaryant]'tan tek farkı stoğun nereye yazıldığıdır: orası
// paylaşılan tek depoyu kullanır, burası depo başına adet alır. Ayrı bir
// fikstür olması bilinçlidir — paylaşılan fikstürü çok depolu hâle getirmek,
// hiçbiri depolarla ilgilenmeyen onlarca senaryoyu da yeniden yazmak olurdu.
//
// Depolar KİMLİĞE göre sıralı gezilir: harita üzerinde dönmek seviyelerin
// yazılma sırasını koşudan koşuya değiştirir ve bir arıza ayıklanamaz hâle
// gelirdi.
func depolaraDagilmisVaryant(
	ctx context.Context,
	t *testing.T,
	baslik string,
	fiyatlar map[string]int64,
	stoklar map[string]int64,
) (varyantID, stokKalemID string) {
	t.Helper()

	require.NotEmpty(t, stoklar,
		"çok depolu fikstür en az bir depo istemeli; depo verilmezse kalem hiç "+
			"seviyelenmez ve senaryo 'stoğu yok' durumunu sınamaya başlardı")

	varyantID = yeniVaryant(ctx, t, baslik, fiyatlar)

	sira := fiksturSayaci.Add(1)
	kalem, err := stokSvc.CreateInventoryItem(ctx, inventorysvc.CreateInventoryItemInput{
		SKU:   fmt.Sprintf("E2E-DEPO-SKU-%d", sira),
		Title: baslik,
	})
	require.NoError(t, err, "fikstür stok kalemi oluşturulamadı")

	require.NoError(t, urunSvc.SetVariantInventoryItem(ctx, varyantID, kalem.ID),
		"varyant stok kalemine bağlanamadı; bağ olmadan akış varyantı stoksuz sayar")

	for _, lokasyonID := range slices.Sorted(maps.Keys(stoklar)) {
		seviye, err := stokSvc.SetInventoryLevel(ctx, kalem.ID, lokasyonID, stoklar[lokasyonID])
		require.NoError(t, err, "%s deposunda fikstür stok seviyesi yazılamadı", lokasyonID)
		require.Equal(t, stoklar[lokasyonID], seviye.Available(),
			"yeni seviyede satılabilir adet fiziksel adede eşit olmalı; eşit değilse "+
				"fikstür daha başlarken rezerve stok taşıyor demektir")
	}

	return varyantID, kalem.ID
}

// ikiSatirliSepet kayıtlı bir müşteriye sepet açar, İKİ satır ekler ve sepetin
// kimliğiyle hesaplanmış toplamlarını döner.
//
// [sepetHazirla] tek satır ekler; çok depolu senaryolar en az iki satır ister
// çünkü sınanan şey satırların FARKLI depolardan ayrılabilmesidir. Satırlar
// verilen sırayla eklenir ve sıra bir ayrıntı değildir: sepet satırları
// (created_at, id) sırasıyla okunur, plan o sırayı devralır, dolayısıyla telafi
// senaryosunda "önceki satır" ilk eklenen satırdır.
//
// Sepet KAYITLI müşteriye açılır; gerekçe [sepetHazirla]'dakiyle aynıdır:
// senaryolar siparişleri müşteriye göre okur ve misafir siparişleri
// birbirininkini görürdü.
func ikiSatirliSepet(
	ctx context.Context,
	t *testing.T,
	musteriID, ilkVaryantID string,
	ilkAdet int64,
	ikinciVaryantID string,
	ikinciAdet int64,
) (sepetID string, toplamlar cartwf.Totals) {
	t.Helper()

	sepet, err := akislar.CreateCart(ctx, cartwf.CreateCartInput{
		CountryCode: vergiliUlke,
		CustomerID:  musteriID,
	})
	require.NoError(t, err, "fikstür sepeti açılamadı")

	_, err = akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: ilkVaryantID,
		Quantity:  ilkAdet,
	})
	require.NoError(t, err, "fikstür sepetine ilk satır eklenemedi")

	ikinci, err := akislar.AddLineItem(ctx, cartwf.AddLineItemInput{
		CartID:    sepet.CartID,
		VariantID: ikinciVaryantID,
		Quantity:  ikinciAdet,
	})
	require.NoError(t, err, "fikstür sepetine ikinci satır eklenemedi")

	return sepet.CartID, ikinci.Totals
}

// stokSeviyeleri kalemin TÜM lokasyonlardaki seviyelerini döner.
//
// [stokSeviyesi] burada kullanılamaz: o, kalemin TEK bir lokasyonda
// seviyelendiğini şart koşar ve bu dosyanın kalemleri bilinçli olarak birden
// çok depoya yayılır.
func stokSeviyeleri(ctx context.Context, t *testing.T, stokKalemID string) []inventorymodels.InventoryLevel {
	t.Helper()

	seviyeler, err := stokSvc.ListInventoryLevels(ctx, stokKalemID)
	require.NoError(t, err, "stok seviyeleri okunamadı")
	return seviyeler
}

// depoSeviyesi kalemin BELİRLİ bir depodaki seviyesini döner.
//
// Seviye aranarak bulunur, dilimin sırasına güvenilmez: sıra stok modülünün
// sorgusuna aittir ve değişmesi, testin sessizce yanlış deponun adetlerini
// doğrulamasına yol açardı. Seviye bulunamazsa test DÜŞER — "o depoda seviye
// yok" ile "o depoda adet sıfır" farklı şeylerdir ve ikincisini varsaymak,
// rezervasyonun hiç dokunmadığı bir depoyu doğrulanmış saymak olurdu.
func depoSeviyesi(
	ctx context.Context,
	t *testing.T,
	stokKalemID, lokasyonID string,
) inventorymodels.InventoryLevel {
	t.Helper()

	for _, seviye := range stokSeviyeleri(ctx, t, stokKalemID) {
		if seviye.LocationID == lokasyonID {
			return seviye
		}
	}

	require.FailNow(t, "stok seviyesi bulunamadı",
		"%s kalemi %s deposunda seviyelenmiş olmalı", stokKalemID, lokasyonID)
	return inventorymodels.InventoryLevel{}
}

// rezervasyonlariOku akıştan dönen rezervasyon kimliklerini GERÇEK stok
// modülünden okur ve STOK KALEMİNE göre eşler.
//
// Eşleme kimliğe göre yapılır, dilimin sırasına göre değil: sıra akışın satır
// sırasından gelir ve ona bağlı bir iddia, sıra değiştiği gün yanlış satırı
// doğrulamış olurdu.
//
// Okuma stok modülünden yapılır, akışın döndürdüğü bir alandan DEĞİL: sınanan
// iddia, rezervasyonun stok modülünün DEFTERİNDE seçilen depoda açıldığıdır.
// Akışın kendi çıktısına bakmak yalnızca akışın ne söylediğini doğrulardı.
func rezervasyonlariOku(
	ctx context.Context,
	t *testing.T,
	kimlikler []string,
) map[string]inventorymodels.Reservation {
	t.Helper()

	kalemBasina := make(map[string]inventorymodels.Reservation, len(kimlikler))
	for _, kimlik := range kimlikler {
		rezervasyon, err := stokSvc.GetReservation(ctx, kimlik)
		require.NoError(t, err, "%s rezervasyonu stok modülünden okunamadı", kimlik)
		_, tekrar := kalemBasina[rezervasyon.InventoryItemID]
		require.False(t, tekrar,
			"aynı stok kalemi için İKİ rezervasyon alınmış (%s); her sepet satırı "+
				"tek bir rezervasyon almalı", rezervasyon.InventoryItemID)
		kalemBasina[rezervasyon.InventoryItemID] = rezervasyon
	}
	return kalemBasina
}
