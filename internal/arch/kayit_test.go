package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Bileşim köklerinin ve çekirdek sözleşmesinin yolları.
const (
	// bilesimKoku üretim ikilisinin modül kaydını kurduğu pakettir. Kaynağı
	// AYRIŞTIRILIR, import EDİLMEZ: main paketi import edilemez.
	bilesimKoku = "cmd/server"
	// e2eZemini uçtan uca testlerin modülleri gerçek migration'larla ayağa
	// kaldırdığı pakettir. O paket "integration" derleme etiketinin
	// arkasındadır; ayrıştırma etiketi umursamaz, bu yüzden bu denetim
	// entegrasyon koşusu olmadan da çalışır.
	e2eZemini = "internal/e2e"
	// cekirdekModulPaketi [module.Module] sözleşmesinin ve [module.Registry]
	// kaydının yaşadığı çekirdek paketidir.
	cekirdekModulPaketi = modulePath + "/internal/core/module"
	// akisDizini modüller arası akışların yaşadığı ağaçtır (ADR 0006). Ne
	// çekirdektir ne de modül: depguard kuralları internal/modules içindir,
	// modül kayıt denetimi de internal/modules altını gezer — yani bu ağacı
	// bugüne kadar HİÇBİR kablolama kuralı kapsamıyordu.
	akisDizini = "internal/workflows"
	// cekirdekContainerPaketi container'ın yaşadığı çekirdek paketidir. Bir
	// akış paketinin "container'dan kurulmak üzere tasarlandığı" işareti, dışa
	// açık bir işlevinin bu tipi PARAMETRE olarak almasıdır.
	cekirdekContainerPaketi = modulePath + "/internal/core/container"
	// kurulumIsaretiAdi akış yapıcılarının konvansiyonel adıdır.
	//
	// İleri yön denetimi bu adı KULLANMAZ (şekle bakar, bkz.
	// [containerdanKurulanAkisPaketleri]); ad yalnızca TERS yönde, bileşim
	// kökünün kurduğu ama denetimin yapıcı olarak GÖRMEDİĞİ bir paketi
	// yakalamak için gerekir. Bayatlığı [TestHerAkisBilesimKokundeKurulu]
	// içinde doğrulanır.
	kurulumIsaretiAdi = "FromContainer"
)

// modulSozlesmesi [module.Module] arayüzünün metot kümesidir: metot adından
// beklenen parametre ve sonuç tiplerine.
//
// İmzalar KAYNAK METİN olarak karşılaştırılır (go/types.ExprString), çünkü bu
// denetimin tip denetleyicisi çalıştırması gerekmez ve gerekseydi go.mod'a yeni
// bir bağımlılık girerdi. Bunun bedeli, paketin farklı bir takma adla import
// edilmesi hâlinde eşleşmenin kaçmasıdır; bedel [modulUygulayanPaketler]
// içinde ödenir: dört metot ADI da bulunup imza tutmazsa test SESSİZ
// GEÇMEZ, hata verir.
var modulSozlesmesi = map[string]struct {
	parametreler []string
	sonuclar     []string
}{
	"Name": {sonuclar: []string{"string"}},
	"Register": {
		parametreler: []string{"context.Context", "*container.Container"},
		sonuclar:     []string{"error"},
	},
	"Migrations": {sonuclar: []string{"fs.FS"}},
	"Routes":     {parametreler: []string{"chi.Router"}},
}

// kayitDisiModuller bileşim kökünde BİLİNÇLİ olarak kayıt dışı bırakılan modül
// paketlerinin gerekçeleridir; anahtar paketin import yoludur.
//
// # Muafiyet neden var
//
// Yazılmış ama henüz kablolanmamış bir modül gerçek bir durumdur (yarım kalmış
// bir faz, yalnızca gömülü kullanım için düşünülmüş bir modül). Böyle bir
// modülü kayıt DIŞI bırakmayı imkânsız kılmak, geliştiriciyi testi susturmanın
// başka bir yolunu aramaya iterdi ve o yol her zaman daha sessiz olurdu.
//
// # Muafiyet neden BURADA
//
// İşaret, dizinde duran ".gitkeep" gibi sessiz bir dosya DEĞİL, gerekçesi
// yazılmış bir kod satırıdır: gerekçe kod incelemesinde görünür, git blame'de
// bir tarihi ve sahibi olur, gerekçesiz eklenemez (değer zorunludur) ve
// aşağıdaki BAYAT MUAFİYET denetimi sayesinde çürüyemez — muaf tutulan modül
// bir gün kaydedilirse ya da paket silinirse test yine düşer. Yani muafiyet
// borçtur ve borç görünür durur.
//
// Bugün boştur: deponun on beş modülünün on beşi de kayıtlıdır.
var kayitDisiModuller = map[string]string{}

// e2eZemininDisiModuller e2e zemininde BİLİNÇLİ olarak kurulmayan modül
// paketlerinin gerekçeleridir; anahtar paketin import yoludur.
//
// Ayrı bir harita tutulmasının sebebi, iki muafiyetin FARKLI şeyleri
// anlatmasıdır: [kayitDisiModuller] "bu modül üretimde yok" der,
// bu harita "bu modül üretimde var ama uçtan uca zeminde koşturulamıyor" der
// (örneğin dış bir servise zorunlu bağımlılığı olan bir modül). İkincisi
// birincisinden çok daha hafif bir borçtur ve aynı listede durmaları, ağır
// olanı hafif gösterirdi.
//
// Bugün boştur: zemin üretimin kaydettiği her modülü kurar.
var e2eZemininDisiModuller = map[string]string{}

// kurulmayanAkislar bileşim kökünde BİLİNÇLİ olarak kurulmayan akış
// paketlerinin gerekçeleridir; anahtar paketin import yoludur.
//
// Muafiyetin neden var olduğu ve neden BURADA olduğu [kayitDisiModuller]
// godoc'unda anlatılmıştır ve tekrarlanmıyor. Bu haritanın ayrı durmasının
// sebebi, borcun BÜYÜKLÜĞÜNÜN farklı olmasıdır: kaydedilmemiş bir modül
// yalnızca kendi yüzeyini kaybettirir, kurulmamış bir akış ise onu ADLA çözen
// modül uçlarını da kapatır (bkz. cart modülündeki linePricing). İki borcu
// aynı listede tutmak, ağır olanı hafif gösterirdi.
//
// Bugün boştur: internal/workflows'un iki paketi de bileşim kökünde kuruludur.
var kurulmayanAkislar = map[string]string{}

// ayristirilmisDosya bir Go dosyasının ayrıştırılmış hâlini ve import takma
// adlarını taşır.
type ayristirilmisDosya struct {
	yol       string
	agac      *ast.File
	importlar map[string]string
}

// akisKurulumu bileşim kökünde bulunan bir akış kurulumunun yeridir.
type akisKurulumu struct {
	// konum yapıcıya yapılan başvurunun kaynak konumudur.
	konum token.Position
	// kapsayan başvuruyu içeren işlevin adıdır; paket düzeyindeki bir
	// bildirimdeyse boştur.
	kapsayan string
	// yapici çağrılan yapıcının adıdır.
	yapici string
	// sayilmiyor başvurunun bulunduğunu ama GEÇERLİ bir kurulum sayılmadığını
	// söyler (ölü kod ya da çağrılmayan başvuru). Sebebi bulunduğu yerde
	// raporlanmıştır; çağıran taraf ikinci bir hata üretmez.
	sayilmiyor bool
}

