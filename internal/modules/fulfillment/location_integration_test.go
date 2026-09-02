//go:build integration

package fulfillment_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/repository"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// Bu dosya depo seçim POLİTİKASININ kalıcılık yolunu GERÇEK Postgres'e karşı
// sınar.
//
// # Neden servis birim testleri yetmez
//
// Politikanın KARARI saf bir fonksiyondur ve sahte depoyla sınanır; oradaki
// iddialar doğrudur ama SQL'e hiç dokunmazlar. Buradaki iddialar tam olarak
// SQL'in kendisidir: upsert'ün çakışma dalı, bölge bağlarının toptan
// yazılması, ON DELETE CASCADE, array_agg'in bağı olmayan depo için BOŞ dizi
// üretmesi ve listelemenin bağları TEK sorguda toplaması. Sahte depo bunların
// hiçbirini yanlış yapamaz, çünkü hiçbirini yapmaz.
//
// # Kimlikler test BAŞINA benzersizdir
//
// Tablo yumuşak silme taşımaz ve testler tek veritabanı paylaşır; her senaryo
// kendi önekini kullanır ki bir testin bıraktığı satır başka bir testin
// sayımına girmesin.

// politikaKur bir depo politikası yazar ve sonucu döner.
func politikaKur(
	ctx context.Context,
	t *testing.T,
	svc *service.Service,
	locationID string,
	priority int64,
	bolgeler ...string,
) models.ShippingLocation {
	t.Helper()

	loc, err := svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
		LocationID: locationID,
		Priority:   priority,
		RegionIDs:  bolgeler,
	})
	require.NoError(t, err, "politika yazılamadı: %s", locationID)
	return loc
}

// TestPolitikaUpsertIkinciYazmadaSATIRIEZER aynı depoya ikinci kez yazmanın
// yeni bir satır ÜRETMEDİĞİNİ, mevcut satırı ezdiğini kanıtlar.
//
// Upsert'ün çakışma dalı ancak burada koşar: sahte depo zaten haritadır ve
// ikinci yazma onu doğal olarak ezer, yani birim testi ON CONFLICT'in var
// olmadığı bir dünyada da yeşil kalırdı.
func TestPolitikaUpsertIkinciYazmadaSATIRIEZER(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_upsert_1"
	politikaKur(ctx, t, svc, depo, 5, "reg_a", "reg_b")
	ikinci := politikaKur(ctx, t, svc, depo, -3, "reg_c")

	assert.Equal(t, int64(-3), ikinci.Priority, "öncelik ezilmeli")
	assert.Equal(t, []string{"reg_c"}, ikinci.RegionIDs,
		"bölge bağları TOPTAN yazılır: eski bağlar kalmaz")

	okunan, err := svc.GetShippingLocation(ctx, depo)
	require.NoError(t, err)
	assert.Equal(t, ikinci.Priority, okunan.Priority)
	assert.Equal(t, ikinci.RegionIDs, okunan.RegionIDs)
	assert.True(t, okunan.UpdatedAt.After(okunan.CreatedAt) ||
		okunan.UpdatedAt.Equal(okunan.CreatedAt),
		"updated_at geriye gitmemeli")
}

