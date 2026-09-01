package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/config"
)

// ortamOrnegiYolu operatörün ayarları öğrendiği tek belgedir.
//
// Kod okumayan bir kurulumcu için ayarların LİSTESİ burasıdır; burada
// yazmayan bir kol, o kurulumcu için var değildir.
const ortamOrnegiYolu = ".env.example"

// composeYolu yerel geliştirme yığınını açan compose dosyasıdır.
//
// .env.example'daki değişkenlerin bir bölümü uygulamaya değil BU dosyaya
// gider (Postgres portu, Redis parolası); ikinci tüketici olarak burada
// aranır.
const composeYolu = "deploy/docker-compose.yml"

// eklentilerYolu eklenti kaynaklarının köküdür.
//
// Üçüncü tüketici: eklenti ayarları (örn. STRIPE_API_KEY) Config'te DEĞİL,
// eklentinin kendi kaynağında okunur — çekirdek eklentileri tanımaz.
const eklentilerYolu = "plugins"

// readmeYolu deponun anlatı belgesidir.
const readmeYolu = "README.md"

// zorunluAyarOrnekDegeri envDefault TAŞIMAYAN alanların .env.example'da alması
// gereken değerdir: BOŞ.
//
// Karar şu soruya cevaptır: varsayılanı olmayan bir ayar belgede nasıl
// görünmeli? Üç seçenek vardı ve ikisi zarar verir.
//
//  1. Hiç yazılmasın. REDDEDİLDİ: JWT_SECRET ve ADMIN_BOOTSTRAP_PASSWORD tam
//     da varsayılanı OLMAMASI gereken, yani operatörün MUTLAKA bilmesi
//     gereken ayarlardır. Belgede yoklarsa üretime çıkan kurulum onları hiç
//     duymamış olur.
//  2. Örnek bir değer yazılsın. REDDEDİLDİ: bu dosya "cp .env.example .env"
//     ile OLDUĞU GİBİ kopyalanır. Örnek bir JWT_SECRET, jetonları herkesin
//     bildiği bir sırla imzalayan çalışır bir kurulum üretirdi;
//     ADMIN_BOOTSTRAP_PASSWORD ise gerçek bir yönetici hesabı tohumlardı.
//     Kopyalanan bir belgede "örnek değer" diye bir şey yoktur, yalnızca
//     GERÇEK değer vardır.
//  3. Anahtar yazılsın, değer boş bırakılsın. SEÇİLDİ: operatör kolu görür,
//     kopyalayan kişi ise kodun ilan ettiği davranışa düşer — geliştirmede
//     açılışa özel rastgele sır, paylaşılan ortamda açılışın durması.
//
// Örnek değerlerin yeri YORUM satırıdır (bkz. STRIPE_API_KEY ve
// "openssl rand -base64 48"); yorum kopyalansa bile hiçbir şey ayarlamaz.
const zorunluAyarOrnekDegeri = ""

// bilincliAyrilanlar .env.example'da varsayılanından BİLEREK farklı yazılmış
// ayarları, adlarından gerekçelerine eşler.
//
// Buradaki her giriş bir BORÇTUR: belgeyi okuyan operatör, kodun varsayılanını
// yanlış öğrenir. Bedeli ödemeye değdiğini gerekçe açıklamak zorundadır;
// gerekçesiz giriş eklemek testi sessizleştirmek olur, bu da testin varlık
// sebebini yok eder.
//
// Muafiyetler ÇÜRÜMEZ: aşağıdaki denetim, artık ayrışmayan bir girişte de
// hata verir ve girişin SİLİNMESİNİ ister. Yoksa liste zamanla "bir zamanlar
// farklıydı" kayıtlarıyla dolar ve gerçek bir ayrışmayı örter.
var bilincliAyrilanlar = map[string]string{
	// Kodun varsayılanı "json"dır ve öyle olmalıdır: üretimde logu okuyan
	// şey insan değil, toplayıcıdır. .env.example ise KOPYALANMAK üzere
	// vardır ve kopyalayan kişi yerelde terminale bakar; orada tek satırlık
	// JSON, bir hata mesajını gözle takip etmeyi imkânsızlaştırır. Ayrışma
	// güvenliği etkilemez ve dosyadaki yorum kodun varsayılanını açıkça
	// söyler, yani operatör yanlış öğrenmez.
	"LOG_FORMAT": "yerel geliştirmede terminal okunaklılığı; üretim varsayılanı json kalır",
}

