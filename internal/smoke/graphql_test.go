//go:build smoke

package smoke

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/product/graph"
)

// GraphQL sertleştirme sınırlarının senaryodaki DÜŞÜK değerleri.
//
// Değerler varsayılanların (10.000/20/2/15/4 MiB) çok altındadır ve bu,
// senaryonun tek çalışma koşuludur: varsayılanla koşan bir süreçte hiçbir
// belge sınıra çarpmaz, yani "ayar bağlı mı" sorusu CEVAPSIZ kalır. Ortam
// değişkenini düşürüp reddi görmek, bağın gerçekten tuttuğunun tek kanıtıdır.
//
// Beş değer TEK süreçte birlikte verilir ve birbirini gölgelemeyecek biçimde
// kalibre edilmiştir; hangi belgenin hangi kapıya çarptığı alt testlerin
// yorumlarındadır. Ayrı süreçler de aynı şeyi kanıtlardı ama beş kez açılış +
// beş kez migration bedeliyle.
const (
	// grafikSecimSiniri fragment'lar açıldıktan sonraki seçim sayısı tavanıdır.
	// Senaryonun en büyük MEŞRU belgesi 6 seçimdir; 8, ona pay bırakır.
	grafikSecimSiniri = 8
	// grafikAlanTekrariSiniri aynı alanın aynı nesne altındaki tekrar tavanıdır.
	grafikAlanTekrariSiniri = 2
	// grafikIcGozlemKokSiniri bir belgedeki __schema/__type kökü tavanıdır.
	grafikIcGozlemKokSiniri = 1
	// grafikIcGozlemDerinlikSiniri iç gözlem alt ağacının derinlik tavanıdır.
	// Üçtür: "__schema { types { name } }" tam olarak 3 derindir ve GEÇMELİDİR;
	// yanıt baytı senaryosu tam da o belgeyi çalıştırır.
	grafikIcGozlemDerinlikSiniri = 3
	// grafikYanitBaytSiniri tek bir yanıtın bayt tavanıdır.
	//
	// 2 KiB, senaryonun tüm meşru yanıtlarının (tek ürünlük liste ~150 bayt) ve
	// tüm hata zarflarının (~200 bayt) üstünde, şemanın açıklamalı iç gözlem
	// dökümünün ise çok altındadır — yani yalnızca sınanmak istenen belgeyi
	// keser.
	grafikYanitBaytSiniri = 2048
)

// grafikYanit GraphQL yanıt zarfının senaryoların okuduğu kadarıdır.
//
// data HAM bırakılır: her alt test kendi belgesinin şeklini bekler ve tek bir
// Go tipine bağlamak, iki farklı sorgunun aynı yapıyı döndürmesini zorunlu
// kılardı.
type grafikYanit struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message    string         `json:"message"`
		Extensions map[string]any `json:"extensions"`
	} `json:"errors"`
}

// grafikIste GraphQL ucuna bir belge gönderir.
//
// Yol [graph.Path] sabitinden okunur: ucu bağlayan ve anlatan yerler de aynı
// sabiti paylaşır. Elle yazılsaydı, yol değiştiğinde senaryo var olmayan bir
// uca 404 alır ve bunu "koruma çalışıyor" sanabilirdi.
func (s *surec) grafikIste(anahtar, belge string) (kod int, yanit string) {
	s.t.Helper()

	return s.vitrinIste(http.MethodPost, graph.Path, anahtar, map[string]string{"query": belge})
}

// grafikCoz yanıt gövdesini GraphQL zarfına çözer.
func grafikCoz(t *testing.T, govde string) grafikYanit {
	t.Helper()

	var yanit grafikYanit
	require.NoError(t, json.Unmarshal([]byte(govde), &yanit),
		"GraphQL yanıtı çözülemedi; gövde: %s", govde)

	return yanit
}

// grafikHatasi belgenin TEK bir hatayla reddedildiğini doğrular ve o hatayı
// döner.
//
// Durum kodu 200 BEKLENİR ve bu bir gevşeklik değil, ucun sözleşmesidir: sınır
// kodları errcode.RegisterErrorType ile kaydedilmez (gerekçe graph/limits.go),
// yani protokol hatası gövdedeki errors dizisindedir. 200 dışında bir kod,
// isteğin sınıra değil BAŞKA bir şeye — koruma yığınına, yönlendirmeye —
// takıldığını gösterirdi ve senaryo sınamak istediği şeyi hiç sınamamış olurdu.
func grafikHatasi(t *testing.T, kod int, govde string) (mesaj, hataKodu string) {
	t.Helper()

	require.Equal(t, http.StatusOK, kod,
		"sınır aşımı GraphQL zarfıyla bildirilmeli (200 + errors); gövde: %s", govde)

	yanit := grafikCoz(t, govde)
	require.Len(t, yanit.Errors, 1, "tek bir sınır hatası beklenir; gövde: %s", govde)

	kodDegeri, _ := yanit.Errors[0].Extensions["code"].(string)

	return yanit.Errors[0].Message, kodDegeri
}