// TestHerModulBilesimKokundeKayitli "yazılan her modül BİLEŞİM KÖKÜNDE
// kayıtlı" değişmezini denetler.
//
// # Hangi arıza sınıfı
//
// Bu deponun en pahalı hata sınıfı, TÜKETİCİSİ OLMAYAN YETENEKTİR: Faz 8 ve
// Faz 9'un tamamı yazılmış, testleri yeşilken /admin/v1/** uçlarının HİÇBİRİ
// mount edilmemişti — yönetim yüzeyi bir kurulumda hiç var olmadı. Aynısı b2b
// modülünde tekrarlandı: modül, migration'ı, uçları ve testleri hazırdı ama
// cmd/server'da kaydı yoktu, yani harcama limiti HİÇBİR kurulumda
// uygulanmıyordu ve order her müşteriyi sınırsız sayıyordu.
//
// İki arızanın da ortak yanı, hiçbir testin düşmemesidir: modülün kendi
// testleri modülü kendisi kurar, bu yüzden yeşildir. Eksik olan şey modülde
// değil, modül ile bileşim kökü ARASINDADIR ve orayı denetleyen kimse yoktu.
//
// # Neden liste tutmuyor
//
// Denetim modül dizinlerini GEZER ve [module.Module] sözleşmesini kimin
// karşıladığını metot kümesinden çıkarır. Elle yazılmış bir modül listesi,
// kuralı yalnızca BUGÜN için uygulardı: on altıncı modülü yazan kişi listeyi
// güncellemeyi unuttuğunda test yine yeşil kalırdı — yani tam olarak
// yakalaması gereken hatayı kaçırırdı.
//
// # Neden ayrıştırıyor, import etmiyor
//
// Bileşim kökü main paketidir ve import EDİLEMEZ. Kayıt bu yüzden kaynaktan
// okunur: [module.Registry] değişkeni bulunur, üzerindeki Add çağrıları
// toplanır ve her çağrının argümanının hangi pakete gittiği dosyanın import
// listesinden çözülür.
func TestHerModulBilesimKokundeKayitli(t *testing.T) {
	t.Parallel()

	moduller := modulUygulayanPaketler(t)
	require.NotEmpty(t, moduller,
		"internal/modules altında [module.Module] uygulayan hiçbir paket bulunamadı; "+
			"denetim KÖR kalmış olmalı (sözleşme mi değişti?)")

	kayitli := kayitliModulPaketleri(t, bilesimKoku, false)

	for _, yol := range slices.Sorted(maps.Keys(moduller)) {
		if _, kayitliMi := kayitli[yol]; kayitliMi {
			continue
		}
		if gerekce, muaf := kayitDisiModuller[yol]; muaf {
			t.Logf("%s bileşim kökünde bilinçli olarak KAYITLI DEĞİL: %s", yol, gerekce)
			continue
		}
		t.Errorf("%s paketi [module.Module]'ü uyguluyor (%s) ama %s/'daki kayda EKLENMİYOR.\n"+
			"Kaydedilmeyen bir modül hiçbir kurulumda yoktur: migration'ı uygulanmaz, "+
			"servisi container'a girmez, uçları mount edilmez ve modülün kendi testleri "+
			"yeşil kaldığı için bu hiçbir yerde görünmez.\n"+
			"Ya cmd/server/main.go'da registry.Add(...) satırını ekleyin, ya da modülü "+
			"gerekçesiyle birlikte kayitDisiModuller haritasına yazın.",
			yol, strings.Join(moduller[yol], ", "), bilesimKoku)
	}

	// Ters yön denetimin KENDİ kör noktasını kapatır: bileşim kökünün
	// kaydettiği bir internal/modules paketini denetim modül olarak GÖRMÜYORSA,
	// sözleşme okuması kaymış demektir. O andan sonra bu test "her modül
	// kayıtlı" değil "gördüğüm modüller kayıtlı" derdi — yani yarın yazılan
	// modülü sessizce kapsam dışı bırakır ve yeşil kalırdı.
	for _, yol := range slices.Sorted(maps.Keys(modulPaketlerineSuz(kayitli))) {
		if _, gorulduMu := moduller[yol]; !gorulduMu {
			t.Errorf("%s paketi %s/'da kayıtlı ama denetim onu [module.Module] "+
				"uygulayan bir paket olarak GÖRMÜYOR.\n"+
				"Kayıt bir modül olduğunu kanıtlar; denetimin görmemesi, sözleşme "+
				"okumasının (modulSozlesmesi) gerçekle ayrıştığı anlamına gelir ve bu "+
				"testi bundan sonra kör bırakır.", yol, bilesimKoku)
		}
	}

	bayatMuafiyetleriDenetle(t, kayitDisiModuller, moduller,
		"[module.Module] uygulayan bir paket", kayitli, bilesimKoku)
}

// TestKayitliHerModulE2EZemindeKurulu bileşim kökünde kayıtlı her modülün
// uçtan uca zeminde de kurulduğunu denetler.
//
// # Değişmezin ikinci yarısı neden gerekli
//
// Birinci yarı ([TestHerModulBilesimKokundeKayitli]) "yazılan modül üretimde
// kayıtlı mı" diye sorar. Tek başına yetmez, çünkü KAYIT ile ÇALIŞMA aynı şey
// değildir: registry.Add satırı derlenir, açılışta koşar ve yine de o modülün
// gerçek migration'ı, gerçek route'ları ve modüller arası bağı hiçbir testte
// bir arada denenmemiş olabilir. b2b tam olarak böyleydi — kaydın kendisi bir
// satırdı ve o satırın order modülünün davranışını değiştirdiğini gösteren tek
// yer e2e zeminidir.
//
// # Neden e2e zemini, neden başka bir zemin değil
//
// Zemin, kurulumun üretimdeki hâlinin tek kopyasıdır: aynı çekirdek servis
// adları, aynı [module.Registry], gerçek PostgreSQL, gerçek migration'lar ve
// yetki denetimini router AĞACINI gezerek yapan testler (bkz.
// internal/e2e/yetki_test.go, 196 yönetim ucu). Bir modül o zemine girdiği anda
// mevcut ağaç gezen testlerin kapsamına da girer; girmediği sürece ne kadar
// test yazılırsa yazılsın kendi kabarcığında kalır.
//
// # Bu test Faz 8/9 arızasını görür müydü
//
// Doğrudan değil, birinci yarı görürdü. Ama zemin ile üretimin AYNI kümeyi
// tutması şartı, arızanın oluşmasını engellerdi: Faz 8/9 modülleri zemine
// eklendiği anda bu test üretimde eksik olan kaydı isterdi; zemine de
// eklenmeselerdi birinci yarı zaten düşerdi. İki yarı birlikte, "modül var ama
// hiçbir bileşim kökünde yok" durumunu kapatır.
//
// # Neden internal/e2e'ye dokunulmuyor
//
// e2e_test.go zaten "Modül kümesi ve sırası cmd/server/main.go'dakinin
// aynısıdır" diye YAZIYOR. Bu bir yorum vaadidir ve bu depodaki üçüncü hata
// sınıfı tam olarak budur: godoc'un vaadi ile kodun davranışının ayrışması.
// Vaadi zorlayan şey vaadin yanına yazılacak bir satır değil, onu dışarıdan
// denetleyen bir testtir; test o dosyayı DEĞİŞTİRMEZ, yalnızca okur.
func TestKayitliHerModulE2EZemindeKurulu(t *testing.T) {
	t.Parallel()

	uretim := modulPaketlerineSuz(kayitliModulPaketleri(t, bilesimKoku, false))
	zemin := modulPaketlerineSuz(kayitliModulPaketleri(t, e2eZemini, true))
	require.NotEmpty(t, zemin,
		"e2e zemininde hiçbir modül kaydı bulunamadı; denetim KÖR kalmış olmalı")

	for _, yol := range slices.Sorted(maps.Keys(uretim)) {
		if _, kuruluMu := zemin[yol]; kuruluMu {
			continue
		}
		if gerekce, muaf := e2eZemininDisiModuller[yol]; muaf {
			t.Logf("%s e2e zemininde bilinçli olarak KURULMUYOR: %s", yol, gerekce)
			continue
		}
		t.Errorf("%s modülü %s/'da kayıtlı (%s) ama %s/ zemininde KURULMUYOR.\n"+
			"Zemine girmeyen bir modülün üretim kablolaması hiçbir yerde uçtan uca "+
			"denenmez: migration'ı gerçek veritabanında koşmaz, uçları router ağacını "+
			"gezen yetki denetiminin kapsamına girmez ve modüller arası bağı yalnızca "+
			"TAKLİT karşılıklarla sınanır.\n"+
			"Ya zemine ekleyin, ya da modülü gerekçesiyle e2eZemininDisiModuller "+
			"haritasına yazın.",
			yol, bilesimKoku, uretim[yol], e2eZemini)
	}

	// Ters yön BURADA denetlenmez ve bu bir eksiklik değil: zeminde kurulu olup
	// üretimde kayıtlı OLMAYAN bir modül, tam olarak Faz 8/9 arızasıdır ve
	// [TestHerModulBilesimKokundeKayitli] onu zaten düşürür. İki testin aynı
	// şeyi iki kez söylemesi, biri değiştiğinde ötekinin sessizce gereksizleşmesi
	// demek olurdu.
	bayatMuafiyetleriDenetle(t, e2eZemininDisiModuller, uretim,
		"bileşim kökünde kayıtlı bir modül", zemin, e2eZemini)
}