// composeIkameDeseni compose dosyasındaki ${AD} ve ${AD:-varsayılan} biçimlerini yakalar.
//
// Baştaki (^|[^$]) grubu ZORUNLUDUR: compose'da $${AD} yazımı bir ikame
// DEĞİL, konteynerin kendi kabuğuna geçirilen kaçırılmış bir dolardır
// (healthcheck komutları böyle yazılır). Onları ikame sanmak, compose'un hiç
// okumadığı bir değişkeni belgelenmiş saymak olurdu.
var composeIkameDeseni = regexp.MustCompile(`(?m)(^|[^$])\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// buyukHarfDizeDeseni Go kaynağındaki BÜYÜK HARFLİ dize sabitlerini yakalar.
//
// Eklenti ayar adları kaynakta böyle durur (const apiKeySetting =
// "STRIPE_API_KEY"); ad bir sabitin arkasına saklandığı için çağrı yerini
// aramak yetmez, değerin kendisi aranır.
var buyukHarfDizeDeseni = regexp.MustCompile(`"([A-Z][A-Z0-9_]*)"`)

// pluginsAtamaDeseni README'deki PLUGINS=... örneklerini yakalar.
var pluginsAtamaDeseni = regexp.MustCompile(`PLUGINS=([A-Za-z0-9_,-]+)`)

// ayarAlani Config'in ortamdan okunan tek bir alanıdır.
type ayarAlani struct {
	// ad ortam değişkeninin adıdır (env etiketi).
	ad string
	// yol Go alan yoludur; hata mesajının hangi alanı işaret ettiğini söyler.
	yol string
	// varsayilan envDefault etiketinin değeridir.
	varsayilan string
	// varsayilanVar, envDefault etiketinin BULUNUP bulunmadığıdır. Boş bir
	// varsayılan ile hiç varsayılan olmaması ayrı şeylerdir.
	varsayilanVar bool
}

// composeDegisken compose dosyasındaki tek bir ${...} ikamesidir.
type composeDegisken struct {
	// varsayilan ${AD:-varsayılan} biçimindeki yedektir.
	varsayilan string
	// varsayilanVar yedeğin YAZILIP yazılmadığıdır. Yedeksiz bir ikame için
	// değer karşılaştırması yapılamaz ama belgelenmiş olması yine de gerekir.
	varsayilanVar bool
}

// ortamAtamasi .env.example'daki tek bir KEY=VALUE satırıdır.
type ortamAtamasi struct {
	// deger tırnakları soyulmuş değerdir.
	deger string
	// satir dosyadaki 1 tabanlı satır numarasıdır.
	satir int
}

// configAyarlari Config yapısını yansımayla gezip ortamdan okunan her alanı döner.
//
// Yansıma ZORUNLUDUR, elle liste değil: bu testin varlık sebebi, YARIN eklenen
// bir ayarın belgesiz kalmasını engellemektir. Elle yazılmış bir liste tam da
// o alanı içermez ve kuralı yalnızca bugün için uygular.
//
// İç içe yapılar ve envPrefix de gezilir. Config bugün DÜZ bir yapıdır, yani
// bu dallar bugün hiç çalışmaz; yine de yazılıdır çünkü ayarlar bir gün
// gruplanırsa (env/v11 bunu destekler) düz bir gezinti o grubun TAMAMINI
// sessizce atlar — ve sessizce atlanan ayar, bu testin engellemek için var
// olduğu şeyin ta kendisidir.
func configAyarlari(t *testing.T) []ayarAlani {
	t.Helper()

	var ayarlar []ayarAlani
	var gez func(tip reflect.Type, onek, yol string)
	gez = func(tip reflect.Type, onek, yol string) {
		for i := range tip.NumField() {
			alan := tip.Field(i)
			if !alan.IsExported() {
				continue
			}
			alanTipi := alan.Type
			for alanTipi.Kind() == reflect.Pointer {
				alanTipi = alanTipi.Elem()
			}

			ad, etiketVar := alan.Tag.Lookup("env")
			if !etiketVar {
				// env etiketi olmayan bir YAPI, ayarları gruplayan bir
				// düğümdür; içine inilir.
				if alanTipi.Kind() == reflect.Struct {
					gez(alanTipi, onek+alan.Tag.Get("envPrefix"), yol+alan.Name+".")
					continue
				}
				// env etiketi olmayan sade bir alan HİÇBİR ortam
				// değişkeninden doldurulamaz. Config'in tek işi ortamı
				// taşımaktır; böyle bir alan ya unutulmuş bir etikettir ya da
				// kimsenin ayarlayamayacağı bir kol — ikisi de arızadır.
				t.Errorf("config.Config.%s%s alanının env etiketi YOK.\n"+
					"Config yalnızca ortamdan doldurulur; etiketsiz alan hiçbir "+
					"ortam değişkeniyle ayarlanamaz ve operatöre görünmez.",
					yol, alan.Name)
				continue
			}

			varsayilan, varsayilanVar := alan.Tag.Lookup("envDefault")
			ayarlar = append(ayarlar, ayarAlani{
				ad:            onek + ad,
				yol:           yol + alan.Name,
				varsayilan:    varsayilan,
				varsayilanVar: varsayilanVar,
			})
		}
	}
	gez(reflect.TypeOf(config.Config{}), "", "")

	require.NotEmpty(t, ayarlar, "config.Config'te hiç env etiketli alan bulunamadı; gezinti bozulmuş olmalı")
	return ayarlar
}

// ortamOrneginiOku .env.example'daki atamaları POSIX kabuk semantiğiyle ayrıştırır.
//
// Dosya "set -a; . ./.env" ile yüklenir, yani bir kabuk betiğidir: '#' ile
// başlayan satır hiçbir şey ATAMAZ ve tırnaklar değere dâhil DEĞİLDİR. Ayrıştırıcı
// bunu taklit eder; etmeseydi yorum içindeki bir örnek (örn. STRIPE_API_KEY)
// gerçek bir ayar sanılırdı.
func ortamOrneginiOku(t *testing.T) map[string]ortamAtamasi {
	t.Helper()

	ham, err := os.ReadFile(filepath.Join(repoRoot, ortamOrnegiYolu))
	require.NoError(t, err, "%s okunamadı", ortamOrnegiYolu)

	atamalar := make(map[string]ortamAtamasi)
	for i, satir := range strings.Split(string(ham), "\n") {
		no := i + 1
		kirp := strings.TrimSpace(satir)
		if kirp == "" || strings.HasPrefix(kirp, "#") {
			continue
		}
		kirp = strings.TrimPrefix(kirp, "export ")

		ad, deger, bulundu := strings.Cut(kirp, "=")
		if !bulundu {
			t.Errorf("%s:%d: %q satırı bir atama değil.\n"+
				"Dosya kabukla yüklenir; atama olmayan bir satır ya sessizce hiçbir şey "+
				"yapar ya da komut olarak ÇALIŞIR.", ortamOrnegiYolu, no, kirp)
			continue
		}
		ad = strings.TrimSpace(ad)

		// Kabuk, tırnaksız bir değerde boşlukla başlayan '#'ten sonrasını yorum
		// sayar; ayrıştırıcı da saymalı ki yorumlu bir satırın değeri yanlış
		// okunmasın.
		deger = strings.TrimSpace(deger)
		if !strings.HasPrefix(deger, "'") && !strings.HasPrefix(deger, `"`) {
			if yorum := strings.Index(deger, " #"); yorum >= 0 {
				deger = strings.TrimSpace(deger[:yorum])
			}
		}
		deger = tirnakSoy(deger)

		if onceki, varmis := atamalar[ad]; varmis {
			t.Errorf("%s:%d: %s İKİ KEZ atanmış (önceki: satır %d).\n"+
				"Kabukta sonuncusu kazanır; belge ise iki değer birden vaat eder. "+
				"Fazlasını silin.", ortamOrnegiYolu, no, ad, onceki.satir)
			continue
		}
		atamalar[ad] = ortamAtamasi{deger: deger, satir: no}
	}

	require.NotEmpty(t, atamalar, "%s'da hiç atama bulunamadı", ortamOrnegiYolu)
	return atamalar
}