// grafikAyarlari GraphQL senaryosunun süreç ortamını kurar.
//
// Sınırlar ORTAM DEĞİŞKENİYLE verilir, kodla değil: sınanan şey tam olarak
// "operatörün yazdığı değer graph.Options'a ulaşıyor mu" sorusudur ve o zincir
// (config etiketi → cmd/server kablolaması → modül seçeneği) yalnızca gerçek
// süreçte tamdır.
func grafikAyarlari(dsn string, port int) ayarlar {
	ayar := temelAyarlar(dsn, port)
	ayar["ADMIN_BOOTSTRAP_EMAIL"] = tohumEposta
	ayar["ADMIN_BOOTSTRAP_PASSWORD"] = tohumParola

	ayar["GRAPHQL_MAX_SELECTIONS"] = strconv.Itoa(grafikSecimSiniri)
	ayar["GRAPHQL_MAX_FIELD_REPETITION"] = strconv.Itoa(grafikAlanTekrariSiniri)
	ayar["GRAPHQL_MAX_INTROSPECTION_ROOTS"] = strconv.Itoa(grafikIcGozlemKokSiniri)
	ayar["GRAPHQL_MAX_INTROSPECTION_DEPTH"] = strconv.Itoa(grafikIcGozlemDerinlikSiniri)
	ayar["GRAPHQL_MAX_RESPONSE_BYTES"] = strconv.Itoa(grafikYanitBaytSiniri)

	return ayar
}

// vitrinUrunuYayinla yayında bir ürün açar ve satış kanalına bağlar.
//
// İKİ adım da zorunludur: vitrin yalnızca "published" ürünleri, yalnızca
// isteğin kanalında görünenleri döner. Biri atlanırsa GraphQL sorgusu boş bir
// liste döner ve senaryo, hiçbir şey kanıtlamadan yeşil kalırdı.
func vitrinUrunuYayinla(t *testing.T, s *surec, jeton, kanalID, baslik, handle string) {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/products", jeton, map[string]any{
		"handle": handle,
		"title":  baslik,
		"status": "published",
	})
	require.Equal(t, http.StatusCreated, kod, "ürün açılamadı; gövde: %s", govde)

	urun := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde)
	require.NotEmpty(t, urun.ID, "ürün kimlik dönmeli; gövde: %s", govde)

	kod, govde = s.yonetimIste(http.MethodPost, "/admin/v1/products/"+urun.ID+"/sales-channels",
		jeton, map[string]any{"sales_channel_id": kanalID})
	require.Equal(t, http.StatusOK, kod, "ürün satış kanalına bağlanamadı; gövde: %s", govde)
}