// TestHerAkisBilesimKokundeKurulu "container'dan kurulmak üzere yazılan her
// akış BİLEŞİM KÖKÜNDE gerçekten kurulur" değişmezini denetler.
//
// # Hangi arıza sınıfı
//
// Yukarıdaki iki testin kapattığı sınıfın ta kendisi — ama onların kapsamı
// DIŞINDA kalmış bir örneğiyle. internal/workflows/cart ve
// internal/workflows/checkout yazılmış, birim testleri yeşil, uçtan uca
// zeminde kanıtlanmış ve bileşim köküne HİÇ bağlanmamıştı: cmd/server yalnızca
// saga MOTORUNU kaydediyordu, iki akışın FromContainer'ını üretim kodunda
// çağıran kimse yoktu. Yani çalışan ikilide sepeti siparişe çeviren yol
// yoktu — ödeme, kargo, checkout promosyonu, order.placed bildirimi ve b2b
// harcama limiti erişilemezdi — üstelik README onu sunulan bir yetenek gibi
// anlatıyordu.
//
// [TestHerModulBilesimKokundeKayitli] bunu göremezdi: o denetim
// internal/modules altını gezer ve [module.Module] sözleşmesini arar. Akışlar
// modül DEĞİLDİR (dört metodu taşımazlar) ve internal/modules altında
// durmazlar, yani değişmezin KAPSAMI, kapatması gereken sınıfın bir örneğini
// dışarıda bırakmıştı. Bu test o kapsamı genişletir.
//
// # İşaret neden ŞEKİL, neden "FromContainer" adı değil
//
// Ada bakmak kuralı yalnızca BUGÜN için uygulardı: yapıcısını Kur ya da New
// diye adlandıran üçüncü bir akış paketi, denetimin gözünde hiç var olmazdı ve
// bağlanmadığı gün test yine yeşil kalırdı — yani tam olarak yakalaması
// gereken hatayı kaçırırdı. Bir paketi "container'dan kurulur" yapan şey
// yapıcısının ADI değil, ŞEKLİDİR: dışa açık bir işlevin *container.Container
// alması, o paketin bağımlılıklarını kayıttan ADLA çözdüğünün ve bunu ancak
// Register döngüsü bittikten sonra yapabileceğinin işaretidir.
//
// Konvansiyonel ad yine de bir işe yarar ve TERS yönde kullanılır: bileşim
// kökü FromContainer çağırdığı hâlde denetim o pakette container alan bir
// yapıcı GÖRMÜYORSA, şekil okuması gerçekle ayrışmış demektir (örneğin imza
// container yerine bir arayüz almaya başlamıştır) ve o andan sonra denetim
// paketi sessizce kapsam dışı bırakırdı.
//
// # Neden ÖLÜ KOD da kurulum sayılmıyor
//
// Bulunan arızanın şekli tam olarak buydu: FromContainer çağrılıyordu — ama
// yalnızca internal/e2e içinden. Üretim ikilisinde kurulumun VAR OLMASI
// yetmez, main()'den ERİŞİLİYOR olması gerekir; erişilmeyen bir kurulum
// işlevi, hiç yazılmamış bir kurulum işleviyle aynı şeydir ve derleyici de,
// modülün kendi testleri de bundan haberdar olmaz. Bu yüzden denetim bileşim
// kökünün çağrı grafiğini main()'den gezer ve ulaşamadığı bir kurulumu
// kurulum saymaz.
//
// Graf ADLARDAN kurulur ve bilinçli olarak GENİŞ tutulur: bir ad başka bir adın
// gövdesinde geçiyorsa kenar vardır (çağrı olmasa bile, örneğin değer olarak
// geçirilmişse) ve düğümler yalnızca işlevler değil, paket düzeyindeki var/const
// bildirimleridir de — kurulumu bir işlev değişkenine almak onu grafın dışına
// çıkarmaz (bkz. [bilesimKokuDugumleri]). Aşırı geniş bir graf en fazla ölü bir
// kurulumu canlı sanır; dar bir graf ise canlı kurulumu ölü ilan edip testi
// yanlış yere düşürürdü ve insanlar denetime güvenmeyi bırakırdı.
//
// # Bu değişmez neyi GARANTİ ETMEZ
//
// Yalnızca ŞUNU garanti eder: bileşim kökünde, main()'den erişilebilen bir
// yerde, her akış paketinin yapıcısı çağrılıyor. Statik analizin
// cevaplayabileceği soru budur ve daha fazlasını iddia etmek yanlış olurdu.
//
// Cevaplayamadığı soru, çağrının KOŞUP koşmadığıdır. Kurulum bir koşulun
// arkasına alındığında —
//
//	var akislariAc = false
//	if akislariAc { akislariKaydet(c) }
//
// — çağrı grafta durmaya devam eder ve bu test GEÇER, oysa çalışan ikilide
// sepete satır eklenemez ve sepet siparişe çevrilemez (iki uç da 500 döner:
// cart modülü akışı çözemez ve kapalı arızalanır). Bayrağı
// okuyup değerlendirmek de kurtarmazdı: değer bir ortam değişkeninden, bir
// yapılandırmadan ya da bir başka çağrının dönüşünden gelebilir ve o noktada
// denetim, uygulamayı çalıştırmanın kötü bir taklidine dönüşür.
//
// # Eksik yarı: ÇALIŞMA ZAMANI KANITI
//
// O soruyu yalnızca yolu GERÇEKTEN KULLANAN bir koşum yanıtlar ve yanıtı
// internal/smoke'tadır: TestVitrinSepettenSipariseGercekSurecte gerçek ikiliyi
// açar, katalogdan fiyatlanan bir satırla sepeti doldurur ve sepeti siparişe
// çevirir. Bayrak, koşul ya da değişken — yolu kapatan HER mutasyon orada
// düşer, çünkü o test yolu kullanır.
//
// İki katman birbirinin yerine GEÇMEZ, birbirini tamamlar:
//
//   - Statik değişmez, kurulum satırı SİLİNDİĞİNDE düşer ve bunu docker'sız,
//     saniyeler içinde, hangi paketin kurulmadığını adıyla söyleyerek yapar.
//     Smoke koşusu aynı arızayı yalnızca "satır eklenemedi, 500" diye
//     bildirir; teşhis için kaynağa inmek gerekir.
//   - Smoke, kurulum KOŞMADIĞINDA düşer. Statik değişmez o durumu göremez ve
//     görebileceğini iddia etmez.
//
// Biri kaldırılırsa ötekinin yettiği SANILIR; ikisinin de neyi kapattığı bu
// yüzden yazılıdır.
//
// # Neden liste tutmuyor, neden ayrıştırıyor
//
// Gerekçeler [TestHerModulBilesimKokundeKayitli] godoc'undakiyle birebir
// aynıdır ve tekrarlanmıyor: elle yazılmış bir akış listesi üçüncü akışı
// kaçırırdı, bileşim kökü de main paketi olduğu için import edilemez.
//
// # Neden e2e ikizi YOK
//
// Modül tarafında ikinci bir yarı vardır ([TestKayitliHerModulE2EZemindeKurulu])
// çünkü orada KAYIT ile ÇALIŞMA ayrı şeylerdir. Akışlarda bu ayrım yoktur:
// akış zaten yalnızca kurulduğu yerde vardır ve zemin onu kurmazsa mağaza
// uçları KAPALI arızalanır, yani zeminde akış kurmayı unutan bir kişi yeşil
// bir koşu göremez — vitrin senaryoları o anda 500 alır. Kuralı ikinci kez
// yazmak, kendini zaten zorlayan bir şartı tekrar etmek olurdu.
func TestHerAkisBilesimKokundeKurulu(t *testing.T) {
	t.Parallel()

	akislar := containerdanKurulanAkisPaketleri(t)
	require.NotEmpty(t, akislar,
		"%s altında container'dan kurulan hiçbir paket bulunamadı; denetim KÖR kalmış "+
			"olmalı (yapıcılar artık *container.Container almıyor mu?)", akisDizini)

	konvansiyonYasiyor := false
	for _, yapicilar := range akislar {
		if slices.Contains(yapicilar, kurulumIsaretiAdi) {
			konvansiyonYasiyor = true
			break
		}
	}
	require.True(t, konvansiyonYasiyor,
		"hiçbir akış paketinde %q adında bir yapıcı YOK; kurulumIsaretiAdi bayatlamış "+
			"olmalı.\nSabit, ters yön denetiminin tek dayanağıdır: bayatladığında bileşim "+
			"kökünün kurduğu ama denetimin göremediği bir paket sessizce kapsam dışı kalır.",
		kurulumIsaretiAdi)

	kurulan := bilesimKokundeKurulanAkislar(t, akislar)

	canli := map[string]token.Position{}
	for yol, kurulum := range kurulan {
		if !kurulum.sayilmiyor {
			canli[yol] = kurulum.konum
		}
	}

	for _, yol := range slices.Sorted(maps.Keys(akislar)) {
		if _, kuruluMu := canli[yol]; kuruluMu {
			continue
		}
		// Bulunmuş ama sayılmamış bir kurulumun hatası, neden sayılmadığını
		// bilen yerde çoktan verildi; burada ikinci kez ve daha kaba bir
		// cümleyle söylemek, doğru teşhisi gürültüye gömerdi.
		if _, bulunduMu := kurulan[yol]; bulunduMu {
			continue
		}
		if gerekce, muaf := kurulmayanAkislar[yol]; muaf {
			t.Logf("%s bileşim kökünde bilinçli olarak KURULMUYOR: %s", yol, gerekce)
			continue
		}
		t.Errorf("%s paketi container'dan kurulmak üzere yazılmış (%s) ama %s/'da "+
			"KURULMUYOR.\n"+
			"Kurulmayan bir akış hiçbir kurulumda yoktur: onu container'dan ADLA çözen "+
			"modül uçları kapalı arızalanır, modüller arası zincirin tamamı (fiyat, "+
			"indirim, vergi, ödeme, kargo, bildirim) erişilemez olur ve akışın kendi "+
			"testleri akışı kendisi kurduğu için bu hiçbir yerde görünmez.\n"+
			"Ya %s/'da yapıcıyı çağırıp sonucu container'a bırakın, ya da paketi "+
			"gerekçesiyle birlikte kurulmayanAkislar haritasına yazın.",
			yol, strings.Join(akislar[yol], ", "), bilesimKoku, bilesimKoku)
	}

	// Ters yön denetimin KENDİ kör noktasını kapatır: bileşim kökü bir akış
	// paketinin yapıcısını çağırdığı hâlde denetim o pakette container alan bir
	// yapıcı görmüyorsa, şekil okuması gerçekle ayrışmıştır. O andan sonra bu
	// test "her akış kurulu" değil "gördüğüm akışlar kurulu" derdi.
	for _, yol := range slices.Sorted(maps.Keys(kurulan)) {
		if _, gorulduMu := akislar[yol]; !gorulduMu {
			t.Errorf("%s paketinin %q yapıcısı %s/'da çağrılıyor (%s) ama denetim o "+
				"pakette container'dan kurulan bir yapıcı GÖRMÜYOR.\n"+
				"Çağrının kendisi paketin container'dan kurulduğunu kanıtlar; denetimin "+
				"görmemesi, şekil okumasının (dışa açık + *container.Container parametresi) "+
				"gerçekle ayrıştığı anlamına gelir ve bu testi bundan sonra kör bırakır.",
				yol, kurulan[yol].yapici, bilesimKoku, kurulan[yol].konum)
		}
	}

	bayatMuafiyetleriDenetle(t, kurulmayanAkislar, akislar,
		"container'dan kurulan bir akış paketi", canli, bilesimKoku)
}