// tirnakSoy değeri saran tek katmanlı tırnağı kaldırır.
func tirnakSoy(deger string) string {
	if len(deger) < 2 {
		return deger
	}
	ilk, son := deger[0], deger[len(deger)-1]
	if ilk == son && (ilk == '\'' || ilk == '"') {
		return deger[1 : len(deger)-1]
	}
	return deger
}

// composeDegiskenleri compose dosyasındaki ikameleri adlarına göre döner.
func composeDegiskenleri(t *testing.T) map[string]composeDegisken {
	t.Helper()

	ham, err := os.ReadFile(filepath.Join(repoRoot, composeYolu))
	require.NoError(t, err, "%s okunamadı", composeYolu)

	degiskenler := make(map[string]composeDegisken)
	for _, e := range composeIkameDeseni.FindAllStringSubmatch(string(ham), -1) {
		ad := e[2]
		// Aynı değişken birden çok kez geçebilir (biri yedekli, biri yedeksiz);
		// yedek taşıyan geçiş belirleyicidir.
		if strings.Contains(e[0], ":-") {
			degiskenler[ad] = composeDegisken{varsayilan: e[3], varsayilanVar: true}
			continue
		}
		if _, varmis := degiskenler[ad]; !varmis {
			degiskenler[ad] = composeDegisken{}
		}
	}

	require.NotEmpty(t, degiskenler, "%s'de hiç ${...} ikamesi bulunamadı", composeYolu)
	return degiskenler
}