// TestGraphQLVitrinYuzeyiGercekSurecte E senaryosudur: GraphQL okuma yüzeyi
// gerçek binary üzerinde, gerçek koruma yığınının arkasında çalışır ve beş YENİ
// sertleştirme ayarı gerçekten graph.Options'a ulaşır.
//
// # Neden gerçek süreç
//
// internal/e2e bu ucu httptest ile sürer ve yeşil bulur, ama router'ı KENDİ
// kurar: bileşim kökündeki kablolamayı (cmd/server'ın product.Options'ı),
// config ayrıştırmasını, açılıştaki migration'ları, eklenti yüklemesini ve
// gerçek ağı atlar. Yani "depoyu klonlayıp çalıştıran birinin vitrini GraphQL'e
// cevap veriyor mu" sorusunu cevaplayamaz.
//
// Sertleştirme tarafında boşluk daha da somuttur: beş ayar config'ten
// graph.Options'a YENİ bağlandı ve bağın tuttuğu HİÇ sınanmadı. Birim testleri
// kapıların davranışını kanıtlar, ama ayarın oraya ULAŞTIĞINI kanıtlayamaz —
// cmd/server'daki satır silinse hepsi yeşil kalır ve kurulum, belgede yazandan
// başka bir sınırla çalışır.
//
// # TEK süreç
//
// Alt testlerin tamamı aynı süreci sürer. Sertleştirme değerleri süreç ömrü
// boyunca sabit olduğu için her ayara bir süreç açmak beş açılış + beş
// migration demekti; değerler bunun yerine birbirini gölgelemeyecek biçimde
// kalibre edildi (bkz. [grafikSecimSiniri] ve kardeşleri). Kapı sırası
// (seçim bütçesi → derinlik → iç gözlem kökü → alan tekrarı → karmaşıklık →
// yanıt baytı) graph.NewHandler'da yazılıdır ve belgeler o sıraya göre
// seçilmiştir.
func TestGraphQLVitrinYuzeyiGercekSurecte(t *testing.T) {
	s := sunucuBaslat(t, grafikAyarlari(senaryoVeritabani(t), bosPort(t)))
	s.hazirBekle(acilisSuresi)

	jeton, kanalID, vitrinAnahtari := yonetimZeminiKur(t, s, "Smoke GraphQL Kanalı")

	const urunBasligi = "Smoke GraphQL Ürünü"
	vitrinUrunuYayinla(t, s, jeton, kanalID, urunBasligi, "smoke-graphql-urunu")

	t.Run("anahtarsız istek 401 döner", func(t *testing.T) {
		kod, govde := s.grafikIste("", "{ products(limit: 1) { count } }")

		assert.Equal(t, http.StatusUnauthorized, kod,
			"publishable anahtarsız GraphQL isteği reddedilmeli; gövde: %s", govde)
		// Yanıt GraphQL zarfı DEĞİL, çekirdeğin hata zarfı olmalıdır: koruma
		// çalıştırıcıya ULAŞMADAN keser ve /store/v1 altındaki her uçla aynı
		// biçimi döndürür. Gövdede "data" görmek, isteğin korumayı geçip
		// gqlgen'e ulaştığı anlamına gelirdi.
		assert.NotContains(t, govde, `"data"`,
			"kimliksiz istek çalıştırıcıya ulaşmamalı; gövde: %s", govde)
	})

	t.Run("GET kabul edilmez", func(t *testing.T) {
		// Uç chi'ye yalnızca POST ile kaydedilir (bkz. graph.NewHandler): GET
		// isteği gqlgen'in "transport not supported" 400'ü yerine dürüst bir
		// 405 almalıdır. İddia ancak gerçek router'da sınanabilir.
		kod, govde := s.vitrinIste(http.MethodGet, graph.Path, vitrinAnahtari, nil)

		assert.Equal(t, http.StatusMethodNotAllowed, kod,
			"GraphQL ucu yalnızca POST kabul etmeli; gövde: %s", govde)
	})

	t.Run("products sorgusu kanalın katalogunu döner", func(t *testing.T) {
		kod, govde := s.grafikIste(vitrinAnahtari,
			"{ products(limit: 5) { count items { id title handle } } }")
		require.Equal(t, http.StatusOK, kod, "products sorgusu 200 dönmeli; gövde: %s", govde)

		yanit := grafikCoz(t, govde)
		require.Empty(t, yanit.Errors, "meşru sorgu hatasız çalışmalı; gövde: %s", govde)

		var veri struct {
			Products struct {
				Count int `json:"count"`
				Items []struct {
					ID     string `json:"id"`
					Title  string `json:"title"`
					Handle string `json:"handle"`
				} `json:"items"`
			} `json:"products"`
		}
		require.NoError(t, json.Unmarshal(yanit.Data, &veri),
			"products verisi çözülemedi; gövde: %s", govde)

		require.Equal(t, 1, veri.Products.Count,
			"kanala bağlanan tek ürün sayılmalı; gövde: %s", govde)
		require.Len(t, veri.Products.Items, 1, "sayfa tek ürün taşımalı; gövde: %s", govde)
		assert.Equal(t, urunBasligi, veri.Products.Items[0].Title,
			"dönen ürün yönetim ucundan açılan ürün olmalı; gövde: %s", govde)
	})

	// Aşağıdaki alt testlerin her biri YALNIZCA sınadığı kapıya çarpan bir
	// belge gönderir. Kapıların DAVRANIŞI graph paketinin birim testlerinde çok
	// daha ucuza sınanmıştır ve burada tekrarlanmaz; burada sınanan tek şey
	// BAĞDIR — ortam değişkeni → config alanı → cmd/server → graph.Options.
	t.Run("GRAPHQL_MAX_SELECTIONS süreçte bağlı", func(t *testing.T) {
		// On seçim; bütçe sekiz. Kapı hepsinden ÖNCE koştuğu için belgenin
		// başka bir sınıra çarpması mümkün değildir.
		kod, govde := s.grafikIste(vitrinAnahtari,
			"{ products { count offset limit items { id handle title createdAt updatedAt } } }")

		mesaj, hataKodu := grafikHatasi(t, kod, govde)
		assert.Equal(t, "SELECTION_BUDGET_EXCEEDED", hataKodu,
			"seçim bütçesi ortam değişkeninden bağlanmalı; gövde: %s", govde)
		assert.Contains(t, mesaj, strconv.Itoa(grafikSecimSiniri),
			"mesaj UYGULANAN sınırı söylemeli; varsayılan (10000) görünürse ortam "+
				"değişkeni graph.Options'a hiç ulaşmamış demektir; gövde: %s", govde)
	})

	t.Run("GRAPHQL_MAX_FIELD_REPETITION süreçte bağlı", func(t *testing.T) {
		// Dört seçim (bütçenin altında), aynı nesne altında üç kez "count";
		// tavan iki. Takma adlar sayımda yok sayılır, yani üçü de aynı çifttir.
		kod, govde := s.grafikIste(vitrinAnahtari,
			"{ products(limit: 1) { count a: count b: count } }")

		mesaj, hataKodu := grafikHatasi(t, kod, govde)
		assert.Equal(t, "FIELD_REPETITION_LIMIT_EXCEEDED", hataKodu,
			"alan tekrarı sınırı ortam değişkeninden bağlanmalı; gövde: %s", govde)
		assert.Contains(t, mesaj, strconv.Itoa(grafikAlanTekrariSiniri),
			"mesaj UYGULANAN sınırı söylemeli; varsayılan (20) görünürse ortam "+
				"değişkeni graph.Options'a hiç ulaşmamış demektir; gövde: %s", govde)
	})

	t.Run("GRAPHQL_MAX_INTROSPECTION_DEPTH süreçte bağlı", func(t *testing.T) {
		// Dört seviye derin TEK bir iç gözlem kökü: kök sayısı sınırın (1)
		// altındadır, yani belgeyi reddeden şey yalnızca DERİNLİK olabilir. İki
		// kapı aynı hata kodunu paylaştığı için ayrımı mesaj yapar.
		kod, govde := s.grafikIste(vitrinAnahtari, "{ __schema { queryType { fields { name } } } }")

		mesaj, hataKodu := grafikHatasi(t, kod, govde)
		assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", hataKodu,
			"iç gözlem derinliği ortam değişkeninden bağlanmalı; gövde: %s", govde)
		assert.Contains(t, mesaj, "depth",
			"reddin sebebi DERİNLİK olmalı; kök sayısı mesajı görünürse belge "+
				"sınanmak istenen kapıya hiç çarpmamış demektir; gövde: %s", govde)
		assert.Contains(t, mesaj, strconv.Itoa(grafikIcGozlemDerinlikSiniri),
			"mesaj UYGULANAN sınırı söylemeli; varsayılan (15) görünürse ortam "+
				"değişkeni graph.Options'a hiç ulaşmamış demektir; gövde: %s", govde)
	})

	t.Run("GRAPHQL_MAX_INTROSPECTION_ROOTS süreçte bağlı", func(t *testing.T) {
		// İki kök, ikisi de derinlik tavanının altında: reddin sebebi yalnızca
		// KÖK SAYISI olabilir.
		kod, govde := s.grafikIste(vitrinAnahtari,
			`{ __schema { queryType { name } } t: __type(name: "Product") { name } }`)

		mesaj, hataKodu := grafikHatasi(t, kod, govde)
		assert.Equal(t, "INTROSPECTION_LIMIT_EXCEEDED", hataKodu,
			"iç gözlem kökü sınırı ortam değişkeninden bağlanmalı; gövde: %s", govde)
		assert.Contains(t, mesaj, "introspection roots",
			"reddin sebebi KÖK SAYISI olmalı; derinlik mesajı görünürse belge "+
				"sınanmak istenen kapıya hiç çarpmamış demektir; gövde: %s", govde)
		assert.Contains(t, mesaj, strconv.Itoa(grafikIcGozlemKokSiniri),
			"mesaj UYGULANAN sınırı söylemeli; varsayılan (2) görünürse ortam "+
				"değişkeni graph.Options'a hiç ulaşmamış demektir; gövde: %s", govde)
	})

	t.Run("GRAPHQL_MAX_RESPONSE_BYTES süreçte bağlı", func(t *testing.T) {
		// Belge tüm ÖN kapılardan geçer (4 seçim, 3 derinlik, 1 kök, tekrarsız)
		// ve ÇALIŞIR; onu kesen tek şey gerçekleşen bayttır. Şemanın tip
		// açıklamaları tek başına 2 KiB'ın kat kat üstündedir.
		kod, govde := s.grafikIste(vitrinAnahtari, "{ __schema { types { name description } } }")

		mesaj, hataKodu := grafikHatasi(t, kod, govde)
		assert.Equal(t, "RESPONSE_LIMIT_EXCEEDED", hataKodu,
			"yanıt bayt sınırı ortam değişkeninden bağlanmalı; gövde: %s", govde)
		assert.Contains(t, mesaj, strconv.Itoa(grafikYanitBaytSiniri),
			"mesaj UYGULANAN sınırı söylemeli; varsayılan (4194304) görünürse ortam "+
				"değişkeni graph.Options'a hiç ulaşmamış demektir; gövde: %s", govde)

		// Yarım JSON GÖNDERİLMEZ: aşan gövde atılır, yerine tam ve geçerli bir
		// hata zarfı yazılır. grafikHatasi'nın gövdeyi çözebilmiş olması bunun
		// kanıtıdır; buradaki iddia sürecin de ayakta kaldığını söyler.
		assert.False(t, s.oldu(), "yanıt sınırı süreci düşürmemeli\n%s", s.gunluk())
	})
}