// bayatMuafiyetleriDenetle muafiyet haritasının çürümesini yakalar.
//
// Bir muafiyet iki şekilde bayatlar: muaf tutulan paket ARTIK YOKTUR (silinmiş
// ya da adı değişmiş bir modül) ya da paket ARTIK KAYITLIDIR. İkisi de sessiz
// kalırsa harita, kimsenin okumadığı ve kimsenin doğrulamadığı bir yorum
// yığınına döner — muafiyeti kod içinde tutmanın bütün gerekçesi de o anda
// düşer.
func bayatMuafiyetleriDenetle[T any](
	t *testing.T,
	muafiyetler map[string]string,
	aday map[string]T,
	adayAciklamasi string,
	kayitli map[string]token.Position,
	kok string,
) {
	t.Helper()

	for _, yol := range slices.Sorted(maps.Keys(muafiyetler)) {
		if _, adayMi := aday[yol]; !adayMi {
			t.Errorf("muafiyet BAYAT: %q artık %s değil.\n"+
				"Paket silindiyse ya da adı değiştiyse muafiyet satırı da gitmelidir; "+
				"kalırsa bir gün aynı adla yazılan yeni bir modülü sessizce muaf tutar.",
				yol, adayAciklamasi)
			continue
		}
		if konum, kayitliMi := kayitli[yol]; kayitliMi {
			t.Errorf("muafiyet BAYAT: %q %s/'da artık kayıtlı (%s) ama hâlâ muaf tutuluyor.\n"+
				"Muafiyet borçtur; borç ödendiğinde satır silinmelidir.", yol, kok, konum)
		}
	}
}