// eklentiAyarAdlari eklenti kaynağında geçen büyük harfli dize sabitlerini döner.
//
// Test dosyaları DIŞARIDA bırakılır: bir ayar adının yalnızca testte geçmesi,
// üretimde onu okuyan kimse olmadığı anlamına gelir — ve tüketicisi olmayan
// bir ayarı belgelemek, operatöre çalışmayan bir kol vaat etmektir.
func eklentiAyarAdlari(t *testing.T) map[string]bool {
	t.Helper()

	adlar := make(map[string]bool)
	kok := filepath.Join(repoRoot, eklentilerYolu)
	err := filepath.WalkDir(kok, func(yol string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(yol, ".go") || strings.HasSuffix(yol, "_test.go") {
			return nil
		}
		ham, okumaHatasi := os.ReadFile(yol)
		if okumaHatasi != nil {
			return okumaHatasi
		}
		for _, e := range buyukHarfDizeDeseni.FindAllStringSubmatch(string(ham), -1) {
			adlar[e[1]] = true
		}
		return nil
	})
	require.NoError(t, err, "%s taranamadı", eklentilerYolu)
	return adlar
}

// eklentiKayitAdlari eklentilerin PLUGINS listesinde kullanılan adlarını döner.
//
// Adlar kaynaktan OKUNUR, elle yazılmaz: yeni bir eklentinin adı buraya
// eklenmediği için denetimden kaçması, testin yakalaması gereken şeyin ta
// kendisidir.
//
// Aranan şey paket adı ya da dizin adı DEĞİL, "const Name" değeridir; ikisi
// bilinçli olarak ayrışabilir (searchpg paketinin kayıt adı "search-pg"dir)
// ve PLUGINS yalnızca kayıt adını tanır.
func eklentiKayitAdlari(t *testing.T) map[string]bool {
	t.Helper()

	adlar := make(map[string]bool)
	kok := filepath.Join(repoRoot, eklentilerYolu)
	err := filepath.WalkDir(kok, func(yol string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(yol, ".go") || strings.HasSuffix(yol, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		ayrisik, ayristirmaHatasi := parser.ParseFile(fset, yol, nil, 0)
		if ayristirmaHatasi != nil {
			return ayristirmaHatasi
		}
		for _, decl := range ayrisik.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				deger, ok := spec.(*ast.ValueSpec)
				if !ok || len(deger.Names) != 1 || deger.Names[0].Name != "Name" || len(deger.Values) != 1 {
					continue
				}
				if lit, ok := deger.Values[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					adlar[strings.Trim(lit.Value, `"`)] = true
				}
			}
		}
		return nil
	})
	require.NoError(t, err, "%s taranamadı", eklentilerYolu)

	require.NotEmpty(t, adlar, "%s altında hiç eklenti adı bulunamadı", eklentilerYolu)
	return adlar
}