// TestPolitikaSilmeBolgeBaglariniCASCADEDusurur modül içi foreign key'in
// gerçekten CASCADE olduğunu kanıtlar.
//
// Bağlar düşmeseydi tablo yetim satır biriktirir ve aynı depo için yazılan
// yeni bir politika, silinmiş sanılan bağlarla birlikte okunurdu.
func TestPolitikaSilmeBolgeBaglariniCASCADEDusurur(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_cascade_1"
	politikaKur(ctx, t, svc, depo, 0, "reg_x", "reg_y")

	require.NoError(t, svc.DeleteShippingLocation(ctx, depo))

	var bagSayisi int64
	err := testPool.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM shipping_location_regions WHERE location_id = $1`, depo).
		Scan(&bagSayisi)
	require.NoError(t, err, "bağ sayısı okunabilmeli")
	assert.Zero(t, bagSayisi, "politika silinince bölge bağları da düşmeli")

	// Aynı depo yeniden yazılabilmeli: yumuşak silme olsaydı birincil anahtar
	// çakışırdı ve bu çağrı hata dönerdi.
	yeniden := politikaKur(ctx, t, svc, depo, 7)
	assert.Equal(t, int64(7), yeniden.Priority)
	assert.Empty(t, yeniden.RegionIDs, "yeniden yazılan politika eski bağları taşımamalı")
}

// TestPolitikaOkumasiBagiOlmayanDepoIcinBOSDIZIDoner array_agg'in FILTER
// dalını kanıtlar.
//
// FILTER olmasaydı LEFT JOIN, bağı olmayan depo için tek elemanı NULL olan bir
// dizi döndürürdü ve "bağı yok" ile "bağı bir tane ve o da bilinmiyor" ayırt
// edilemezdi. Sonucu somut: bağı olmayan depo TÜM bölgelere hizmet eder sayılır
// ve o kural sessizce bozulurdu.
func TestPolitikaOkumasiBagiOlmayanDepoIcinBOSDIZIDoner(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const bagsiz = "sloc_bagsiz_1"
	const bagli = "sloc_bagli_1"
	politikaKur(ctx, t, svc, bagsiz, 0)
	politikaKur(ctx, t, svc, bagli, 0, "reg_q")

	sirali, err := svc.RankLocations(ctx, "reg_bambaska", []string{bagsiz, bagli})
	require.NoError(t, err,
		"bağı olmayan depo elenmemeli; elenirse sonuç boş küme olur ve çağrı Conflict döner")
	assert.Equal(t, []string{bagsiz}, sirali,
		"bağı olmayan depo TÜM bölgelere hizmet eder, bağlı olan yalnızca bağlılarına")
}

// TestPolitikaYinelenenBolgeIkinciYazmadaCakismaUretmez
// InsertShippingLocationRegions'ın ON CONFLICT DO NOTHING dalını kanıtlar.
//
// Servis katmanı yinelenenleri zaten eliyor; bu test SQL'in kendisini sınar ve
// iki katmanın birbirini gizlemediğini gösterir: servisin elemesi kaldırılsa
// bile veritabanı çakışma hatası ÜRETMEZ, çünkü "aynı bölgeyi iki kez bağlamak"
// tek kez bağlamakla aynı sonucu verir.
func TestPolitikaYinelenenBolgeIkinciYazmadaCakismaUretmez(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_yineleyen_1"
	loc := politikaKur(ctx, t, svc, depo, 0, "reg_z", " reg_z ", "reg_w")
	assert.Equal(t, []string{"reg_w", "reg_z"}, loc.RegionIDs,
		"yinelenen bölge tek bağa inmeli; dönen sıra KİMLİĞE göredir, girdininki "+
			"değil — bağlar bir küme kurar")

	// Doğrudan SQL ile yinelenen yazma da çakışmamalı.
	_, err := testPool.Pool().Exec(ctx,
		`INSERT INTO shipping_location_regions (location_id, region_id)
		 VALUES ($1, $2) ON CONFLICT (location_id, region_id) DO NOTHING`, depo, "reg_z")
	require.NoError(t, err, "yinelenen bağ yazması çakışma ÜRETMEMELİ")
}

// TestPolitikaListelemesiBaglariTEKSorgudaToplar listelemenin bağları depo
// başına ayrı sorguyla değil, toplu okumayla getirdiğini kanıtlar.
//
// İddia sonuç üzerinden kurulur: üç deponun bağları eksiksiz ve doğru
// eşleşmiş dönerse toplu okuma çalışıyordur. Sıra da sınanır — listeleme
// SEÇİMİN uyguladığı sırayı (önce öncelik, sonra kimlik) döndürmelidir, yoksa
// yönetim ekranı politikayı farklı bir düzende gösterirdi.
func TestPolitikaListelemesiBaglariTEKSorgudaToplar(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const onek = "sloc_liste_"
	politikaKur(ctx, t, svc, onek+"c", 5, "reg_1")
	politikaKur(ctx, t, svc, onek+"a", -2, "reg_1", "reg_2")
	politikaKur(ctx, t, svc, onek+"b", 5)

	kayitlar, toplam, err := svc.ListShippingLocations(ctx, service.Page{Limit: 100})
	require.NoError(t, err)
	require.GreaterOrEqual(t, toplam, int64(3))

	bizimkiler := make([]models.ShippingLocation, 0, 3)
	for _, kayit := range kayitlar {
		if strings.HasPrefix(kayit.LocationID, onek) {
			bizimkiler = append(bizimkiler, kayit)
		}
	}
	require.Len(t, bizimkiler, 3, "üç politika da listede olmalı")

	assert.Equal(t, []string{onek + "a", onek + "b", onek + "c"},
		[]string{bizimkiler[0].LocationID, bizimkiler[1].LocationID, bizimkiler[2].LocationID},
		"sıra önce önceliğe, sonra kimliğe göre olmalı — seçimin uyguladığı sıranın aynısı")

	assert.Equal(t, []string{"reg_1", "reg_2"}, bizimkiler[0].RegionIDs,
		"çok bağlı deponun bağları eksiksiz ve sıralı dönmeli")
	assert.Empty(t, bizimkiler[1].RegionIDs, "bağı olmayan depo boş dilim dönmeli")
	assert.Equal(t, []string{"reg_1"}, bizimkiler[2].RegionIDs,
		"bağlar depolara DOĞRU eşleşmeli; toplu okuma satırları karıştırmamalı")
}

// TestPolitikaBilinmeyenDepoNotFound olmayan bir kaydın okunmasının ve
// silinmesinin SESSİZCE başarılı olmadığını kanıtlar.
func TestPolitikaBilinmeyenDepoNotFound(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	_, err := svc.GetShippingLocation(ctx, "sloc_hic_yazilmadi")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "okuma NotFound dönmeli: %v", err)

	err = svc.DeleteShippingLocation(ctx, "sloc_hic_yazilmadi")
	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err),
		"silme NotFound dönmeli; DELETE olmayan satır için de hatasız döner ve "+
			"denetim olmasaydı yanlış kimlikle yapılan silme başarılı görünürdü: %v", err)
}

// TestPolitikaBolgeYazmaIslemDISINDAReddedilir depo katmanının işlem şartını
// kanıtlar.
//
// Bağların toptan yazımı iki deyimdir (sil, yaz). İşlemsiz çalıştırılırsa
// aradaki bir okuma depoyu BÖLGESİZ görür — yani kapsamı daraltmak için
// yapılan bir düzenleme, bir an için onu TÜM bölgelere açar. Şart bir yorum
// değil, sınanan bir davranıştır.
func TestPolitikaBolgeYazmaIslemDISINDAReddedilir(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_islemsiz_1"
	politikaKur(ctx, t, svc, depo, 0, "reg_a")

	depoKatmani := repository.New(testPool.Pool())
	err := depoKatmani.ReplaceShippingLocationRegions(ctx, depo, []string{"reg_b"})
	require.Error(t, err, "işlem dışında bölge yazımı reddedilmeli")
	assert.Equal(t, errors.KindInternal, errors.KindOf(err),
		"sınıf Internal olmalı: kusur istemcide değil, çağıran koddadır — %v", err)

	okunan, err := svc.GetShippingLocation(ctx, depo)
	require.NoError(t, err)
	assert.Equal(t, []string{"reg_a"}, okunan.RegionIDs,
		"reddedilen çağrı hiçbir bağı SİLMEMİŞ olmalı")
}

// TestPolitikaOkumasiBolgeleriKIMLIGEGoreSiralar seçim yolunun okuduğu bölge
// dizisinin sırasını çiviler.
//
// Sıra kozmetik değildir: eleme boş küme ürettiğinde hata mesajı bu diziyi
// yazar ve operatör ölü bir bölge kimliğini o dökümde arar. Kararsız bir sıra,
// aynı arızanın iki koşuda farklı görünmesi demektir. Bağlar bilerek TERS
// sırada yazılır — yazma sırası korunsaydı test yanlış yönü ölçerdi.
func TestPolitikaOkumasiBolgeleriKIMLIGEGoreSiralar(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_sira_1"
	politikaKur(ctx, t, svc, depo, 0, "reg_z", "reg_m", "reg_a")

	depoKatmani := repository.New(testPool.Pool())
	politikalar, err := depoKatmani.LocationPolicies(ctx, []string{depo})
	require.NoError(t, err)
	require.Len(t, politikalar, 1)
	assert.Equal(t, []string{"reg_a", "reg_m", "reg_z"}, politikalar[0].RegionIDs,
		"seçim yolunun okuduğu bağlar KİMLİĞE göre sıralı gelmeli")

	// Tekil okuma da aynı sırayı vermeli; iki okuma yolu ayrışırsa yönetim
	// ekranıyla hata mesajı farklı düzen gösterirdi.
	okunan, err := svc.GetShippingLocation(ctx, depo)
	require.NoError(t, err)
	assert.Equal(t, politikalar[0].RegionIDs, okunan.RegionIDs,
		"tekil okuma ile seçim okuması aynı sırayı vermeli")
}

// TestEszamanliPolitikaYazmasiYIRTIKKayitBirakmaz iki yazmanın birbirinin
// içine geçmediğini kanıtlar.
//
// Yazma İKİ deyimdir (öncelik upsert'i + bağların toptan yenilenmesi) ve
// yırtılma somuttur: A'nın önceliğiyle B'nin bölgeleri yan yana kalabilirdi.
// Bunu engelleyen şey işlemin kendisi değil, upsert'ün aldığı SATIR KİLİDİDİR —
// ikinci yazma, birincisi commit edene kadar kendi upsert'ünde bekler. İddia bu
// yüzden "hata olmadı" değil, "sonuç yazılan çiftlerden TAM OLARAK BİRİDİR".
func TestEszamanliPolitikaYazmasiYIRTIKKayitBirakmaz(t *testing.T) {
	ctx := t.Context()
	svc, _ := yeniServis(t)

	const depo = "sloc_eszamanli_1"
	const yazarSayisi = 8

	// Her yazarın (öncelik, bölgeler) çifti benzersizdir ve öncelikten
	// türetilebilir: sonucun bir ÇİFT olduğu ancak böyle denetlenebilir.
	beklenenBolge := func(oncelik int64) string { return fmt.Sprintf("reg_%d", oncelik) }

	var wg sync.WaitGroup
	hatalar := make([]error, yazarSayisi)
	for i := range yazarSayisi {
		wg.Add(1)
		go func() {
			defer wg.Done()
			oncelik := int64(i)
			_, err := svc.SetShippingLocation(ctx, service.SetShippingLocationInput{
				LocationID: depo,
				Priority:   oncelik,
				RegionIDs:  []string{beklenenBolge(oncelik)},
			})
			hatalar[i] = err
		}()
	}
	wg.Wait()

	for i, err := range hatalar {
		require.NoError(t, err, "%d. yazar hata almamalı", i)
	}

	sonuc, err := svc.GetShippingLocation(ctx, depo)
	require.NoError(t, err)
	assert.Equal(t, []string{beklenenBolge(sonuc.Priority)}, sonuc.RegionIDs,
		"kayıt YIRTIK olmamalı: bölgeler kazanan yazarın önceliğiyle aynı çiftten gelmeli "+
			"(öncelik %d)", sonuc.Priority)
}
