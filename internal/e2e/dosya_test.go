//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	filesvc "github.com/bdrtr/gobit/internal/modules/file/service"
	productmodels "github.com/bdrtr/gobit/internal/modules/product/models"
)

// Bu dosya TEK bir zinciri uçtan uca kanıtlar:
//
//	yükleme -> adres -> ürün görseli
//
// # Neden yalnızca burada kanıtlanabilir
//
// Zincirin iki ucu İKİ AYRI modüldedir: file baytları alır ve bir adres
// üretir, product o adresi ürün kaydında saklar. İkisi birbirini import etmez
// ve etmemelidir (Prensip 2.1), yani hiçbir birim testi ikisini aynı anda
// göremez. file'ın kendi entegrasyon testi "adres üretildi" der ve orada
// durur; product'ın testi "verdiğim URL geri geldi" der ve o URL'in gerçekten
// bir şey döndürdüğünü hiç sormaz. Aradaki boşlukta tam olarak şu arıza
// yaşayabilir: yükleme çalışır, ürün kaydedilir, vitrindeki <img> kırık
// görünür.
//
// # Sunum ucu neden BAŞLIKSIZ çağrılıyor
//
// Adresi çağıran şey bir tarayıcının resim isteğidir ve tarayıcı bir <img>
// isteğine özel başlık EKLEYEMEZ: ne Authorization ne publishable anahtar.
// Testin de eklememesi bu yüzden bir kolaylık değil, senaryonun kendisidir
// ([adresiCagir] hiçbir başlık koymaz). Uç korumalı bir önek altına
// taşınsaydı bu dosya kırmızı yanardı — istenen budur.
//
// # Kanıtlananlar
//
//  1. Yönetim ucundan PNG yüklenir; 201 döner ve yanıt bir ADRES taşır.
//  2. O adres GERÇEKTEN çalışır: aynı baytlar, SAKLANAN içerik tipi ve
//     X-Content-Type-Options: nosniff.
//  3. Adres bir ürün görseli olarak kullanılabilir ve ürün kaydından okunan
//     adres hâlâ içerik döndürür.
//  4. Kimliksiz yükleme 401.
//  5. Yalan Content-Type ile gönderilen metin REDDEDİLİR ve diske hiçbir şey
//     yazılmaz.
//  6. Silinen yüklemenin adresi artık içerik döndürmez.

// yuklemeYolu yönetim yükleme ucudur.
//
// Yol ELLE yazılır, file/api paketinden okunmaz: oradaki sabit dışa kapalıdır
// ve olması gereken de budur — istemcinin gördüğü şey dizenin kendisidir.
// Yol değişirse bu test 404 alır ve bu doğrudur: yayımlanmış bir uç yolunu
// sessizce değiştiremez.
const yuklemeYolu = "/admin/v1/uploads"

// dosyaAlani multipart gövdesinde dosyanın beklendiği alan adıdır.
//
// Alan adı da yol gibi TEL ÜZERİNDEKİ sözleşmedir; aynı gerekçeyle elle
// yazılır.
const dosyaAlani = "file"

// nosniffBasligi tarayıcının içerik tipi tahminini kapatan başlıktır.
const nosniffBasligi = "X-Content-Type-Options"

// yuklemeGorunumu yükleme yanıtının test tarafındaki karşılığıdır.
//
// Tip file/api'nin DTO'sundan kopyalanmaz, YAZILIR: o tip dışa kapalıdır ve
// olması gereken de budur. Buradaki alan adları istemcinin gördüğü JSON
// sözleşmesidir; bir yeniden adlandırma testte görünmelidir.
type yuklemeGorunumu struct {
	// ID kaydın kimliğidir; silme ucu bunu alır.
	ID string `json:"id"`
	// URL dosyanın erişilebilir adresidir — zincirin orta halkası.
	URL string `json:"url"`
	// ContentType SAKLANAN, yani içerikten TESPİT EDİLMİŞ tiptir.
	ContentType string `json:"content_type"`
	// Size dosyanın bayt cinsinden boyutudur.
	Size int64 `json:"size"`
	// Checksum içeriğin SHA-256 özetidir.
	Checksum string `json:"checksum"`
	// ProviderID dosyayı saklayan sağlayıcının kimliğidir.
	ProviderID string `json:"provider_id"`
	// OriginalName istemcinin bildirdiği addır; yalnızca gösterimdir.
	OriginalName string `json:"original_name"`
	// UploadedBy yüklemeyi yapan çağıranın kimliğidir.
	UploadedBy string `json:"uploaded_by"`
}