// TestOrtamOrnegiConfigVarsayilanlariylaUyusuyor her ayarın belgesinin ve
// varsayılanının aynı şeyi söylediğini doğrular.
//
// Denetlenen değişmez şudur: config.Config'in env etiketli HER alanı için
// (a) adı .env.example'da geçer, (b) oradaki değer envDefault ile AYNIDIR.
//
// Bu sınıfın bu depoda gerçekleşmiş üç örneği vardı ve üçü de aynı biçimde
// zararlıydı: .env.example "aşağıdaki İKİ sınır" derken sınır yedi taneydi;
// GraphQL'in beş yeni sınırı hiç yazılmamıştı (yani operatör onları
// bilmiyordu bile); handler.go "tavan 64 KiB" derken gerçek tavan 1 MiB'dı.
// Üçünde de kod doğruydu — YANLIŞ OLAN BELGEYDİ, ve belge yanlışken kurulum
// yapan kişi doğru koda rağmen yanlış karar verir.
//
// Ayrışmanın sessizliği asıl meseledir: yanlış bir varsayılan hiçbir testi
// düşürmez, hiçbir log satırı üretmez. Yalnızca operatör, ayarladığını sandığı
// şeyi ayarlamamış olur ve bunu ancak sınır aşıldığında — yani üretimde —
// öğrenir.
func TestOrtamOrnegiConfigVarsayilanlariylaUyusuyor(t *testing.T) {
	t.Parallel()

	ayarlar := configAyarlari(t)
	atamalar := ortamOrneginiOku(t)

	for _, ayar := range ayarlar {
		atama, belgelenmis := atamalar[ayar.ad]
		if !belgelenmis {
			t.Errorf("config.Config.%s ayarı (%s) %s'da YOK.\n"+
				"Belgede yazmayan bir ayar, operatör için var değildir: ne "+
				"varlığını ne varsayılanını öğrenebilir.",
				ayar.yol, ayar.ad, ortamOrnegiYolu)
			continue
		}

		if gerekce, muaf := bilincliAyrilanlar[ayar.ad]; muaf {
			// Muafiyet CANLI olmalı. Ayrışma ortadan kalktığında giriş
			// silinmezse liste, gerçek bir ayrışmayı da örtecek biçimde
			// büyür.
			assert.NotEqual(t, ayar.varsayilan, atama.deger,
				"%s için bilinçli ayrışma kaydı var (%q) ama değer artık varsayılanla AYNI (%q).\n"+
					"Kaydı bilincliAyrilanlar'dan SİLİN; çürümüş bir muafiyet, "+
					"yarınki gerçek ayrışmayı sessizce geçirir.",
				ayar.ad, gerekce, ayar.varsayilan)
			continue
		}

		if !ayar.varsayilanVar {
			assert.Equal(t, zorunluAyarOrnekDegeri, atama.deger,
				"%s:%d: %s ayarının envDefault'u YOK, yani zorunlu bir ayardır; "+
					"%s'da değeri BOŞ olmalı, %q yazılmış.\n"+
					"Bu dosya olduğu gibi .env'e kopyalanır: buraya yazılan örnek bir "+
					"sır, çalışan ama herkesin bildiği bir sırla imzalayan bir kurulum "+
					"üretir. Örnek değerin yeri yorum satırıdır.",
				ortamOrnegiYolu, atama.satir, ayar.ad, ortamOrnegiYolu, atama.deger)
			continue
		}

		assert.Equal(t, ayar.varsayilan, atama.deger,
			"%s:%d: %s ayarı belgede %q, config.Config.%s'in varsayılanı ise %q.\n"+
				"Belge ile varsayılan aynı şeyi söylemeli. Ayrışma bilinçliyse "+
				"bilincliAyrilanlar'a GEREKÇESİYLE ekleyin; sessiz ayrışma, "+
				"operatörün yanlış öğrenmesidir.",
			ortamOrnegiYolu, atama.satir, ayar.ad, atama.deger, ayar.yol, ayar.varsayilan)
	}
}