// modulUygulayanPaketler internal/modules altında [module.Module] sözleşmesini
// karşılayan paketleri döner: anahtar paketin import yolu, değer sözleşmeyi
// karşılayan tiplerin adlarıdır.
//
// Modül olmayan yardımcı paketler (api, service, repository, models…) elenir
// çünkü sözleşme METOT KÜMESİNDEN okunur, dizin adından değil: bir modülün alt
// paketi dördünü birden taşımaz.
func modulUygulayanPaketler(t *testing.T) map[string][]string {
	t.Helper()

	bulunan := map[string][]string{}
	for _, dizin := range slices.Sorted(maps.Keys(uretimPaketleri(t, filepath.Join(repoRoot, modulesDir)))) {
		fset := token.NewFileSet()
		dosyalar := ayristir(t, fset, dizin, false)
		aliciMetotlari := map[string]map[string]*ast.FuncDecl{}

		for _, d := range dosyalar {
			for _, decl := range d.agac.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				tip := aliciTipAdi(fn.Recv.List[0].Type)
				if tip == "" {
					continue
				}
				if aliciMetotlari[tip] == nil {
					aliciMetotlari[tip] = map[string]*ast.FuncDecl{}
				}
				aliciMetotlari[tip][fn.Name.Name] = fn
			}
		}

		for _, tip := range slices.Sorted(maps.Keys(aliciMetotlari)) {
			if sozlesmeyiKarsiliyor(t, fset, dizin, tip, aliciMetotlari[tip]) {
				yol := paketImportYolu(t, dizin)
				bulunan[yol] = append(bulunan[yol], tip)
			}
		}
	}

	return bulunan
}

// sozlesmeyiKarsiliyor bir tipin metot kümesinin [module.Module]'ü karşılayıp
// karşılamadığını söyler.
//
// Dört metot ADININ tamamı varken imzalardan biri tutmuyorsa sonuç "hayır"
// değil, HATADIR: bu ya sözleşmenin değişip denetimin geride kalması ya da
// paketin farklı bir takma adla import edilmesi demektir. İkisinde de doğru
// davranış sessizce elemek değil, sesini çıkarmaktır — sessiz eleme, modülü
// denetimin kapsamından çıkarır ve testi tam da işe yarayacağı yerde kör
// bırakır.
func sozlesmeyiKarsiliyor(
	t *testing.T,
	fset *token.FileSet,
	dizin, tip string,
	metotlar map[string]*ast.FuncDecl,
) bool {
	t.Helper()

	tam := true
	for ad := range modulSozlesmesi {
		if _, varMi := metotlar[ad]; !varMi {
			tam = false
			break
		}
	}
	if !tam {
		return false
	}

	uyumlu := true
	for _, ad := range slices.Sorted(maps.Keys(modulSozlesmesi)) {
		beklenen := modulSozlesmesi[ad]
		fn := metotlar[ad]
		parametreler := alanTipleri(fn.Type.Params)
		sonuclar := alanTipleri(fn.Type.Results)
		if slices.Equal(parametreler, beklenen.parametreler) && slices.Equal(sonuclar, beklenen.sonuclar) {
			continue
		}
		uyumlu = false
		t.Errorf("%s: %s.%s imzası [module.Module] sözleşmesine uymuyor.\n"+
			"beklenen: (%s) (%s) — bulunan: (%s) (%s)\n"+
			"Tip dört metodun DÖRDÜNÜ de taşıyor, yani büyük olasılıkla bir modül. "+
			"Sözleşme değiştiyse modulSozlesmesi haritası da güncellenmelidir; "+
			"aksi hâlde bu paket kayıt denetiminden sessizce düşer.",
			fset.Position(fn.Pos()), tip, ad,
			strings.Join(beklenen.parametreler, ", "), strings.Join(beklenen.sonuclar, ", "),
			strings.Join(parametreler, ", "), strings.Join(sonuclar, ", "))
	}

	if !uyumlu {
		t.Logf("%s: %s tipi sözleşmeyi karşılamış SAYILIYOR; denetim kör kalmasın diye "+
			"kayıt şartı yine uygulanır", dizin, tip)
	}

	return true
}

// kayitliModulPaketleri verilen paketteki [module.Registry] kaydına eklenen
// modüllerin import yollarını döner; değer, Add çağrısının konumudur.
//
// testDosyalariDahil, e2e zemini içindir: orada kurulum TestMain akışında
// yapılır ve üretim dosyası yoktur.
func kayitliModulPaketleri(t *testing.T, kok string, testDosyalariDahil bool) map[string]token.Position {
	t.Helper()

	dizin := filepath.Join(repoRoot, kok)
	fset := token.NewFileSet()
	dosyalar := ayristir(t, fset, dizin, testDosyalariDahil)
	require.NotEmpty(t, dosyalar, "%s içinde ayrıştırılacak Go dosyası yok", kok)

	// Önce kaydın DEĞİŞKEN adları toplanır: "Add" adında bir metot her tipte
	// olabilir (atomik sayaç, eklenti kaydı, slice sarmalayıcı) ve alıcının
	// gerçekten modül kaydı olduğunu ancak bu ad kümesi söyler.
	degiskenler := kayitDegiskenAdlari(dosyalar)
	require.NotEmpty(t, degiskenler,
		"%s içinde bir [module.Registry] değişkeni bulunamadı.\n"+
			"Kayıt başka bir biçimde kuruluyorsa (yardımcı işlev, tip gömme) bu denetim "+
			"HİÇBİR ŞEY doğrulamıyor demektir; kayitDegiskenAdlari güncellenmelidir.", kok)

	kayitli := map[string]token.Position{}
	for _, d := range dosyalar {
		ast.Inspect(d.agac, func(n ast.Node) bool {
			cagri, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sec, ok := cagri.Fun.(*ast.SelectorExpr)
			if !ok || sec.Sel.Name != "Add" {
				return true
			}
			alici, ok := sec.X.(*ast.Ident)
			if !ok || !degiskenler[alici.Name] {
				return true
			}

			konum := fset.Position(cagri.Lparen)
			paketAdi, cozuldu := argumaninPaketi(cagri.Args)
			if !cozuldu {
				t.Errorf("%s: %s.Add(...) argümanı tanınmayan biçimde.\n"+
					"Denetim yalnızca \"paket.Yapıcı(...)\" biçimini okuyabilir; başka bir "+
					"biçim, kaydın hangi modüle gittiğini denetimden GİZLER. Kayıt bu biçime "+
					"getirilmeli ya da argumaninPaketi genişletilmelidir.", konum, alici.Name)
				return true
			}
			yol, bilinen := d.importlar[paketAdi]
			if !bilinen {
				t.Errorf("%s: %q paketi %s dosyasının import listesinde çözülemedi",
					konum, paketAdi, filepath.Base(d.yol))
				return true
			}
			kayitli[yol] = konum

			return true
		})
	}

	return kayitli
}

// kayitDegiskenAdlari paketteki [module.Registry] değerlerini tutan
// tanımlayıcıların adlarını döner.
//
// İki kaynak taranır: module.NewRegistry çağrısının atandığı değişkenler ve
// tipi *module.Registry olan bildirimler (işlev parametreleri dâhil — e2e
// zemini kaydı yardımcı bir işleve PARAMETRE olarak geçirir).
func kayitDegiskenAdlari(dosyalar []ayristirilmisDosya) map[string]bool {
	adlar := map[string]bool{}

	for _, d := range dosyalar {
		ast.Inspect(d.agac, func(n ast.Node) bool {
			switch dugum := n.(type) {
			case *ast.AssignStmt:
				for i, sag := range dugum.Rhs {
					cagri, ok := sag.(*ast.CallExpr)
					if !ok || !cekirdeginModulTipi(cagri.Fun, "NewRegistry", d.importlar) {
						continue
					}
					if i < len(dugum.Lhs) {
						if ident, ok := dugum.Lhs[i].(*ast.Ident); ok {
							adlar[ident.Name] = true
						}
					}
				}
			case *ast.ValueSpec:
				if cekirdeginModulTipi(dugum.Type, "Registry", d.importlar) {
					for _, ident := range dugum.Names {
						adlar[ident.Name] = true
					}
					break
				}
				for i, deger := range dugum.Values {
					cagri, ok := deger.(*ast.CallExpr)
					if !ok || !cekirdeginModulTipi(cagri.Fun, "NewRegistry", d.importlar) {
						continue
					}
					if i < len(dugum.Names) {
						adlar[dugum.Names[i].Name] = true
					}
				}
			case *ast.Field:
				if cekirdeginModulTipi(dugum.Type, "Registry", d.importlar) {
					for _, ident := range dugum.Names {
						adlar[ident.Name] = true
					}
				}
			}

			return true
		})
	}

	return adlar
}