// urunGorunumu ürün yanıtının bu testin ilgilendiği parçasıdır.
//
// models.Product'ın tamamı çözülmez: bu testin iddiası kataloğun şekliyle
// değil, YALNIZCA görsel adresiyle ilgilidir ve gereksiz alanları çözmek,
// katalogda yapılan alakasız bir değişikliği bu dosyada kırardı.
type urunGorunumu struct {
	// ID ürünün kimliğidir.
	ID string `json:"id"`
	// Images ürünün görselleridir.
	Images []struct {
		// ID görsel kaydının kimliğidir.
		ID string `json:"id"`
		// URL görselin adresidir; yüklemenin ürettiği adres buraya girer.
		URL string `json:"url"`
	} `json:"images"`
}

// pngIcerigi GERÇEK bir PNG üretir.
//
// Elle yazılmış bir bayt dizisi ("\x89PNG..." + çöp) de tip tespitini
// geçerdi, ama testin iddiası "sihirli baytlar tanınıyor" değil "gerçek bir
// görsel uçtan uca sağ salim geçiyor"dur. Kodlayıcıdan geçen bir görsel
// ayrıca boyutu ve içeriği önceden bilinemeyen bir gövde üretir, yani
// "aynı baytlar döndü" iddiası sabit bir dizeyi değil gerçek bir dosyayı
// sınar.
//
// Görsel BİLEREK tek renk değildir: tek renk PNG'i öyle iyi sıkıştırır ki
// gövde birkaç bayta iner ve "aynı baytlar" iddiası neredeyse boşa düşerdi.
func pngIcerigi(t *testing.T) []byte {
	t.Helper()

	gorsel := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := range 16 {
		for x := range 16 {
			gorsel.Set(x, y, color.RGBA{
				R: uint8(x * 16),
				G: uint8(y * 16),
				B: uint8((x ^ y) * 8),
				A: 255,
			})
		}
	}

	var tampon bytes.Buffer
	require.NoError(t, png.Encode(&tampon, gorsel), "fikstür PNG'i kodlanamadı")
	require.NotEmpty(t, tampon.Bytes(), "fikstür PNG'i boş olamaz")

	return tampon.Bytes()
}

// multipartGovde tek dosyalı bir multipart gövdesi kurar; gövdeyi ve isteğin
// Content-Type başlığını döner.
//
// Parçanın Content-Type'ı PARAMETREDİR ve bu testin can alıcı noktasıdır:
// istemcinin bildirdiği tip bir İDDİADIR ve yalan olabilir. Yardımcının
// [multipart.Writer.CreateFormFile]'ı kullanmamasının tek sebebi budur —
// o metot tipi her zaman "application/octet-stream" yazar ve yalan
// söyleyemez, yani sınanmak istenen durumu kuramaz.
func multipartGovde(t *testing.T, dosyaAdi, iddiaEdilenTip string, icerik []byte) (govde []byte, icerikTipi string) {
	t.Helper()

	var tampon bytes.Buffer
	yazici := multipart.NewWriter(&tampon)

	baslik := make(textproto.MIMEHeader)
	baslik.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name=%q; filename=%q`, dosyaAlani, dosyaAdi))
	baslik.Set("Content-Type", iddiaEdilenTip)

	parca, err := yazici.CreatePart(baslik)
	require.NoError(t, err, "multipart parçası açılamadı")

	_, err = parca.Write(icerik)
	require.NoError(t, err, "multipart parçası yazılamadı")
	require.NoError(t, yazici.Close(), "multipart gövdesi kapatılamadı")

	return tampon.Bytes(), yazici.FormDataContentType()
}

// yuklemeIstegi yükleme ucuna multipart bir istek yapar.
//
// yetki BOŞ verilirse Authorization başlığı hiç EKLENMEZ; "başlık yok" ile
// "boş başlık" farklı durumlardır ve 401 iddiası ilkini hedefler
// (bkz. [yonetimIstegi], aynı ayrım).
func yuklemeIstegi(t *testing.T, yetki, dosyaAdi, iddiaEdilenTip string, icerik []byte) *httptest.ResponseRecorder {
	t.Helper()

	govde, icerikTipi := multipartGovde(t, dosyaAdi, iddiaEdilenTip, icerik)

	istek := httptest.NewRequest(http.MethodPost, yuklemeYolu, bytes.NewReader(govde))
	istek.Header.Set("Content-Type", icerikTipi)

	if yetki != "" {
		istek.Header.Set("Authorization", yetki)
	}

	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// dosyaYukle gizli anahtarla bir dosya yükler ve dönen kaydı çözer.
func dosyaYukle(t *testing.T, dosyaAdi, iddiaEdilenTip string, icerik []byte) yuklemeGorunumu {
	t.Helper()

	kayit := yuklemeIstegi(t, "Bearer "+gizliAnahtar, dosyaAdi, iddiaEdilenTip, icerik)
	require.Equal(t, http.StatusCreated, kayit.Code,
		"yükleme 201 dönmeli; gövde: %s", kayit.Body.String())

	var zarf struct {
		Data yuklemeGorunumu `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"yükleme yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf.Data
}