// TestOrtamOrnegindeSahipsizDegiskenYok belgedeki her değişkenin bir TÜKETİCİSİ
// olduğunu doğrular.
//
// Ters yön de en az ilki kadar önemlidir: silinmiş bir ayarın belgede kalması,
// operatöre ÇALIŞMAYAN bir kol vaat etmektir. O kolu çeviren kişi bir şey
// ayarladığını sanır, hiçbir hata görmez ve davranış değişmez — bulunması en
// pahalı arıza türü budur.
//
// Meşru üç tüketici vardır ve üçü de KAYNAKTAN doğrulanır, elle yazılmış bir
// izin listesinden değil:
//
//  1. config.Config alanı — uygulamanın kendi ayarı.
//  2. deploy/docker-compose.yml ikamesi — yerel yığının ayarı (Postgres
//     portu, Redis parolası). Bunlar uygulamaya HİÇ ulaşmaz ama .env'i
//     compose da okur, yani belgede yerleri vardır.
//  3. plugins/ altındaki bir ayar adı — eklenti ayarı (STRIPE_API_KEY).
//     Çekirdek eklentileri tanımaz (Prensip 2.4), bu yüzden bu adların
//     Config'te karşılığı YOKTUR ve olmamalıdır.
//
// Compose değişkenleri için değer de karşılaştırılır: compose'un kendi
// yedeği ${AD:-varsayılan} biçiminde yazılıdır ve belge ondan ayrışırsa
// "make up" ile açılan yığın, belgenin anlattığından başka bir yerde dinler.
func TestOrtamOrnegindeSahipsizDegiskenYok(t *testing.T) {
	t.Parallel()

	atamalar := ortamOrneginiOku(t)
	ikameler := composeDegiskenleri(t)
	eklentiAdlari := eklentiAyarAdlari(t)

	configAdlari := make(map[string]bool)
	for _, ayar := range configAyarlari(t) {
		configAdlari[ayar.ad] = true
	}

	for ad, atama := range atamalar {
		switch {
		case configAdlari[ad]:
			// İlk testin kapsamında; değeri orada karşılaştırılır.
		case ikameler[ad].varsayilanVar:
			assert.Equal(t, ikameler[ad].varsayilan, atama.deger,
				"%s:%d: %s belgede %q, %s'deki yedeği ise %q.\n"+
					"İkisi ayrışırsa \"make up\" ile açılan yığın, belgenin "+
					"anlattığından başka bir yapılandırmayla çalışır.",
				ortamOrnegiYolu, atama.satir, ad, atama.deger, composeYolu,
				ikameler[ad].varsayilan)
		case eklentiAdlari[ad]:
			// Eklenti ayarı; değeri eklentinin kendi sözleşmesine aittir.
		default:
			t.Errorf("%s:%d: %s değişkenini OKUYAN kimse yok.\n"+
				"Ne config.Config'te bir alanı, ne %s'de bir ikamesi, ne de %s "+
				"altında bir okuyucusu var. Belgede duran ama kimsenin okumadığı "+
				"bir değişken, operatöre çalışmayan bir kol vaat eder: çevirir, "+
				"hiçbir şey olmaz, hata da almaz.",
				ortamOrnegiYolu, atama.satir, ad, composeYolu, eklentilerYolu)
		}
	}
}

// TestComposeDegiskenleriBelgelenmis compose'un okuduğu her değişkenin
// .env.example'da yazdığını doğrular.
//
// Bu, ilk testin compose tarafındaki karşılığıdır ve aynı sessiz arızayı
// kapatır: .env.example zaten POSTGRES_PORT ve REDIS_PORT'u belgeliyor, yani
// dosyanın kendi konvansiyonu "compose değişkenleri de burada yazar"dır.
// Dördünü yazıp ikisini yazmamak, o ikisini AYARLANAMAZ sanan bir operatör
// üretir — port çakışması yaşayan kişi çareyi compose dosyasını düzenlemekte,
// yani depoyu çatallamakta bulur.
func TestComposeDegiskenleriBelgelenmis(t *testing.T) {
	t.Parallel()

	degiskenler := composeDegiskenleri(t)
	atamalar := ortamOrneginiOku(t)

	for ad := range degiskenler {
		if _, belgelenmis := atamalar[ad]; !belgelenmis {
			t.Errorf("%s %s'de okunuyor ama %s'da YOK.\n"+
				"Belgelenmemiş bir compose değişkeni, operatörün ayarlanamaz sandığı "+
				"bir ayardır.", ad, composeYolu, ortamOrnegiYolu)
		}
	}
}

