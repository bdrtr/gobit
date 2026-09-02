package service_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// Bu dosya depo seçim POLİTİKASINI sınar: eleme, sıralama ve eşitlik bozma.
//
// Testler gerçek bir veritabanı İSTEMEZ çünkü kararın kendisi saf bir
// fonksiyondur; sahte depo yalnızca politika kayıtlarını taşır. Politikanın
// gerçek Postgres üzerinde ve gerçek saga ile koştuğu kanıt e2e tarafındadır.

// politikaYaz test için bir depo politikası kurar.
func politikaYaz(
	t *testing.T,
	kurulum testKurulum,
	locationID string,
	priority int64,
	regionIDs ...string,
) {
	t.Helper()
	_, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: locationID,
		Priority:   priority,
		RegionIDs:  regionIDs,
	})
	if err != nil {
		t.Fatalf("depo politikası yazılamadı (%s): %v", locationID, err)
	}
}

// TestRankLocationsHedefBolgeyeHizmetEtmeyenAdayElenir kapsam elemesini
// kanıtlar.
//
// Elenen aday, eşitlik bozma kuralının (kimliği en küçük) BAŞA koyacağı
// adaydır; aksi hâlde test politikayı değil, kimlik sırasını sınamış olurdu.
// Elenen aday sıradan TAMAMEN düşer, sona atılmaz: geri düşme onu yine
// denerdi.
func TestRankLocationsHedefBolgeyeHizmetEtmeyenAdayElenir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_de")
	politikaYaz(t, kurulum, "sloc_izmir", 0, testRegionID)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir"}, sirali,
		"hedef bölgeye hizmet etmeyen aday elenmeli, kimliği küçük olsa bile")
}

// TestRankLocationsBolgesizDepoTumBolgelereHizmetEder bağı OLMAYAN deponun
// elenmediğini kanıtlar.
//
// Kural satış kanalı kapsamınınkiyle aynıdır ve aynı tuzağı taşır: bir deponun
// son bölge bağını silmek onu kapatmaz, TÜM bölgelere açar. Test tuzağın
// bilinçli olduğunu sabitler — kural tersine çevrilseydi bugün politikası
// olmayan her kurulum sipariş veremez hâle gelirdi.
func TestRankLocationsBolgesizDepoTumBolgelereHizmetEder(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_merkez", 0)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_merkez"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_merkez"}, sirali)
}

// TestRankLocationsOncelikKimlikSirasiniEzer sıralamanın eşitlik bozma
// kuralının ÖNÜNDE geldiğini kanıtlar.
//
// İddia bu özelliğin varlık sebebidir: politika olmasaydı kimliği en küçük
// aday başa geçerdi ve işletmeci tercihini ifade edemezdi. Elenmeyen aday
// sırada KALIR — öncelik bir eleme değil, bir dizilim kuralıdır.
func TestRankLocationsOncelikKimlikSirasiniEzer(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 10, testRegionID)
	politikaYaz(t, kurulum, "sloc_izmir", 1, testRegionID)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir", "sloc_ankara"}, sirali,
		"önceliği küçük olan başa geçmeli, diğeri sırada kalmalı")
}

// TestRankLocationsNegatifOncelikPolitikasizDeponunUstunde negatif önceliğin
// politikası OLMAYAN depoyu geçtiğini kanıtlar.
//
// Negatife izin verilmesinin somut sebebi budur: bir depoyu öne almak için tek
// satır yazmak yeterli olmalı, öne almak İSTENMEYEN depolara satır yazmak
// gerekmemelidir.
func TestRankLocationsNegatifOncelikPolitikasizDeponunUstunde(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_zonguldak", -1, testRegionID)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_zonguldak"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_zonguldak", "sloc_ankara"}, sirali,
		"negatif öncelik, kaydı olmayan deponun (sıfır öncelik) üstünde olmalı")
}