// adresiCagir yükleme adresini HİÇBİR BAŞLIK GÖNDERMEDEN çağırır.
//
// Başlıksızlık bilinçlidir ve senaryonun kendisidir: bu adresi çağıran şey
// vitrindeki bir <img> etiketidir ve tarayıcı ona kimlik bilgisi ekleyemez
// (paket belgesi).
func adresiCagir(t *testing.T, adres string) *httptest.ResponseRecorder {
	t.Helper()

	istek := httptest.NewRequest(http.MethodGet, adres, http.NoBody)
	kayit := httptest.NewRecorder()
	testRouter.ServeHTTP(kayit, istek)

	return kayit
}

// hataKodu bir hata yanıtındaki MAKİNE kodunu döner.
//
// İddia status koduna değil bu koda bağlanır: status, hata SINIFINDAN
// türetilen kaba bir işarettir (422 hem "tip yasak" hem "gövde bozuk"
// olabilir), istemcinin gerçekten dallanacağı şey ise koddur.
func hataKodu(t *testing.T, kayit *httptest.ResponseRecorder) string {
	t.Helper()

	var zarf struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"hata yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf.Error.Code
}

// depodakiDosyalar yükleme kökündeki dosya adlarını döner.
//
// Disk seviyesinde bakmanın sebebi somut: "istek reddedildi" ile "diske
// hiçbir şey yazılmadı" AYNI ŞEY DEĞİLDİR. Reddedilen bir yükleme yarım bir
// dosya (ya da temizlenmemiş bir geçici dosya) bırakabilir ve bunu HTTP
// yanıtına bakarak görmek imkânsızdır — kaydı olmayan bir dosyanın adresi de
// yoktur, yani hiçbir uçtan sorulamaz.
func depodakiDosyalar(t *testing.T) []string {
	t.Helper()

	girdiler, err := os.ReadDir(dosyaKoku)
	require.NoError(t, err, "yükleme kökü okunamadı: %s", dosyaKoku)

	adlar := make([]string, 0, len(girdiler))
	for _, girdi := range girdiler {
		adlar = append(adlar, girdi.Name())
	}

	return adlar
}

// urunOlustur verilen görsel adresiyle bir ürün oluşturur ve kaydı döner.
//
// Ürün SERVİSTEN değil YÖNETİM UCUNDAN kurulur; zincirin son halkası tam
// olarak HTTP'de yaşıyor: yüklemenin döndürdüğü adres, bir istemci
// tarafından ürün oluşturma gövdesine konur. Servisi doğrudan çağırmak, o
// halkayı testin kendi eliyle atlaması olurdu.
//
// Görsel ürün OLUŞTURULURKEN verilir çünkü product modülünde ayrı bir görsel
// ucu yoktur: yol "images" alanıdır (bkz. product/api createProductRequest).
func urunOlustur(t *testing.T, gorselAdresi string) urunGorunumu {
	t.Helper()

	sira := fiksturSayaci.Add(1)
	kayit, err := yonetimGovdeliIstek(http.MethodPost, "/admin/v1/products", map[string]any{
		"handle": fmt.Sprintf("e2e-gorselli-urun-%d", sira),
		"title":  "Görselli Ürün",
		"status": string(productmodels.StatusPublished),
		"images": []map[string]any{{"url": gorselAdresi, "rank": 0}},
	})
	require.NoError(t, err, "ürün isteği kurulamadı")
	require.Equal(t, http.StatusCreated, kayit.Code,
		"ürün 201 dönmeli; gövde: %s", kayit.Body.String())

	return urunCoz(t, kayit)
}