// TestBelgelerdekiEklentiAdlariGercek README ve .env.example'ın adıyla andığı
// her eklentinin GERÇEKTEN o adla kayıtlı olduğunu doğrular.
//
// Eklenti adı iki yerde ayrışabilir ve ayrışması ÖLÇÜLDÜ: paket ve dizin adı
// "searchpg", kayıt adı ise "search-pg"dir. PLUGINS yalnızca kayıt adını
// tanır ve tanımadığı adda uygulama AÇILMAZ — yani belgeden kopyalanan
// yanlış bir ad, kurulumun ilk denemesinde açılışı durdurur.
//
// İki yön de denetlenir. İleri yön: belgedeki her PLUGINS=... adı kayıtlı
// olmalı. Geri yön: kayıtlı her eklenti .env.example'da ANILMALI — yazılmış
// ama hiçbir yerde duyurulmamış bir eklenti, tüketicisi olmayan bir
// yetenektir (bu depoda Faz 8/9'un tamamı bir kez böyle yazılıp hiç mount
// edilmemişti).
func TestBelgelerdekiEklentiAdlariGercek(t *testing.T) {
	t.Parallel()

	kayitli := eklentiKayitAdlari(t)

	readme, err := os.ReadFile(filepath.Join(repoRoot, readmeYolu))
	require.NoError(t, err, "%s okunamadı", readmeYolu)
	ortamOrnegi, err := os.ReadFile(filepath.Join(repoRoot, ortamOrnegiYolu))
	require.NoError(t, err, "%s okunamadı", ortamOrnegiYolu)

	for _, belge := range []struct {
		ad     string
		icerik string
	}{
		{readmeYolu, string(readme)},
		{ortamOrnegiYolu, string(ortamOrnegi)},
	} {
		for _, e := range pluginsAtamaDeseni.FindAllStringSubmatch(belge.icerik, -1) {
			for _, ad := range strings.Split(e[1], ",") {
				assert.Contains(t, kayitli, ad,
					"%s: PLUGINS=%s örneğindeki %q adı kayıtlı DEĞİL.\n"+
						"Kayıtlı adlar eklenti kaynağındaki \"const Name\" değerleridir; "+
						"paket ya da dizin adı DEĞİLDİR. Bu örneği kopyalayan kurulum "+
						"açılışta \"bilinmeyen eklenti\" hatasıyla durur.",
					belge.ad, e[1], ad)
			}
		}
	}

	for ad := range kayitli {
		assert.Contains(t, string(ortamOrnegi), ad,
			"%q eklentisi kayıtlı ama %s'da hiç ANILMIYOR.\n"+
				"PLUGINS bölümü tanınan adları sayar; orada yazmayan bir eklenti, "+
				"kimsenin kuramayacağı bir yetenektir.", ad, ortamOrnegiYolu)
	}
}

// belgelerdekiTestAdi README'de anılan bir Go test adını yakalar.
//
// Kalıp bilinçli olarak DAR: yalnızca "Test" ile başlayıp büyük harfle devam
// eden, en az iki büyük harf parçası olan adlar. Daha gevşek bir kalıp
// "Testler" gibi Türkçe kelimeleri de yakalar ve test kendi gürültüsünde
// boğulurdu.
var belgelerdekiTestAdi = regexp.MustCompile(`\bTest[A-Z][A-Za-z0-9]*[A-Z][A-Za-z0-9]*\b`)