// TestRankLocationsKaydiOlmayanDepoSifirOncelikliyleEsittir "kayıt yok" ile
// "önceliği açıkça sıfır" durumlarının AYNI sırada olduğunu kanıtlar.
//
// İkisi ayrılsaydı, bir depoya öncelik sıfır yazmak onu sessizce ileri ya da
// geri alırdı; oysa yazılan değer varsayılanın ta kendisidir.
func TestRankLocationsKaydiOlmayanDepoSifirOncelikliyleEsittir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_izmir", 0, testRegionID)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara", "sloc_izmir"}, sirali,
		"eşit öncelikte sıra kimliğe göre kurulmalı; kaydı olmayan depo sıfır önceliktedir")
}

// TestRankLocationsTumAdaylarElenirseConflict elemenin sonucu boş kaldığında
// hatanın SINIFINI ve KODUNU sabitler.
//
// Sınıf Conflict olmalıdır ve gerekçesi çağıranın dallanması DEĞİLDİR: sınıf
// hatanın HTTP karşılığını belirler ve motorun varsayılan yeniden deneme
// yüklemi KindConflict'i denemez, KindInternal'ı dener. Elenmiş bir aday kümesi
// tekrar denemekle değişmez; Internal seçilseydi işletmecinin elle düzeltmesi
// gereken bir yapılandırma hatası geçici arıza sanılırdı.
//
// Kod AYRIDIR çünkü işletmecinin yapacağı iş ayrıdır: burada stok vardır,
// yanlış kurulmuş olan bölge kapsamıdır.
func TestRankLocationsTumAdaylarElenirseConflict(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_de")
	politikaYaz(t, kurulum, "sloc_izmir", 0, "reg_fr")

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.Error(t, err)
	assert.Empty(t, sirali)
	assert.True(t, errors.IsConflict(err), "hata errors.Conflict olmalı: %v", err)
	assert.Equal(t, service.CodeNoServiceableLocation, errors.CodeOf(err))
}

// TestRankLocationsBosBolgeInvalid hedef bölge verilmeden sıralama
// yapılamayacağını kanıtlar.
//
// Boş bölgede elemeyi ATLAMAK, o bölgeye hizmet etmeyen bir depoyu sessizce
// seçmek olurdu; hata, sebebinden bir modül uzakta ortaya çıkardı.
func TestRankLocationsBosBolgeInvalid(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	for _, bolge := range []string{"", "   "} {
		sirali, err := kurulum.svc.RankLocations(context.Background(), bolge,
			[]string{"sloc_ankara"})
		require.Error(t, err)
		assert.Empty(t, sirali)
		assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
		assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
	}
}

// TestRankLocationsAdayDilimiDegismez karar yüzeyinin kendisine verilen
// veriyi BOZMADIĞINI kanıtlar.
//
// Politika sıralama yapar ama adayları YERİNDE sıralamaz: çağıranın dilimi
// saga'nın aday defteridir ve bir karar yüzeyi kendisine verilen veriyi
// değiştiremez.
func TestRankLocationsAdayDilimiDegismez(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_izmir", -5, testRegionID)

	adaylar := []string{"sloc_ankara", "sloc_izmir", "sloc_bursa"}
	onceki := slices.Clone(adaylar)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID, adaylar)
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_izmir", "sloc_ankara", "sloc_bursa"}, sirali)
	assert.Equal(t, onceki, adaylar, "aday dilimi değiştirilmemeli")
}

// TestSetShippingLocationBolgeleriToptanYazar bölge listesinin MUTLAK
// olduğunu kanıtlar: eski bağlar kalmaz.
//
// Birleştirme (eskiye ekleme) seçilseydi bir bölgeyi kaldırmanın yolu
// olmazdı ve kapsam yalnızca genişleyebilirdi.
func TestSetShippingLocationBolgeleriToptanYazar(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_de", testRegionID)

	kayit, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{"reg_de"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_de"}, kayit.RegionIDs)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.Error(t, err, "kaldırılan bölge bağı sıralamayı da etkilemeli")
	assert.Empty(t, sirali)
	assert.Equal(t, service.CodeNoServiceableLocation, errors.CodeOf(err))
}

// TestSetShippingLocationBosBolgeListesiTumBolgeleriAcar bölge listesini
// boşaltmanın depoyu KAPATMADIĞINI, tüm bölgelere AÇTIĞINI kanıtlar.
//
// Tuzak yazılıdır ve test onu sabitler: kapsamı daraltmak için son bağı silen
// bir işletmeci, tam tersini elde eder.
func TestSetShippingLocationBosBolgeListesiTumBolgeleriAcar(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_de")

	_, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
	})
	require.NoError(t, err)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara"}, sirali)
}