// TestGraphQLSinirlariSifirVeNegatifDegerdeAcilisiDurdurur sertleştirmenin
// SESSİZCE kapanamayacağını kanıtlar.
//
// # Neden ayrı bir iddia
//
// "0 = sınırsız" okuması bu ayarların hiçbirinde YOKTUR ve olmaması bilinçli
// bir karardır (bkz. config.Config.GraphQLMaxDepth): sınır yükseltilebilir,
// kaldırılamaz. Ama karar bir kod satırıdır ve o satır silinirse hiçbir birim
// testi düşmez — olan şey, kurulumun korumasız bir uçla açılıp bunu hiç
// söylememesidir. Senaryo tam olarak o sessizliği kapatır.
//
// # Neden beş süreç ve neden ucuz
//
// config.Load ilk hatada döner, yani tek bir süreçte tek bir ayar sınanabilir.
// Bedeli düşüktür: kapı veritabanına HİÇ dokunmadan önce kapanır ve süreç
// milisaniyeler içinde ölür; beşinin toplamı tek bir normal açılışın çok
// altındadır.
//
// Her alt test yine de KENDİ veritabanını alır. Sebep, kapının bir gün
// kalkması ihtimalidir: yönetim veritabanı paylaşılsaydı, açılmayı başaran bir
// süreç oraya migration uygular ve arızayı komşu senaryolara taşırdı.
func TestGraphQLSinirlariSifirVeNegatifDegerdeAcilisiDurdurur(t *testing.T) {
	// Sıfır ve negatif değerler değişkenler arasında PAYLAŞTIRILDI: ikisini de
	// her değişkende sınamak süreç sayısını ikiye katlar ve aynı kod dalını
	// (deger < 1) onuncu kez ölçerdi.
	durumlar := map[string]struct {
		degisken string
		deger    string
	}{
		"seçim bütçesi sıfır":         {"GRAPHQL_MAX_SELECTIONS", "0"},
		"alan tekrarı negatif":        {"GRAPHQL_MAX_FIELD_REPETITION", "-1"},
		"iç gözlem kökü sıfır":        {"GRAPHQL_MAX_INTROSPECTION_ROOTS", "0"},
		"iç gözlem derinliği negatif": {"GRAPHQL_MAX_INTROSPECTION_DEPTH", "-5"},
		"yanıt baytı sıfır":           {"GRAPHQL_MAX_RESPONSE_BYTES", "0"},
	}

	for ad, durum := range durumlar {
		t.Run(ad, func(t *testing.T) {
			ayar := temelAyarlar(senaryoVeritabani(t), bosPort(t))
			ayar[durum.degisken] = durum.deger

			kod, stderr := acilistaDurmali(t, ayar, acilisSuresi)

			assert.NotZero(t, kod,
				"geçersiz sınır sıfırdan farklı çıkış kodu vermeli; stderr:\n%s", stderr)
			assert.Contains(t, stderr, durum.degisken,
				"stderr operatöre HANGİ ayarı düzelteceğini söylemeli; stderr:\n%s", stderr)
			assert.Contains(t, stderr, "en az 1 olmalı",
				"mesaj sınırın kaldırılamayacağını söylemeli; stderr:\n%s", stderr)
		})
	}
}