// cekirdeginModulTipi ifadenin çekirdeğin module paketindeki verilen ada
// karşılık gelip gelmediğini söyler; işaretçi yıldızı yok sayılır.
//
// Takma ad haritası üzerinden çözülür, tanımlayıcı adına bakılmaz: paketi
// "coremodule" diye import eden bir dosya da doğru tanınmalıdır.
func cekirdeginModulTipi(ifade ast.Expr, ad string, importlar map[string]string) bool {
	if ifade == nil {
		return false
	}
	if yildiz, ok := ifade.(*ast.StarExpr); ok {
		ifade = yildiz.X
	}
	sec, ok := ifade.(*ast.SelectorExpr)
	if !ok || sec.Sel.Name != ad {
		return false
	}
	paket, ok := sec.X.(*ast.Ident)

	return ok && importlar[paket.Name] == cekirdekModulPaketi
}

// argumaninPaketi Add argümanındaki modül yapıcısının paket adını döner.
//
// Beklenen biçim "paket.Yapıcı(...)"dır; başka biçimler çözülemedi olarak
// döner ve çağıran tarafta HATA olur (bkz. [kayitliModulPaketleri]).
func argumaninPaketi(argumanlar []ast.Expr) (string, bool) {
	if len(argumanlar) != 1 {
		return "", false
	}
	cagri, ok := argumanlar[0].(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sec, ok := cagri.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	paket, ok := sec.X.(*ast.Ident)
	if !ok {
		return "", false
	}

	return paket.Name, true
}

// modulPaketlerineSuz kayıt kümesini internal/modules altındaki paketlere
// indirger.
//
// Eklentilerin getirdiği modüller (bkz. plugins/searchpg) kayda çekirdeğin
// eklenti host'u üzerinden girer, bileşim kökünde Add satırı yoktur ve bu
// denetimin konusu değildir: değişmez "internal/modules altında yazılan her
// modül" der.
func modulPaketlerineSuz(kayitli map[string]token.Position) map[string]token.Position {
	onek := modulePath + "/" + modulesDir + "/"
	suzulmus := make(map[string]token.Position, len(kayitli))
	for yol, konum := range kayitli {
		if strings.HasPrefix(yol, onek) {
			suzulmus[yol] = konum
		}
	}

	return suzulmus
}

// containerdanKurulanAkisPaketleri internal/workflows altında container'dan
// kurulmak üzere tasarlanmış paketleri döner: anahtar paketin import yolu,
// değer yapıcıların adlarıdır.
//
// Ölçüt "dışa açık ve *container.Container alan bir işlev"dir. İkisi de
// gereklidir: dışa açık olmayan bir yapıcıyı bileşim kökü zaten çağıramaz
// (yani kural onu kapsayamaz), container almayan bir işlev de bağımlılıklarını
// kayıttan çözmüyor demektir — o paketi kuran taraf onu doğrudan elle kurar ve
// kayıt sırası sorunu doğmaz.
//
// Akış olmayan yardımcı paketler (para, anlık görüntü, katalog…) böylece
// kendiliğinden elenir: ölçüt dizin adı değil imzadır.
func containerdanKurulanAkisPaketleri(t *testing.T) map[string][]string {
	t.Helper()

	kok := filepath.Join(repoRoot, akisDizini)
	require.DirExists(t, kok,
		"%s ağacı YOK; akış kablolaması denetimi dayanaksız kalır. Ağaç taşındıysa "+
			"akisDizini de taşınmalıdır, yoksa denetim boşlukta yeşil kalır", akisDizini)

	bulunan := map[string][]string{}
	for _, dizin := range slices.Sorted(maps.Keys(uretimPaketleri(t, kok))) {
		fset := token.NewFileSet()

		var yapicilar []string
		for _, d := range ayristir(t, fset, dizin, false) {
			for _, decl := range d.agac.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || !fn.Name.IsExported() {
					continue
				}
				if containerAlanIs(fn, d.importlar) {
					yapicilar = append(yapicilar, fn.Name.Name)
				}
			}
		}

		if len(yapicilar) > 0 {
			slices.Sort(yapicilar)
			bulunan[paketImportYolu(t, dizin)] = yapicilar
		}
	}

	return bulunan
}

// containerAlanIs işlevin parametrelerinden en az birinin çekirdeğin
// container'ı olup olmadığını söyler.
func containerAlanIs(fn *ast.FuncDecl, importlar map[string]string) bool {
	if fn.Type.Params == nil {
		return false
	}
	for _, alan := range fn.Type.Params.List {
		if akisContainerTipi(alan.Type, importlar) {
			return true
		}
	}

	return false
}

// akisContainerTipi ifadenin çekirdeğin container tipi olup olmadığını söyler;
// işaretçi yıldızı yok sayılır.
//
// Takma ad haritası üzerinden çözülür, tanımlayıcı adına bakılmaz: paketi
// "corecontainer" diye import eden bir dosya da doğru tanınmalıdır.
func akisContainerTipi(ifade ast.Expr, importlar map[string]string) bool {
	if yildiz, ok := ifade.(*ast.StarExpr); ok {
		ifade = yildiz.X
	}
	sec, ok := ifade.(*ast.SelectorExpr)
	if !ok || sec.Sel.Name != "Container" {
		return false
	}
	paket, ok := sec.X.(*ast.Ident)

	return ok && importlar[paket.Name] == cekirdekContainerPaketi
}