// urunCoz bir ürün yanıtının zarfını çözer.
func urunCoz(t *testing.T, kayit *httptest.ResponseRecorder) urunGorunumu {
	t.Helper()

	var zarf struct {
		Data urunGorunumu `json:"data"`
	}
	require.NoError(t, json.Unmarshal(kayit.Body.Bytes(), &zarf),
		"ürün yanıtı çözülemedi; gövde: %s", kayit.Body.String())

	return zarf.Data
}

// TestYuklenenGorselUrunGorseliOlarakKullanilir zincirin TAMAMINI tek akışta
// kanıtlar.
//
// Adımlar bilerek TEK testtedir: aralarındaki bağ verinin kendisidir
// (yüklemenin ürettiği adres) ve ayrı testlere bölmek, o adresi paylaşmak
// için testler arası bir global gerektirirdi — yani zinciri kanıtlayan şeyi
// zincirin dışına çıkarmak.
func TestYuklenenGorselUrunGorseliOlarakKullanilir(t *testing.T) {
	icerik := pngIcerigi(t)

	var yukleme yuklemeGorunumu

	t.Run("yükleme 201 döner ve adres taşır", func(t *testing.T) {
		yukleme = dosyaYukle(t, "manzara.png", "image/png", icerik)

		require.NotEmpty(t, yukleme.URL,
			"yanıt bir adres taşımalı; adressiz bir yükleme kaydı hiçbir yerde kullanılamaz")
		assert.NotEmpty(t, yukleme.ID, "silme ucunun alacağı kimlik yanıtta olmalı")
		assert.Equal(t, "image/png", yukleme.ContentType,
			"saklanan tip İÇERİKTEN tespit edilmiş olmalı")
		assert.Equal(t, int64(len(icerik)), yukleme.Size,
			"kaydedilen boyut gönderilen gövdeyle aynı olmalı; farklıysa akış bir yerde kesilmiş demektir")
		assert.NotEmpty(t, yukleme.Checksum, "içerik özeti kaydedilmeli")
		assert.Equal(t, "manzara.png", yukleme.OriginalName,
			"istemcinin adı GÖSTERİM için saklanmalı")
		assert.NotEmpty(t, yukleme.UploadedBy,
			"korumalı uçtan gelen yükleme çağıranın kimliğini taşımalı")
		assert.NotEmpty(t, yukleme.ProviderID, "dosyayı saklayan sağlayıcı bilinmeli")
	})

	require.NotEmpty(t, yukleme.URL, "ön koşul: sonraki adımlar adres olmadan yürütülemez")

	t.Run("adres gerçekten çalışır", func(t *testing.T) {
		kayit := adresiCagir(t, yukleme.URL)

		require.Equal(t, http.StatusOK, kayit.Code,
			"yüklemenin adresi içerik döndürmeli; gövde: %s", kayit.Body.String())
		assert.Equal(t, icerik, kayit.Body.Bytes(),
			"dönen baytlar yüklenenlerle BİREBİR aynı olmalı")
		assert.Equal(t, yukleme.ContentType, kayit.Header().Get("Content-Type"),
			"Content-Type SAKLANAN tipten yazılmalı, istemcinin iddiasından değil")
		assert.Equal(t, "nosniff", kayit.Header().Get(nosniffBasligi),
			"nosniff olmadan tarayıcı gönderdiğimiz tipe rağmen içeriğe bakıp kendi tahminini yapar")
	})

	var urun urunGorunumu

	t.Run("adres ürün görseli olarak kullanılabilir", func(t *testing.T) {
		urun = urunOlustur(t, yukleme.URL)

		require.Len(t, urun.Images, 1, "ürün tek görselle oluşturulmuş olmalı")
		assert.Equal(t, yukleme.URL, urun.Images[0].URL,
			"ürün kaydındaki adres yüklemenin ürettiği adresin TA KENDİSİ olmalı")
	})

	require.NotEmpty(t, urun.ID, "ön koşul: son adım ürün olmadan yürütülemez")

	// Son halka: adres ürün kaydından OKUNUR ve o okunan adres çağrılır.
	// Yükleme yanıtındaki adresi tekrar çağırmak yetmezdi — o, zincirin
	// product'tan geçen kısmını hiç sınamaz ve ürün kaydı adresi kırparak
	// (ya da kaçış karakteri ekleyerek) saklasa bile test yeşil kalırdı.
	t.Run("ürün kaydındaki adres hâlâ içerik döndürür", func(t *testing.T) {
		okunan := yonetimIstegi(t, http.MethodGet, "/admin/v1/products/"+urun.ID,
			"Bearer "+gizliAnahtar)
		require.Equal(t, http.StatusOK, okunan.Code,
			"ürün okunmalı; gövde: %s", okunan.Body.String())

		kalici := urunCoz(t, okunan)
		require.Len(t, kalici.Images, 1, "görsel kalıcı olmalı")
		require.Equal(t, yukleme.URL, kalici.Images[0].URL,
			"kalıcı kayıttaki adres yüklemenin adresiyle aynı olmalı")

		kayit := adresiCagir(t, kalici.Images[0].URL)
		require.Equal(t, http.StatusOK, kayit.Code,
			"ürünün taşıdığı adres içerik döndürmeli; gövde: %s", kayit.Body.String())
		assert.Equal(t, icerik, kayit.Body.Bytes(),
			"vitrinin göreceği baytlar yüklenenlerle aynı olmalı")
	})
}