// TestSetShippingLocationYinelenenBolgeElenir aynı bölgenin iki kez
// verilmesinin hata DEĞİL, tek bağ olduğunu kanıtlar.
func TestSetShippingLocationYinelenenBolgeElenir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	kayit, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{testRegionID, " " + testRegionID + " ", "reg_de"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_de", testRegionID}, kayit.RegionIDs,
		"yinelenen bölge elenmeli; dönen sıra girdininki DEĞİL, kimliğe göredir — "+
			"bağlar bir küme kurar, liste değil")
}

// TestSetShippingLocationBosBolgeKimligiInvalid boş bir bölge kimliğinin
// yazılmadığını kanıtlar.
func TestSetShippingLocationBosBolgeKimligiInvalid(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	_, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_ankara",
		RegionIDs:  []string{testRegionID, "   "},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)

	_, getErr := kurulum.svc.GetShippingLocation(context.Background(), "sloc_ankara")
	require.Error(t, getErr, "doğrulama düşen istek hiçbir satır yazmamalı")
	assert.True(t, errors.IsNotFound(getErr))
}

// TestDeleteShippingLocationVarsayilanaDondurur silmenin depoyu KAPATMADIĞINI,
// varsayılana döndürdüğünü kanıtlar.
//
// Ayrım önemlidir: kargo modülü bir depoyu adaylıktan çıkaramaz, aday listesini
// stok olgusu üretir. Silmek yalnızca "bu depo için özel bir kural yok" demektir.
func TestDeleteShippingLocationVarsayilanaDondurur(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_de")

	_, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.Error(t, err, "kapsam dışı depo silmeden ÖNCE elenmeli")

	require.NoError(t, kurulum.svc.DeleteShippingLocation(context.Background(), "sloc_ankara"))

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara"})
	require.NoError(t, err, "politikası silinen depo varsayılana dönmeli")
	assert.Equal(t, []string{"sloc_ankara"}, sirali)
}

// TestDeleteShippingLocationBilinmeyenNotFound olmayan bir kaydın silinmesinin
// SESSİZCE başarılı olmadığını kanıtlar.
//
// DELETE olmayan satır için de hatasız döner; denetim olmasaydı yanlış bir
// kimlikle yapılan silme başarılı görünürdü ve işletmeci kaldırdığını sandığı
// kuralla çalışmaya devam ederdi.
func TestDeleteShippingLocationBilinmeyenNotFound(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	err := kurulum.svc.DeleteShippingLocation(context.Background(), "sloc_yok")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "hata errors.NotFound olmalı: %v", err)
}

// TestListShippingLocationsOncelikSirasindaDoner listelemenin sırasını
// sabitler: önce öncelik, sonra kimlik — seçimin uyguladığı sıranın aynısı.
func TestListShippingLocationsOncelikSirasindaDoner(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 5, testRegionID)
	politikaYaz(t, kurulum, "sloc_bursa", -2, testRegionID)
	politikaYaz(t, kurulum, "sloc_izmir", 5, testRegionID)

	kayitlar, toplam, err := kurulum.svc.ListShippingLocations(context.Background(), service.Page{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), toplam)

	ids := make([]string, 0, len(kayitlar))
	for _, kayit := range kayitlar {
		ids = append(ids, kayit.LocationID)
	}
	assert.Equal(t, []string{"sloc_bursa", "sloc_ankara", "sloc_izmir"}, ids)
}

// TestRankLocationsElemeHatasiBaglariAdlandirir elemenin sebebinin mesajda
// GÖRÜNDÜĞÜNÜ kanıtlar.
//
// İddia bir konfor değil: elenmenin en sinsi sebebi ölü bir bölge kimliğidir.
// İşletmeci bir bölgeyi silip aynı adla yeniden açarsa kimlik değişir, politika
// satırları eskisini taşımaya devam eder ve mağazadaki HER sipariş elenir.
// Yalnızca "hizmet eden depo yok" diyen bir mesajla operatör kimliklerin
// ayrıştığını göremez; mesaj bağları yazdığı için görebilir.
func TestRankLocationsElemeHatasiBaglariAdlandirir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_ankara", 0, "reg_olu")
	politikaYaz(t, kurulum, "sloc_izmir", 0, "reg_olu")

	_, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_izmir"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reg_olu",
		"mesaj deponun GERÇEKTE bağlı olduğu bölgeyi yazmalı")
	assert.Contains(t, err.Error(), testRegionID,
		"mesaj hangi bölgenin arandığını yazmalı")
}