// TestBelgelerdeAnilanTestlerGercek README'de adı geçen her Go testinin
// depoda GERÇEKTEN var olduğunu doğrular.
//
// # Neden var
//
// README, mimari değişmezleri bir tabloda test ADIYLA sayar: okuyucu "bu kural
// nerede zorlanıyor" sorusunun cevabını oradan alır. Test yeniden adlandırılır
// ya da silinirse tablo sessizce YALAN söylemeye başlar — kural zorlanıyormuş
// gibi görünür, zorlanmaz. Bu, bu depodaki C sınıfı arızanın ta kendisidir
// (godoc'un vaadi ile kodun davranışı ayrışır) ve tablo elle doğrulanarak
// yazıldığı için tam olarak çürümeye açıktı.
//
// # Neden yalnızca README
//
// CHANGELOG geçmişi anlatır ve geçmişteki bir testin bugün var olmaması
// NORMALDİR: kaldırılan bir değişmezin kaydı, kaydın kendisidir. README ise
// BUGÜNÜ anlatır, bu yüzden denetlenen odur.
func TestBelgelerdeAnilanTestlerGercek(t *testing.T) {
	t.Parallel()

	ham, err := os.ReadFile("../../README.md")
	require.NoError(t, err, "README.md okunamadı")

	anilanlar := belgelerdekiTestAdi.FindAllString(string(ham), -1)
	require.NotEmpty(t, anilanlar,
		"README'de hiç test adı bulunamadı; kalıp bozulmuş olabilir — "+
			"hiçbir şey bulamayan bir denetim, boşlukta yeşil kalır")

	mevcut := depodakiTestAdlari(t)

	gorulen := map[string]bool{}
	for _, ad := range anilanlar {
		if gorulen[ad] {
			continue
		}
		gorulen[ad] = true

		if _, var_ := mevcut[ad]; var_ {
			continue
		}
		// assert.Contains BİLİNÇLİ olarak kullanılmıyor: harita üyeliğinde
		// başarısız olduğunda haritanın TAMAMINI (binlerce test adı, ~32 KB)
		// basar ve asıl mesajı boğar. Denetimin değeri, düştüğünde okunabilir
		// olmasındadır.
		t.Errorf(
			"README %q testinden bahsediyor ama depoda böyle bir test YOK.\n"+
				"Tablo, kuralın nerede zorlandığını söyler; var olmayan bir teste "+
				"işaret etmesi, zorlanmayan bir kuralı zorlanıyor gibi gösterir.\n"+
				"Test yeniden adlandırıldıysa README'yi de güncelleyin; "+
				"kaldırıldıysa satırı silin.", ad)
	}
}

// depodakiTestAdlari depodaki tüm Go test fonksiyonlarının adlarını toplar.
//
// Derleme etiketleri UMURSANMAZ: ayrıştırma etiketten bağımsızdır ve
// integration/smoke etiketli testler de README'de anılabilir. Etiketlere
// bakılsaydı denetim, etiketsiz koşuda o adları "yok" sayardı.
func depodakiTestAdlari(t *testing.T) map[string]struct{} {
	t.Helper()

	adlar := map[string]struct{}{}
	fset := token.NewFileSet()

	err := filepath.WalkDir("../..", func(yol string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// bin/ derlenmiş araçları, .git/ ise nesne deposunu tutar; ikisi de
			// kaynak değildir ve gezilmeleri yalnızca zaman harcar. testdata/
			// ise Go araçlarının GEZMEDİĞİ dizindir ve içinde bilinçli olarak
			// ayrıştırılamayan dosyalar bulunabilir.
			if ad := d.Name(); ad == ".git" || ad == "bin" || ad == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(yol, "_test.go") {
			return nil
		}

		// Ayrıştırma hatası YUTULMAZ. Yutulsaydı, bozuk tek bir dosyadaki
		// testler sessizce "yok" sayılır ve README onlara işaret ettiği hâlde
		// denetim yeşil kalırdı — yani denetimin kendisi, kapatmaya çalıştığı
		// sınıfın bir örneği olurdu.
		dosya, perr := parser.ParseFile(fset, yol, nil, 0)
		if perr != nil {
			return perr
		}
		for _, tanim := range dosya.Decls {
			fn, ok := tanim.(*ast.FuncDecl)
			if ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Test") {
				adlar[fn.Name.Name] = struct{}{}
			}
		}
		return nil
	})
	require.NoError(t, err, "depo gezilemedi")
	require.NotEmpty(t, adlar, "depoda hiç test bulunamadı")

	return adlar
}