// TestKimliksizYuklemeReddedilir yükleme ucunun KORUMALI olduğunu doğrular.
//
// Sunum ucunun korumasızlığı bilinçli bir ödündür (file/api paket belgesi) ve
// tam da bu yüzden YAZMA yolunun korumalı kaldığı ayrıca kanıtlanmalıdır:
// ikisi birlikte "adresini bilen okur, ama depoya yalnızca kimliği olan
// yazar" cümlesini kurar. Yükleme ucu korumasız kalsaydı, korumasız sunum
// ucu bir dosya paylaşım servisine dönüşürdü.
func TestKimliksizYuklemeReddedilir(t *testing.T) {
	oncekiler := depodakiDosyalar(t)

	kayit := yuklemeIstegi(t, "", "gizlice.png", "image/png", pngIcerigi(t))

	assert.Equal(t, http.StatusUnauthorized, kayit.Code,
		"kimliksiz yükleme 401 almalı; gövde: %s", kayit.Body.String())
	assert.Equal(t, oncekiler, depodakiDosyalar(t),
		"reddedilen istek depoya HİÇBİR ŞEY yazmamalı")
}

// TestYalanIcerikTipiyleGonderilenMetinReddedilir izin listesinin istemcinin
// İDDİASINA değil dosyanın İÇERİĞİNE bakıldığını kanıtlar.
//
// Senaryo, korumasız sunum ucunun en tehlikeli hâlidir: "image/png" diye
// gönderilen bir HTML dosyası kabul edilseydi, aynı kökenden sunulduğunda
// depolanmış XSS olurdu. İstemcinin Content-Type'ına güvenen bir izin listesi
// hiçbir şey elemez — çünkü saldırgan başlığı da kendisi yazar.
//
// İki gövde de sınanır ve ayrım anlamlıdır: düz metin "izin listesi bilmediği
// tipi reddediyor mu" sorusunu, HTML ise "asıl tehlikeli tip geçiyor mu"
// sorusunu yanıtlar.
func TestYalanIcerikTipiyleGonderilenMetinReddedilir(t *testing.T) {
	senaryolar := []struct {
		ad      string
		dosya   string
		icerik  []byte
		beklTip string
	}{
		{
			ad:      "düz metin",
			dosya:   "gorsel.png",
			icerik:  []byte("bu bir metin dosyasıdır, görsel değil.\n"),
			beklTip: "text/plain",
		},
		{
			ad:     "HTML",
			dosya:  "gorsel.png",
			icerik: []byte("<html><body><script>alert(1)</script></body></html>"),
			// Tespit edilen tip mesajda GEÇMEK ZORUNDA DEĞİLDİR; burada
			// yalnızca senaryonun ne olduğunu okunur kılar.
			beklTip: "text/html",
		},
	}

	for _, senaryo := range senaryolar {
		t.Run(senaryo.ad, func(t *testing.T) {
			oncekiler := depodakiDosyalar(t)

			// İstemci hem başlıkta hem dosya adında "png" diyor; ikisi de
			// birer İDDİADIR ve ikisi de yalandır.
			kayit := yuklemeIstegi(t, "Bearer "+gizliAnahtar, senaryo.dosya, "image/png", senaryo.icerik)

			require.Equal(t, http.StatusUnprocessableEntity, kayit.Code,
				"%s içeriği reddedilmeli; gövde: %s", senaryo.beklTip, kayit.Body.String())
			assert.Equal(t, filesvc.CodeTypeNotAllowed, hataKodu(t, kayit),
				"ret sebebi izin listesi olmalı; başka bir kod, dosyanın başka bir sebeple "+
					"düştüğü ve tip denetiminin hiç çalışmamış olabileceği anlamına gelir")
			assert.Equal(t, oncekiler, depodakiDosyalar(t),
				"reddedilen içerik depoya yazılmamalı; yarım ya da geçici bir dosya bile kalmamalı")
		})
	}
}