// TestRankLocationsCagiraninDizesiAynenDoner dönen elemanların çağıranın
// verdiği dizelerle BİREBİR aynı olduğunu kanıtlar.
//
// Eşleştirme baştaki/sondaki boşluklar atılarak yapılır ama dönüş
// normalleştirilmiş kopya OLAMAZ: çağıran sonucu kendi aday defterinde arar ve
// bulamazsa akışı bir iç hata olarak düşürür. Kırpılmış kopya dönseydi
// " sloc_a " yazan bir çağıran, sözleşmeyi çiğnemediği hâlde 500 alırdı.
func TestRankLocationsCagiraninDizesiAynenDoner(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	politikaYaz(t, kurulum, "sloc_izmir", -1, testRegionID)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"  sloc_izmir  ", "sloc_ankara"})
	require.NoError(t, err)
	assert.Equal(t, []string{"  sloc_izmir  ", "sloc_ankara"}, sirali,
		"eşleştirme kırpılmış anahtarla yapılır, dönüş çağıranın dizesidir")
}

// TestRankLocationsYinelenenAdayTekKezSiralanir aynı adayın iki kez verilmesinin
// sırada iki kez GÖRÜNMEDİĞİNİ kanıtlar.
//
// Görünseydi çağıran aynı depoya iki kez ayırma denerdi: ilki tükendiği için
// düşen bir depo, ikinci turda aynı cevabı verir ve geri düşme bir turunu boşa
// harcardı.
func TestRankLocationsYinelenenAdayTekKezSiralanir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_ankara", "sloc_ankara", "sloc_izmir"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_ankara", "sloc_izmir"}, sirali)
}

// TestSetShippingLocationBolgeSayisiSinirlidir sınırın İKİ YANINI da çiviler.
//
// Tek yanlı bir test (yalnızca "101 reddedilir") sınırın `>=`'e çevrilmesini
// yakalamaz; iki yan birlikte, sınırın hem VAR olduğunu hem de DOĞRU YERDE
// olduğunu sabitler.
func TestSetShippingLocationBolgeSayisiSinirlidir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	tamSinir := make([]string, 0, 100)
	for i := range 100 {
		tamSinir = append(tamSinir, fmt.Sprintf("reg_%03d", i))
	}

	kayit, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_sinir",
		RegionIDs:  tamSinir,
	})
	require.NoError(t, err, "tam sınırdaki istek KABUL edilmeli")
	assert.Len(t, kayit.RegionIDs, 100)

	_, err = kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_sinir_asan",
		RegionIDs:  append(tamSinir, "reg_fazla"),
	})
	require.Error(t, err, "sınırı bir aşan istek REDDEDİLMELİ")
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestSetShippingLocationAsiriUzunBolgeReddedilir metin uzunluğu sınırının
// bölge kimliklerinde de uygulandığını kanıtlar.
//
// Sınır olmadan tek bir istek veritabanına sınırsız büyüklükte metin yazardı;
// kardeş denetimler (boş kimlik, sayı sınırı) sınanıyorken bunun sınanmaması,
// korumanın yalnızca yarısının çivilendiği anlamına gelirdi.
func TestSetShippingLocationAsiriUzunBolgeReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	_, err := kurulum.svc.SetShippingLocation(context.Background(), service.SetShippingLocationInput{
		LocationID: "sloc_uzun_bolge",
		RegionIDs:  []string{"reg_" + strings.Repeat("x", 1024)},
	})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestRankLocationsAsiriUzunAdayReddedilir aynı sınırın ADAY kimliklerinde de