// bilesimKokundeKurulanAkislar verilen paketteki akış kurulumlarını döner;
// anahtar kurulan akış paketinin import yolu, değer kurulumun yeridir.
//
// Aranan şey "yapıcı ADININ paket niteleyicisiyle ÇAĞRILMASI"dır. Bu biçimin
// dışına çıkan her başvuru SESSİZ bırakılmaz, çünkü kaçamak yolların tamamı
// aynı sonucu verir: kurulumun hangi pakete gittiği denetimden gizlenir ve
// gizlenen bir bağ, olmayan bir bağla aynı şeydir.
//
//   - Yapıcı bir değere alınıp öyle çağrılırsa (kur := paket.FromContainer)
//     ya da bir dilime konup döngüyle çağrılırsa, ad ÇAĞRI konumunda değil
//     DEĞER konumunda görünür; ikisi de burada yakalanır.
//   - Niteleyici bir paket adı değilse (bir ifadenin sonucu, bir alan) hangi
//     pakete gidildiği okunamaz ve durum hata olarak raporlanır.
func bilesimKokundeKurulanAkislar(t *testing.T, yapicilar map[string][]string) map[string]akisKurulumu {
	t.Helper()

	fset := token.NewFileSet()
	dosyalar := ayristir(t, fset, filepath.Join(repoRoot, bilesimKoku), false)
	require.NotEmpty(t, dosyalar, "%s içinde ayrıştırılacak Go dosyası yok", bilesimKoku)

	// Aranan ad kümesi: paketlerin GERÇEK yapıcıları ve konvansiyonel ad.
	// İkincisi ters yön denetimi içindir; bkz. [kurulumIsaretiAdi].
	adlar := map[string]bool{kurulumIsaretiAdi: true}
	for _, paketYapicilari := range yapicilar {
		for _, ad := range paketYapicilari {
			adlar[ad] = true
		}
	}

	onek := modulePath + "/" + akisDizini + "/"
	erisilebilir := maindenErisilebilirIsler(dosyalar)
	kurulan := map[string]akisKurulumu{}

	for _, d := range dosyalar {
		cagrilanlar := cagriIfadeleri(d.agac)
		for _, decl := range d.agac.Decls {
			// Paket düzeyindeki bir bildirimin (var başlatıcısı) kapsayanı
			// yoktur ve o kod her koşuda çalışır; erişilebilirlik sorusu
			// yalnızca işlevler için anlamlıdır.
			kapsayan, kapsayanCanli := "", true
			if fn, ok := decl.(*ast.FuncDecl); ok {
				kapsayan, kapsayanCanli = fn.Name.Name, erisilebilir[fn.Name.Name]
			}

			ast.Inspect(decl, func(n ast.Node) bool {
				sec, ok := n.(*ast.SelectorExpr)
				if !ok || !adlar[sec.Sel.Name] {
					return true
				}
				yol, cozuldu := akisPaketiniCoz(t, fset.Position(sec.Sel.Pos()), sec, d, onek)
				if !cozuldu {
					return true
				}
				// Ad kümesi PAKETLERİN BİRLEŞİMİDİR; burada o paketin KENDİ
				// yapıcısı olduğu doğrulanır. Doğrulanmasaydı, bir akış
				// paketinin yapıcı adını paylaşan bambaşka bir işlev (örneğin
				// bir yardımcının New'i) o paketi kurulmuş gösterirdi.
				if !slices.Contains(yapicilar[yol], sec.Sel.Name) && sec.Sel.Name != kurulumIsaretiAdi {
					return true
				}

				kurulum := akisKurulumu{
					konum:    fset.Position(sec.Sel.Pos()),
					kapsayan: kapsayan,
					yapici:   sec.Sel.Name,
				}
				switch {
				case !cagrilanlar[sec]:
					t.Errorf("%s: %s.%s bir DEĞER olarak kullanılıyor, çağrılmıyor.\n"+
						"Yapıcıyı bir değişkene, bir dilime ya da bir tabloya alıp öyle "+
						"çağırmak, kurulumun hangi akışa gittiğini bu denetimden GİZLER: "+
						"çağrı yerinde artık paket adı yoktur. Kurulum \"paket.Yapıcı(...)\" "+
						"biçiminde yazılmalı ya da bilesimKokundeKurulanAkislar bu biçimi de "+
						"okuyacak şekilde genişletilmelidir.",
						kurulum.konum, sec.X, sec.Sel.Name)
					kurulum.sayilmiyor = true
				case !kapsayanCanli:
					t.Errorf("%s: %s.%s çağrısı ÖLÜ KODDA — %s() işlevine main()'den "+
						"erişilemiyor.\n"+
						"Derlenen ama koşmayan bir kurulum, hiç yazılmamış bir kurulumla aynı "+
						"şeydir ve tam olarak bu turda bulunan arızadır: akışların yapıcısı "+
						"çağrılıyordu, ama yalnızca testlerin içinden.\n"+
						"Ya çağrı zincirini main()'e bağlayın, ya da kurulum gerçekten "+
						"gereksizse ölü işlevi silin.",
						kurulum.konum, sec.X, sec.Sel.Name, kapsayan)
					kurulum.sayilmiyor = true
				}

				// Aynı paket için geçerli bir kurulum bulunmuşsa kusurlu olan
				// onun yerini almaz: kusur zaten yukarıda raporlandı, kaydın
				// işi ise "kurulu mu" sorusuna cevap vermektir.
				if mevcut, varMi := kurulan[yol]; varMi && !mevcut.sayilmiyor {
					return true
				}
				kurulan[yol] = kurulum

				return true
			})
		}
	}

	return kurulan
}

// akisPaketiniCoz yapıcı başvurusunun hangi akış paketine gittiğini döner.
//
// Akış ağacının dışına giden başvurular (aynı adı taşıyan başka bir paketin
// işlevi) sessizce elenir; okunamayan biçimler ise hata verir, çünkü orada
// eleme ile GÖRMEME arasındaki farkı denetim bilemez.
func akisPaketiniCoz(
	t *testing.T,
	konum token.Position,
	sec *ast.SelectorExpr,
	d ayristirilmisDosya,
	onek string,
) (string, bool) {
	t.Helper()

	paket, ok := sec.X.(*ast.Ident)
	if !ok {
		t.Errorf("%s: %s çağrısının niteleyicisi bir paket adı DEĞİL.\n"+
			"Denetim yalnızca \"paket.Yapıcı(...)\" biçimini okuyabilir; başka bir biçim, "+
			"kurulumun hangi akışa gittiğini GİZLER.", konum, sec.Sel.Name)

		return "", false
	}

	yol, bilinen := d.importlar[paket.Name]
	if !bilinen {
		t.Errorf("%s: %q, %s dosyasının import listesinde çözülemedi.\n"+
			"Yapıcı adı bir paketin değil bir DEĞERİN üzerinden kullanılıyorsa kurulumun "+
			"hedefi okunamaz; kurulum doğrudan paket üzerinden çağrılmalıdır.",
			konum, paket.Name, filepath.Base(d.yol))

		return "", false
	}

	return yol, strings.HasPrefix(yol, onek)
}

// cagriIfadeleri dosyadaki her çağrının ÇAĞRILAN ifadesini kümeler.
//
// Genel (generic) çağrıların "paket.Yapıcı[T](...)" biçiminde araya bir indeks
// ifadesi girer; taban ifade de kümeye alınır, aksi hâlde böyle bir çağrı
// "değer olarak kullanılıyor" sanılırdı.
func cagriIfadeleri(agac *ast.File) map[ast.Expr]bool {
	cagrilan := map[ast.Expr]bool{}
	ast.Inspect(agac, func(n ast.Node) bool {
		cagri, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		cagrilan[cagri.Fun] = true
		switch tipli := cagri.Fun.(type) {
		case *ast.IndexExpr:
			cagrilan[tipli.X] = true
		case *ast.IndexListExpr:
			cagrilan[tipli.X] = true
		}

		return true
	})

	return cagrilan
}

// maindenErisilebilirIsler bileşim kökündeki paket düzeyi adlardan main()'den
// erişilebilenlerin kümesini döner.
//
// Kenarlar ADLARDAN kurulur ve bilinçli olarak geniştir: bir ad başka bir adın
// gövdesinde herhangi bir biçimde geçiyorsa (çağrı, değer olarak geçirme,
// defer) kenar sayılır. Gerekçe [TestHerAkisBilesimKokundeKurulu]
// godoc'undadır.
//
// init de köktür: paket başlatma her koşuda çalışır ve oradan çağrılan bir
// kurulum ölü değildir.
func maindenErisilebilirIsler(dosyalar []ayristirilmisDosya) map[string]bool {
	govdeler := bilesimKokuDugumleri(dosyalar)

	erisilebilir := map[string]bool{}
	var gez func(ad string)
	gez = func(ad string) {
		if erisilebilir[ad] {
			return
		}
		erisilebilir[ad] = true
		for _, govde := range govdeler[ad] {
			ast.Inspect(govde, func(n ast.Node) bool {
				ident, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if _, tanidik := govdeler[ident.Name]; tanidik {
					gez(ident.Name)
				}

				return true
			})
		}
	}

	gez("main")
	if _, varMi := govdeler["init"]; varMi {
		gez("init")
	}

	return erisilebilir
}