// TestSilinenYuklemeninAdresiIcerikDondurmez silmenin dosyayı GERÇEKTEN
// götürdüğünü doğrular.
//
// Yalnızca kaydı silen bir uygulama da 204 döner ve yönetim listesinden kayıt
// kaybolurdu; dosya diskte kalır ve adresi bilen herkes onu okumaya devam
// ederdi. "Sildim" diyen bir sistemin en kötü yalanı budur, çünkü sildiğini
// sanan kişi bir daha bakmaz.
func TestSilinenYuklemeninAdresiIcerikDondurmez(t *testing.T) {
	icerik := pngIcerigi(t)
	yukleme := dosyaYukle(t, "silinecek.png", "image/png", icerik)

	require.Equal(t, http.StatusOK, adresiCagir(t, yukleme.URL).Code,
		"ön koşul: adres silmeden ÖNCE çalışmalı; çalışmıyorsa test silmeyi değil "+
			"hiç var olmamış bir dosyayı sınardı")

	// Dosyanın silmeden ÖNCE diskte olduğu ayrıca iddia edilir. Yalnızca
	// "sonra yok" demek hiçbir şey kanıtlamazdı: adı hiç eşleşmeyen bir
	// arama da "yok" der ve iddia sessizce boşa düşerdi.
	anahtar := dosyaAdiniCikar(yukleme.URL)
	require.Contains(t, depodakiDosyalar(t), anahtar,
		"ön koşul: yüklenen dosya silmeden önce depoda olmalı")

	silme := yonetimIstegi(t, http.MethodDelete, yuklemeYolu+"/"+yukleme.ID,
		"Bearer "+gizliAnahtar)
	require.Equal(t, http.StatusNoContent, silme.Code,
		"silme 204 dönmeli; gövde: %s", silme.Body.String())

	kayit := adresiCagir(t, yukleme.URL)

	assert.Equal(t, http.StatusNotFound, kayit.Code,
		"silinen yüklemenin adresi 404 dönmeli; gövde: %s", kayit.Body.String())
	assert.NotEqual(t, icerik, kayit.Body.Bytes(),
		"silinen dosyanın baytları hiçbir durumda dönmemeli")
	assert.Equal(t, "nosniff", kayit.Header().Get(nosniffBasligi),
		"nosniff HATA yanıtlarında da bulunmalı; başlık ilk satırda yazıldığı için "+
			"eksikliği, sunum ucunun tamamının korumasız kaldığı anlamına gelir")
	assert.NotContains(t, depodakiDosyalar(t), anahtar,
		"silme dosyayı DİSKTEN de götürmeli; yalnızca kaydı silmek, adresi bilen "+
			"birinin okumaya devam etmesi demek olurdu")
}

// dosyaAdiniCikar bir yükleme adresinin son parçasını, yani depo anahtarını
// döner.
//
// Anahtar yanıtta yayımlanmaz (file/api dto belgesi) ve doğrusu da budur;
// diski denetleyebilmek için adresten türetilmesi bu testin YEREL sağlayıcıya
// dayanan tek yeridir. Nesne deposuna geçen bir kurulumda adres imzalıdır ve
// bu satır anlamını yitirir — o gün doğru cevap satırı düzeltmek değil,
// disk iddiasını tamamen kaldırmaktır.
func dosyaAdiniCikar(adres string) string {
	for i := len(adres) - 1; i >= 0; i-- {
		if adres[i] == '/' {
			return adres[i+1:]
		}
	}

	return adres
}