// uygulandığını kanıtlar.
func TestRankLocationsAsiriUzunAdayReddedilir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"sloc_" + strings.Repeat("y", 1024)})
	require.Error(t, err)
	assert.Empty(t, sirali)
	assert.True(t, errors.IsInvalid(err), "hata errors.Invalid olmalı: %v", err)
	assert.Equal(t, service.CodeInvalidInput, errors.CodeOf(err))
}

// TestRankLocationsEsitlikBozmaKirpilmisKimligeGoredir sıralamanın KIRPILMIŞ
// anahtara dayandığını kanıtlar.
//
// Ham dizeye dayansaydı sıra, çağıranın kimlikleri nasıl yazdığına bağlı
// olurdu: "  sloc_z" ham karşılaştırmada "sloc_a"dan ÖNCE gelir (boşluk
// harflerden küçüktür) ve sonuç, aynı iki depo için farklı yazımlarla farklı
// çıkardı. Dönüş değeri yine çağıranın dizesidir; eşleştirme ile dönüş ayrı
// şeylerdir ve bu test ikisini birden çiviler.
func TestRankLocationsEsitlikBozmaKirpilmisKimligeGoredir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)

	sirali, err := kurulum.svc.RankLocations(context.Background(), testRegionID,
		[]string{"  sloc_z", "sloc_a"})
	require.NoError(t, err)
	assert.Equal(t, []string{"sloc_a", "  sloc_z"}, sirali,
		"sıra kırpılmış kimliğe göre kurulmalı ama elemanlar çağıranın dizeleri kalmalı")
}

// TestRankLocationsDogrulamaSirasiCivilenmistir hangi hatanın kazandığını
// sabitler.
//
// Üçüncü satır asıl olandır: her iki girdi de bozukken çağıranın hangi hatayı
// göreceği bir SEÇİMDİR ve yazılmazsa denetimlerin yeri değiştiğinde sessizce
// değişir. Boş aday listesi önce gelir, çünkü o bir DÜNYA durumudur (Conflict)
// ve çağıranın "sipariş verilemez" dalı ona bağlıdır; boş bölge ise bir çağıran
// kusurudur ve bu paketin tek üretim çağıranında zaten oluşamaz.
func TestRankLocationsDogrulamaSirasiCivilenmistir(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ctx := context.Background()

	_, err := kurulum.svc.RankLocations(ctx, "", []string{"sloc_a"})
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "bölge boşken Invalid: %v", err)

	_, err = kurulum.svc.RankLocations(ctx, testRegionID, nil)
	require.Error(t, err)
	assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err),
		"aday boşken Conflict/CodeNoShippingLocation: %v", err)

	_, err = kurulum.svc.RankLocations(ctx, "", nil)
	require.Error(t, err)
	assert.Equal(t, service.CodeNoShippingLocation, errors.CodeOf(err),
		"İKİSİ DE boşken aday denetimi kazanır; sıra bilinçlidir ve burada çivilenir: %v", err)
}

// TestDepoPolitikasiOkumaVeSilmeBosKimligiReddeder yalnızca boşluk taşıyan bir
// lokasyon kimliğinin veritabanına hiç gitmediğini kanıtlar.
//
// Denetim olmasaydı boş kimlik depoya iner ve NotFound olarak geri dönerdi:
// istemci "böyle bir politika yok" görür, oysa gerçek kusur kendi isteğindedir.
// Kardeş denetim yazma yolunda sınanıyor; okuma ve silme yolları da aynı
// güvenceyi vermelidir.
func TestDepoPolitikasiOkumaVeSilmeBosKimligiReddeder(t *testing.T) {
	t.Parallel()

	kurulum := yeniKurulum(t)
	ctx := context.Background()

	for _, kimlik := range []string{"", "   "} {
		_, err := kurulum.svc.GetShippingLocation(ctx, kimlik)
		require.Error(t, err, "okuma boş kimliği reddetmeli: %q", kimlik)
		assert.True(t, errors.IsInvalid(err), "okuma hatası Invalid olmalı: %v", err)

		err = kurulum.svc.DeleteShippingLocation(ctx, kimlik)
		require.Error(t, err, "silme boş kimliği reddetmeli: %q", kimlik)
		assert.True(t, errors.IsInvalid(err), "silme hatası Invalid olmalı: %v", err)
	}
}