// bilesimKokuDugumleri paket düzeyindeki her adı, o adın "gövdesi" sayılan AST
// düğümlerine eşler: işlevler için gövde bloğu, var/const bildirimleri için
// başlatıcı ifadeleri.
//
// # Neden bildirimler de düğüm
//
// Graf yalnızca işlev gövdelerini gezdiği sürece, kurulumu bir işlev
// DEĞİŞKENİNE almak onu denetimin gözünde ölü gösterirdi:
//
//	var akisKurucu = akislariKaydet   // paket düzeyi, hiçbir gövdede değil
//	func main() { akisKurucu(c) }     // "akislariKaydet" adı hiç geçmiyor
//
// İkili tamamen çalışırken denetim "ÖLÜ KODDA" derdi — canlıyı ölü ilan eden
// bir denetim ise en kötü denetimdir: susturulmayı hak ettiğine insanları HAKLI
// olarak ikna eder. Bildirimi düğüm yapmak zinciri tamamlar (main -> akisKurucu
// -> akislariKaydet) ve grafı, godoc'unun zaten VAAT ETTİĞİ genişliğe getirir.
//
// # Neden bildirimler KÖK değil
//
// Paket başlatma her koşuda çalışır, yani bir başlatıcı İFADESİ kayıtsız
// şartsız koşar. Ama "var f = Kur" ifadesinin koştuğu, Kur'un koştuğu anlamına
// GELMEZ: değişkeni kimse çağırmıyorsa Kur hâlâ ölüdür. Bildirimleri kök
// saymak bu ayrımı yutar ve "if false ile kapatılmış ama bir değişkende duran"
// bir kurulumu canlı gösterirdi. Bu yüzden bildirim, ancak REFERANS EDİLDİĞİNDE
// gezilen bir düğümdür.
//
// Ayrım [bilesimKokundeKurulanAkislar] ile de tutarlıdır: orada paket düzeyi
// bir bildirimin İÇİNDEKİ çağrı koşulsuz canlı sayılır, çünkü orada sorulan
// soru başkadır — ifadenin kendisi koşuyor mu? Burada sorulan soru, ADIN
// gösterdiği işlevin koşup koşmadığıdır.
func bilesimKokuDugumleri(dosyalar []ayristirilmisDosya) map[string][]ast.Node {
	govdeler := map[string][]ast.Node{}
	for _, d := range dosyalar {
		for _, decl := range d.agac.Decls {
			switch tipli := decl.(type) {
			case *ast.FuncDecl:
				if tipli.Body != nil {
					govdeler[tipli.Name.Name] = append(govdeler[tipli.Name.Name], tipli.Body)
				}
			case *ast.GenDecl:
				// import ve type bildirimleri çalışan kod taşımaz; yalnızca
				// var/const başlatıcıları bir ada gövde verir.
				if tipli.Tok != token.VAR && tipli.Tok != token.CONST {
					continue
				}
				for _, spec := range tipli.Specs {
					deger, degerMi := spec.(*ast.ValueSpec)
					if !degerMi {
						continue
					}
					// Adlar ile başlatıcılar TAM eşleşmeyebilir ("a, b := f()"
					// biçiminin bildirim karşılığında tek ifade iki ada
					// düşer); her ada hepsi bağlanır. Fazlalık grafı yalnızca
					// GENİŞLETİR ve bu, kabul edilen yöndür.
					for _, ad := range deger.Names {
						for _, baslatici := range deger.Values {
							govdeler[ad.Name] = append(govdeler[ad.Name], baslatici)
						}
					}
				}
			}
		}
	}

	return govdeler
}

// uretimPaketleri kök altındaki, en az bir üretim (_test.go olmayan) dosyası
// bulunan dizinleri döner.
func uretimPaketleri(t *testing.T, kok string) map[string]struct{} {
	t.Helper()

	dizinler := map[string]struct{}{}
	for _, dosya := range goFiles(t, kok) {
		if strings.HasSuffix(dosya, "_test.go") {
			continue
		}
		dizinler[filepath.Dir(dosya)] = struct{}{}
	}

	return dizinler
}

// ayristir bir dizindeki Go dosyalarını (alt dizinlere İNMEDEN) ayrıştırır ve
// import takma adlarını çözer.
func ayristir(t *testing.T, fset *token.FileSet, dizin string, testDosyalariDahil bool) []ayristirilmisDosya {
	t.Helper()

	girdiler, err := os.ReadDir(dizin)
	if err != nil {
		t.Fatalf("%s okunamadı: %v", dizin, err)
	}

	var dosyalar []ayristirilmisDosya
	for _, girdi := range girdiler {
		ad := girdi.Name()
		if girdi.IsDir() || !strings.HasSuffix(ad, ".go") {
			continue
		}
		if !testDosyalariDahil && strings.HasSuffix(ad, "_test.go") {
			continue
		}

		yol := filepath.Join(dizin, ad)
		agac, parseErr := parser.ParseFile(fset, yol, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			t.Fatalf("%s ayrıştırılamadı: %v", yol, parseErr)
		}
		dosyalar = append(dosyalar, ayristirilmisDosya{yol: yol, agac: agac, importlar: importTakmaAdlari(agac)})
	}

	return dosyalar
}

// importTakmaAdlari dosyadaki her import'un yerel adını import yoluna eşler.
//
// Takma ad verilmemişse yol son parçası kullanılır. Bu, paket adının dizin
// adından farklı olduğu durumda yanılır; denetimin baktığı paketlerin hepsinde
// ikisi aynıdır ve yanılma yalnızca bir kaydı GÖRMEMEK olur — o da çağıran
// tarafta sessiz kalmaz, çünkü çözülemeyen paket adı hata verir.
func importTakmaAdlari(agac *ast.File) map[string]string {
	adlar := make(map[string]string, len(agac.Imports))
	for _, imp := range agac.Imports {
		yol := strings.Trim(imp.Path.Value, `"`)
		ad := yol[strings.LastIndex(yol, "/")+1:]
		if imp.Name != nil {
			ad = imp.Name.Name
		}
		adlar[ad] = yol
	}

	return adlar
}

// aliciTipAdi metot alıcısının taban tip adını döner; çözülemezse boş dize.
func aliciTipAdi(ifade ast.Expr) string {
	if yildiz, ok := ifade.(*ast.StarExpr); ok {
		ifade = yildiz.X
	}
	// Genel (generic) alıcılar "T[P]" biçimindedir; taban ad soldadır.
	switch tipli := ifade.(type) {
	case *ast.IndexExpr:
		ifade = tipli.X
	case *ast.IndexListExpr:
		ifade = tipli.X
	}
	if ident, ok := ifade.(*ast.Ident); ok {
		return ident.Name
	}

	return ""
}

// alanTipleri bir parametre ya da sonuç listesinin tiplerini kaynak biçiminde
// döner; "a, b int" gibi paylaşılan bildirimler tipi TEKRARLAYARAK açılır.
func alanTipleri(liste *ast.FieldList) []string {
	if liste == nil {
		return nil
	}

	var tipler []string
	for _, alan := range liste.List {
		for range max(len(alan.Names), 1) {
			tipler = append(tipler, types.ExprString(alan.Type))
		}
	}

	return tipler
}

// paketImportYolu dosya sistemi yolundan Go import yolunu üretir.
func paketImportYolu(t *testing.T, dizin string) string {
	t.Helper()

	rel, err := filepath.Rel(repoRoot, dizin)
	if err != nil {
		t.Fatalf("%s için göreli yol hesaplanamadı: %v", dizin, err)
	}

	return modulePath + "/" + filepath.ToSlash(rel)
}
