// Package openapi çalışan router'dan OpenAPI 3.1 şeması üretir.
//
// # Neden router'dan
//
// Elle yazılan bir şema kaçınılmaz olarak koddan ayrışır: bir route silinir,
// şemada kalır; bir yol değişir, şemada eski hâliyle durur. Burada üretilen
// yollar chi'nin GERÇEK route ağacından okunur, yani sunucunun o an
// servis ettiği şeyle her zaman aynıdır.
//
// # Gövde şemaları da türetilir
//
// Yol ve metod router'dan okunduğu gibi, istek/yanıt GÖVDELERİ de Go
// TİPLERİNDEN yansımayla türetilir (bkz. [Doc.SchemaOf]). Gerekçe aynıdır:
// elle yazılmış bir alan listesi, DTO'ya alan eklendiği gün eksik kalır ve
// kimse fark etmez.
//
// Çalışma zamanı bir handler'ın hangi tipi okuyup hangi tipi yazdığını
// BİLEMEZ; bu bağı modül kurar. Modül kendi uçlarını [Describer] arayüzüyle
// anlatır, [Doc.Describe] ile route'a bağlar ve gövde şemasını [Doc.Item],
// [Doc.List], [Doc.RequestBody] ile TİPTEN üretir.
//
// # Neyi kapsamaz
//
// Anlatılmamış bir uç yalnızca yol, metod, güvenlik ve ortak hata yanıtlarını
// taşır; gövdesi olmaz. Kapsam sınırının açıkça yazılması bilinçlidir:
// "OpenAPI üretiyoruz" deyip gövdesiz bir şema sunmak, istemci
// geliştiricisinin şemaya güvenip yanlış alan adları göndermesine yol açardı.
// Eksik olduğunu bilmek, eksik olduğunu sanmamaktan iyidir.
package openapi

import (
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Version üretilen belgenin OpenAPI sürümüdür.
const Version = "3.1.0"

// codeDocumentUnavailable belgenin üretilemediğini bildirir.
//
// İstemciye giden TEK ayrıntı budur: üretim hatasının metni çakışan tiplerin
// PAKET YOLLARINI taşır (bkz. [Doc.cakismaBildir]) ve bu uç kimliksizdir.
// Sebebin tamamı loga yazılır (bkz. corehttp.WriteError).
const codeDocumentUnavailable = "openapi_document_unavailable"

// adminPrefix admin API'sinin yol önekidir.
const adminPrefix = "/admin/v1"

// storePrefix mağaza API'sinin yol önekidir.
const storePrefix = "/store/v1"

// bearerScheme admin uçlarının güvenlik şemasının adıdır.
const bearerScheme = "bearerAuth"

// publishableScheme mağaza uçlarının güvenlik şemasının adıdır.
const publishableScheme = "publishableKey"

// loginPath kimlik doğrulaması GEREKTİRMEYEN admin ucudur.
//
// Jetonu almanın tek yolu burasıdır; korumalı olsaydı hiç kimse giriş
// yapamazdı. Şemada da korumasız görünmelidir, yoksa istemci üretecleri
// jeton olmadan çağrılamaz bir metod üretir.
//
// Korumasız olmak 401 ÜRETMEMEK demek DEĞİLDİR: hatalı e-posta/parola yine
// 401'dir (bkz. [varsayilanYanitlar]).
const loginPath = adminPrefix + "/auth/login"

// Operation tek bir yol+metod işleminin açıklamasıdır.
type Operation struct {
	// Summary işlemin tek satırlık özetidir.
	Summary string `json:"summary,omitempty"`
	// Description işlemin ayrıntılı açıklamasıdır.
	Description string `json:"description,omitempty"`
	// OperationID istemci üreteçlerinin metod adı olarak kullandığı kimliktir.
	OperationID string `json:"operationId,omitempty"`
	// Tags işlemi gruplayan etiketlerdir (genellikle modül adı).
	Tags []string `json:"tags,omitempty"`
	// Parameters yol ve sorgu parametreleridir.
	Parameters []Parameter `json:"parameters,omitempty"`
	// RequestBody istek gövdesinin şemasıdır; nil olabilir.
	RequestBody map[string]any `json:"requestBody,omitempty"`
	// Responses durum kodundan yanıt tanımına eşlemedir.
	Responses map[string]any `json:"responses"`
	// Security bu işlemin güvenlik gereksinimidir.
	//
	// omitempty BİLİNÇLİ olarak YOKTUR. OpenAPI'de boş dizi ("security: []")
	// "bu uç açıkça korumasız" demektir; omitempty ise boş diziyi JSON'a hiç
	// yazmaz ve alanı olmayan bir işlem "belirtilmemiş" sayılıp kök
	// seviyedeki varsayılan güvenliği MİRAS ALIR. Giriş ucunda kastedilen tam
	// tersidir: jetonu veren uç jeton isteyemez. Kaydın yazılmaması, kök
	// varsayılanın eklendiği gün giriş ucunu sessizce korumalı gösterirdi.
	//
	// nil bırakılırsa JSON'a "security": null yazılırdı; [Doc.islem] bu yüzden
	// her işlem için alanı doldurur ve [guvenlik] hiçbir zaman nil dönmez.
	Security []map[string][]string `json:"security"`
}

// Parameter bir yol ya da sorgu parametresidir.
type Parameter struct {
	// Name parametrenin adıdır.
	Name string `json:"name"`
	// In parametrenin yeridir: "path" | "query" | "header".
	In string `json:"in"`
	// Required parametrenin zorunlu olup olmadığıdır.
	Required bool `json:"required"`
	// Schema parametrenin tip şemasıdır.
	Schema map[string]any `json:"schema"`
	// Description parametrenin açıklamasıdır.
	Description string `json:"description,omitempty"`
}

// Doc üretilen OpenAPI belgesidir.
type Doc struct {
	// zenginlestirme "METOD YOL" anahtarından işlem ayrıntısına eşlemedir.
	zenginlestirme map[string]Operation
	// baslik API başlığıdır.
	baslik string
	// surum API sürümüdür.
	surum string
	// semalar Go tiplerinden türetilmiş bileşen şemalarıdır.
	semalar map[string]any
	// semaSahipleri her bileşen adının hangi Go tipinden türediğini tutar;
	// ad çakışmasını yakalayan tek kayıt budur.
	semaSahipleri map[string]reflect.Type
	// semaCakismalari aynı bileşen adını isteyen FARKLI tiplerin raporudur.
	semaCakismalari []string
	// anlatimSurumu anlatım kayıtlarının kaçıncı sürümde olduğudur.
	//
	// [Doc.Describe] ve bileşen kaydı ([Doc.structSemasiVeyaRef]) onu artırır;
	// [Doc.Handler] önbelleğinin GEÇERLİLİK anahtarına girer. Anlatım API'si
	// kurulum içindir ve TEK İPLİKLİDİR (modüller Describe'ı bileşim kökünde,
	// sunucu dinlemeye başlamadan çağırır); üretim tarafı ise eş zamanlıdır ve
	// bu alanı yalnızca [Doc.mu] altında OKUR.
	anlatimSurumu uint64
	// mu belge ÜRETİMİNİ, gorulen alanını ve önbelleği korur.
	//
	// Üretim baştan sona kilit altındadır çünkü okuma gibi görünse de
	// DEĞİŞTİRİR: [Doc.islem] anlatılan işlemin Responses haritasına ortak
	// hata yanıtlarını yazar. /openapi.json'a eş zamanlı iki istek gelmesi
	// olağandır ve kilitsiz iki üretim aynı haritaya aynı anda yazardı — Go'da
	// bu, kurtarılamayan bir çalışma zamanı hatasıdır.
	mu sync.Mutex
	// gorulen son üretimde bulunan route anahtarlarıdır;
	// UnmatchedDescriptions onu okur.
	gorulen map[string]struct{}
	// onbellek son üretilen belgenin KODLANMIŞ hâlidir (bkz. [Doc.Handler]).
	onbellek *onbellekGirdisi
}

// belgeKimligi belgenin üretildiği GİRDİLERİ tek bir karşılaştırılabilir
// değere indirger.
//
// Önbelleğin geçerliliği bir varsayıma ("ağaç artık donmuştur") değil bu
// değere bağlanır; girdilerden biri değişirse belge yeniden üretilir.
type belgeKimligi struct {
	// routeKarmasi ağaçtaki "METOD YOL" çiftlerinin karmasıdır.
	routeKarmasi uint64
	// routeSayisi ağaçtaki route sayısıdır.
	//
	// Karma SIRADAN BAĞIMSIZ birleştirildiği için (XOR) tek başına iki farklı
	// kümeyi aynı değere indirebilir; sayı, bu ihtimali pratikte kapatır.
	routeSayisi int
	// anlatimSurumu belgenin okunduğu andaki [Doc.anlatimSurumu] değeridir.
	anlatimSurumu uint64
}

// onbellekGirdisi üretilmiş belgeyi ve hangi girdiden üretildiğini tutar.
type onbellekGirdisi struct {
	// kimlik gövdenin üretildiği girdilerin kimliğidir.
	kimlik belgeKimligi
	// govde kodlanmış belgedir; üretim başarısızsa nil'dir.
	govde []byte
	// hata üretim başarısızsa saklanan hatadır.
	//
	// Hata da ÖNBELLEKLENİR: aynı girdiden aynı hata çıkar ve her istekte
	// yeniden üretmek, arızalı bir belgeyi sağlamından PAHALI kılardı.
	hata error
}

// New boş bir belge kurar.
func New(baslik, surum string) *Doc {
	return &Doc{
		zenginlestirme: make(map[string]Operation),
		baslik:         baslik,
		surum:          surum,
		semalar:        make(map[string]any),
		semaSahipleri:  make(map[string]reflect.Type),
	}
}

// Describer kendi uçlarını anlatabilen modüllerin OPSİYONEL arayüzüdür.
//
// # Neden module.Module'e eklenmedi
//
// Metodu modül sözleşmesine koymak TÜM modülleri aynı anda kıran bir
// değişiklikti ve bedeli karşılığında hiçbir şey vermezdi: anlatılmamış bir
// modül GEÇERLİ bir modeldir — belgede yolu, metodu ve güvenliğiyle görünür,
// yalnızca gövdesi olmaz. Zorunlu bir metot, boş gövdeli Describe
// uygulamalarını çoğaltmaktan başka bir şey üretmezdi.
//
// # Kim çağırır
//
// Kompozisyon kökü (cmd/server) modül listesi üzerinden tip iddiasıyla
// çağırır. Çekirdek çağıramaz: [Doc] modülleri tanımaz (Prensip 2.4) ve
// modül listesini gören tek yer kurulumdur.
type Describer interface {
	// Describe modülün uçlarını belgeye işler ([Doc.Describe] ile).
	Describe(d *Doc)
}

// Describe bir route'un işlem ayrıntılarını kaydeder.
//
// method ve pattern, chi'de tanımlandığı gibi verilmelidir (örn.
// "GET", "/store/v1/products/{id}"). Eşleşmeyen bir kayıt sessizce yok
// SAYILMAZ — [Doc.Build] onu [Doc.UnmatchedDescriptions] ile raporlar; aksi
// hâlde yolu değişmiş bir route'un açıklaması sessizce kaybolurdu.
//
// Kayıt, üretilmiş belgeyi de GEÇERSİZ kılar (bkz. [Doc.Handler]): kurulumdan
// sonra anlatılan bir uç, aksi hâlde önbellekteki eski belgede görünmezdi.
func (d *Doc) Describe(method, pattern string, op Operation) {
	d.zenginlestirme[anahtar(method, pattern)] = op
	d.anlatimSurumu++
}

// Build router'ı dolaşarak OpenAPI belgesini üretir.
//
// Üretim baştan sona kilit altındadır; gerekçesi [Doc.mu] alanındadır.
func (d *Doc) Build(r chi.Routes) (map[string]any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.uret(r)
}

// uret belgeyi üretir ve [Doc.mu] kilidinin TUTULDUĞUNU varsayar.
func (d *Doc) uret(r chi.Routes) (map[string]any, error) {
	yollar := map[string]any{}
	gorulen := map[string]struct{}{}

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		yol := normalizeYol(route)
		if !ilgili(yol) {
			return nil
		}

		gorulen[anahtar(method, yol)] = struct{}{}

		islem := d.islem(method, yol)

		mevcut, _ := yollar[yol].(map[string]any)
		if mevcut == nil {
			mevcut = map[string]any{}
			yollar[yol] = mevcut
		}

		mevcut[strings.ToLower(method)] = islem

		return nil
	})
	if err != nil {
		return nil, err
	}

	d.gorulen = gorulen

	// Çakışma kontrolü YÜRÜYÜŞTEN SONRADIR: eşleşmeyen açıklamalar
	// ([Doc.UnmatchedDescriptions]) çakışmadan bağımsız bir arızadır ve
	// operatörün ikisini birden görebilmesi için gorulen yine de dolmalıdır.
	if len(d.semaCakismalari) > 0 {
		return nil, errors.Invalid(codeSchemaNameConflict,
			"OpenAPI bileşen adı çakıştı: %s", strings.Join(d.semaCakismalari, "; "))
	}

	return map[string]any{
		"openapi": Version,
		"info": map[string]any{
			"title":   d.baslik,
			"version": d.surum,
		},
		"paths":      yollar,
		"components": d.bilesenler(),
	}, nil
}

// UnmatchedDescriptions [Doc.Build] sırasında hiçbir route ile eşleşmeyen
// açıklamaları döner.
//
// Boş olmayan bir sonuç, bir route'un yolu değişmiş ya da silinmiş ama
// açıklamasının kaldığı anlamına gelir. Sessiz kalmak, belgede olmayan bir
// ucun anlatılmasına ya da var olan bir ucun anlatılmamasına yol açardı.
func (d *Doc) UnmatchedDescriptions() []string {
	d.mu.Lock()
	defer d.mu.Unlock()

	var eksik []string

	for k := range d.zenginlestirme {
		if _, ok := d.gorulen[k]; !ok {
			eksik = append(eksik, k)
		}
	}

	sort.Strings(eksik)

	return eksik
}

// islem tek bir route için OpenAPI işlemini kurar.
func (d *Doc) islem(method, yol string) Operation {
	op := d.zenginlestirme[anahtar(method, yol)]

	if op.OperationID == "" {
		op.OperationID = islemKimligi(method, yol)
	}

	if len(op.Tags) == 0 {
		if etiket := etiketten(yol); etiket != "" {
			op.Tags = []string{etiket}
		}
	}

	// Yol parametreleri desenden türetilir; elle yazılanlar korunur.
	op.Parameters = birlestirParametreler(op.Parameters, yolParametreleri(yol))

	if op.Responses == nil {
		op.Responses = map[string]any{}
	}

	for kod, aciklama := range varsayilanYanitlar(yol) {
		if _, var_ := op.Responses[kod]; !var_ {
			op.Responses[kod] = aciklama
		}
	}

	// Yalnızca nil doldurulur: elle verilen BOŞ dilim "bu uç açıkça korumasız"
	// demektir ve ezilirse anlamı tersine döner.
	if op.Security == nil {
		op.Security = guvenlik(yol)
	}

	return op
}

// anahtar zenginleştirme haritasının anahtarını üretir.
func anahtar(method, pattern string) string {
	return strings.ToUpper(method) + " " + pattern
}

// normalizeYol chi'nin döndürdüğü route dizesini OpenAPI yoluna çevirir.
//
// chi iç içe Mount'larda "/*" kalıntıları bırakabilir; bunlar OpenAPI'de
// geçersizdir.
func normalizeYol(route string) string {
	yol := strings.ReplaceAll(route, "/*/", "/")
	yol = strings.TrimSuffix(yol, "/*")

	if yol == "" {
		return "/"
	}

	return yol
}

// ilgili yolun belgeye dâhil edilip edilmeyeceğini bildirir.
//
// Yalnızca versiyonlu API yüzeyi belgelenir; /health ve /ready operasyonel
// uçlardır ve istemci üreteçlerinin metod üretmesi istenmez.
func ilgili(yol string) bool {
	return strings.HasPrefix(yol, adminPrefix) || strings.HasPrefix(yol, storePrefix)
}

// etiketten yoldan modül etiketini çıkarır (örn. "/store/v1/products" → "products").
func etiketten(yol string) string {
	kalan := strings.TrimPrefix(strings.TrimPrefix(yol, adminPrefix), storePrefix)
	parcalar := strings.Split(strings.Trim(kalan, "/"), "/")

	if len(parcalar) == 0 || parcalar[0] == "" {
		return ""
	}

	return parcalar[0]
}

// islemKimligi metod ve yoldan bir operationId türetir.
func islemKimligi(method, yol string) string {
	temiz := strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_").Replace(yol)

	return strings.ToLower(method) + temiz
}

// yolParametreleri desendeki {ad} yer tutucularını parametreye çevirir.
func yolParametreleri(yol string) []Parameter {
	var params []Parameter

	for _, parca := range strings.Split(yol, "/") {
		if !strings.HasPrefix(parca, "{") || !strings.HasSuffix(parca, "}") {
			continue
		}

		ad := strings.Trim(parca, "{}")
		params = append(params, Parameter{
			Name:     ad,
			In:       "path",
			Required: true,
			Schema:   map[string]any{semaTip: tipDize},
		})
	}

	return params
}

// birlestirParametreler elle yazılan parametreleri türetilenlerle birleştirir.
//
// Elle yazılan KAZANIR: açıklama ve örnek gibi ayrıntıları türetici üretemez.
func birlestirParametreler(elle, turetilen []Parameter) []Parameter {
	varOlan := make(map[string]struct{}, len(elle))
	for _, p := range elle {
		varOlan[p.In+":"+p.Name] = struct{}{}
	}

	sonuc := append([]Parameter(nil), elle...)

	for _, p := range turetilen {
		if _, ok := varOlan[p.In+":"+p.Name]; !ok {
			sonuc = append(sonuc, p)
		}
	}

	return sonuc
}

// guvenlik yola göre güvenlik gereksinimini döner.
//
// HİÇBİR ZAMAN nil DÖNMEZ: [Operation.Security] omitempty taşımadığı için nil
// bir dilim şemaya "security": null yazar ve bu geçersizdir. Boş dilim ise
// hem geçerli hem anlamlıdır — "bu uç açıkça korumasız".
func guvenlik(yol string) []map[string][]string {
	switch {
	case yol == loginPath:
		// Boş dilim ile nil FARKLIDIR: boş dilim "bu uç açıkça korumasız"
		// demektir ve kök seviyedeki güvenliği EZER; nil "belirtilmemiş"
		// demektir ve okuyucu kök varsayılanı miras aldığını varsayar.
		return []map[string][]string{}
	case strings.HasPrefix(yol, adminPrefix):
		return []map[string][]string{{bearerScheme: {}}}
	case strings.HasPrefix(yol, storePrefix):
		return []map[string][]string{{publishableScheme: {}}}
	default:
		// Belgeye yalnızca admin/store yolları girer ([ilgili]); buraya düşen
		// bir yol için doğru cevap da "korumasız"dır, "belirtilmemiş" değil.
		return []map[string][]string{}
	}
}

// JSON Schema'nın sık tekrarlanan anahtar ve tip adları.
//
// Sabit olarak tutulmalarının sebebi tekrarın kendisi değil, yazım hatasının
// SESSİZ olmasıdır: "propertes" yazılmış bir harita anahtarı derlenir, şema
// üretilir ve yalnızca şemayı okuyan istemci alanı bulamayınca ortaya çıkar.
const (
	semaTip          = "type"
	semaOzellikler   = "properties"
	semaZorunlu      = "required"
	semaAciklama     = "description"
	semaOgeler       = "items"
	semaEkOzellikler = "additionalProperties"
	semaBicim        = "format"
	semaRef          = "$ref"
	semaHerhangi     = "anyOf"
	tipNesne         = "object"
	tipDize          = "string"
	tipTamSayi       = "integer"
	tipSayi          = "number"
	tipDizi          = "array"
	tipMantiksal     = "boolean"
	tipBos           = "null"
	bicimTarihSaat   = "date-time"
	bicimBayt        = "byte"
	bicimInt32       = "int32"
	bicimInt64       = "int64"
	bicimFloat       = "float"
	bicimDouble      = "double"
)

// Çekirdeğin kendi paylaşılan bileşenlerinin adları.
//
// Türetilen şemalarla AYNI ad alanını paylaşırlar; bu yüzden adları
// [ayrilmisSemaAdlari] üzerinden korunur.
const (
	semaAdiError = "Error"
	semaAdiList  = "List"
)

// varsayilanYanitlar her uçta olabilecek ortak hata yanıtlarını döner.
//
// Giriş ucu 401'in DIŞINDA TUTULMAZ. Uç korumasızdır ama işi tam olarak
// kimlik bilgisi doğrulamaktır ve hatalı e-posta/parolada 401 döner
// (auth servisi errors.Unauthorized üretir). "Korumasız" ile "401 üretmez"
// karıştırılırsa istemci üreteci giriş hatasını hiç ele almayan bir metod
// üretir ve hatalı parola istemcide beklenmeyen bir arıza gibi görünür.
func varsayilanYanitlar(yol string) map[string]any {
	yanitlar := map[string]any{
		"401": hataYaniti("Kimlik doğrulama eksik veya geçersiz"),
		"422": hataYaniti("Girdi doğrulamadan geçmedi"),
		"429": hataYaniti("İstek sınırı aşıldı"),
		"500": hataYaniti("Beklenmeyen sunucu hatası"),
	}

	if yol == loginPath {
		// Giriş ucunda 401 "jeton eksik" değil "kimlik bilgisi hatalı"dır.
		// Başarısız denemeler arasında ayrım yapılmaz; açıklama da bu yüzden
		// tek bir nedeni işaret etmez (bkz. auth adminLogin).
		yanitlar["401"] = hataYaniti("E-posta ya da parola hatalı")

		// 403 yalnızca yetkilendirme adımı OLAN uçlarda anlamlıdır; girişte
		// henüz bir kimlik yoktur, dolayısıyla yetersiz yetki de olamaz.
		return yanitlar
	}

	if strings.HasPrefix(yol, adminPrefix) {
		yanitlar["403"] = hataYaniti("Kimlik doğrulandı ama yetki yetersiz")
	}

	return yanitlar
}

// hataYaniti ortak hata zarfına atıfta bulunan bir yanıt tanımı üretir.
func hataYaniti(aciklama string) map[string]any {
	return Response(aciklama, refSemasi(semaAdiError))
}

// bilesenler paylaşılan şemaları, türetilmiş şemaları ve güvenlik tanımlarını
// döner.
func (d *Doc) bilesenler() map[string]any {
	return map[string]any{
		"securitySchemes": map[string]any{
			bearerScheme: map[string]any{
				semaTip:        "http",
				"scheme":       "bearer",
				"bearerFormat": "JWT",
				semaAciklama:   "Admin oturum jetonu. /admin/v1/auth/login ile alınır.",
			},
			publishableScheme: map[string]any{
				semaTip: "apiKey",
				"in":    "header",
				"name":  "x-publishable-api-key",
				semaAciklama: "Mağaza isteğini bir satış kanalına bağlar. " +
					"SIR DEĞİLDİR; tarayıcıda görünmesi beklenir.",
			},
		},
		"schemas": d.semaBilesenleri(),
	}
}

// semaBilesenleri çekirdeğin ortak şemalarını türetilmiş şemalarla birleştirir.
//
// Türetilenler ortakları EZEMEZ: [ayrilmisSemaAdlari] çakışmayı daha
// [Doc.SchemaOf] aşamasında yakalar ve [Doc.Build] hata döner. Burada
// ortakların sonra yazılması ikinci savunma hattıdır — bir gün o kontrol
// atlanırsa hata zarfının şeması yine de bozulmaz.
func (d *Doc) semaBilesenleri() map[string]any {
	semalar := make(map[string]any, len(d.semalar)+len(ayrilmisSemaAdlari))

	for ad, sema := range d.semalar {
		semalar[ad] = sema
	}

	semalar[semaAdiError] = map[string]any{
		semaTip:     tipNesne,
		semaZorunlu: []string{"error"},
		semaOzellikler: map[string]any{
			"error": map[string]any{
				semaTip:     tipNesne,
				semaZorunlu: []string{"code", "message"},
				semaOzellikler: map[string]any{
					"code":       map[string]any{semaTip: tipDize},
					"message":    map[string]any{semaTip: tipDize},
					"request_id": map[string]any{semaTip: tipDize},
					"details":    map[string]any{semaTip: tipNesne},
				},
			},
		},
	}

	// TİPSİZ liste zarfı BİLİNÇLİ olarak yayımlanmaz.
	//
	// Bir zamanlar "kayıt şeması bilinmeyen uçlar için" diye yazılıyordu ama
	// hiçbir uç ona atıf yapmıyordu; gerçek bir istemci üreteci
	// (openapi-generator) onu "kullanılmayan model" diye bildirdi ve üretilen
	// her istemcide ölü bir sınıf olarak duruyordu.
	//
	// Anlatılmamış liste uçlarına varsayılan olarak bağlamak da cazipti ama
	// YANLIŞ olurdu: zarf biçimi bu depoda evrensel olsa bile, bir ucun
	// gerçekten liste döndüğü DOĞRULANMADAN şemaya yazılamaz. Doğrulanmamış
	// bir iddia, sessizlikten kötüdür — istemci onu doğru sanır.
	//
	// Ad yine de [ayrilmisSemaAdlari] içinde KALIR: bir modülün "List" adlı
	// DTO'su, yayımlanan sözleşmede anlamı olmayan bir genel ad üretirdi.

	return semalar
}

// Handler üretilen şemayı JSON olarak sunan handler'ı döner.
//
// # Neden önbellek
//
// Belge üretimi ucuz DEĞİLDİR: router ağacının tamamı gezilir, her route için
// işlem nesnesi (parametreler, ortak yanıtlar, güvenlik) kurulur, bileşen
// şemaları kopyalanır ve sonuç JSON'a kodlanır. Bunu her istekte yapmak,
// küçük bir GET'i sürecin en pahalı işine çevirir — üstelik bu uç, kimlik ve
// kota kapılarının DIŞINDA mount edilebilen bir uçtur.
//
// # Neden "açılışta bir kez" değil
//
// Ağacın ne zaman DONDUĞU çekirdeğin bilebileceği bir şey değildir: route'ları
// modüller bootstrap sırasında, eklentiler ise ondan sonra bağlar
// (bkz. plugin paketindeki Registry.MountRoutes) ve bu handler'ın hangi sırada
// kaydedildiğine dair bir garanti YOKTUR. Açılışta bir kez üretmek, handler
// kaydedildikten sonra bağlanan her route'u belgeden SESSİZCE düşürürdü —
// belgenin varlık sebebi tam da bunun olmamasıdır.
//
// Bu yüzden önbellek bir varsayıma değil, belgenin GİRDİLERİNE bağlanır: her
// istek ağacın kimliğini çıkarır (yalnızca gezme; işlem kurulmaz, kodlama
// yapılmaz) ve anlatım sürümüyle birleştirip önbellektekiyle karşılaştırır.
// Girdi aynıysa kodlanmış gövde olduğu gibi yazılır. Kalan maliyet ağacın
// gezilmesidir ve üretimin yanında küçüktür; karşılığında önbellek, çalışırken
// route ekleyen bir kurulumda da doğru kalır.
func (d *Doc) Handler(r chi.Routes) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		govde, err := d.kodlanmisBelge(r)
		if err != nil {
			// Hata istemciye HAM verilmez: metni çakışan tiplerin PAKET
			// YOLLARINI taşır ve bu uç kimliksiz çağrılabilir. KindInternal'a
			// sarmak çekirdeğin kararını uygular — gövde ortak hata zarfıdır,
			// mesaj maskelenir, gerçek sebep istek kimliğiyle loglanır.
			corehttp.WriteError(req.Context(), w,
				errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
					"openapi belgesi üretilemedi"))

			return
		}

		// Başlık ve durum kodu çekirdeğin kapısından yazılır: nil gövde
		// WriteJSON'da "yalnızca başlık ve status" demektir, yani Content-Type
		// kararı burada İKİNCİ kez tanımlanmaz.
		corehttp.WriteJSON(req.Context(), w, http.StatusOK, nil)

		// Gövde doğrudan yazılır çünkü ZATEN KODLANMIŞTIR. Çekirdeğin
		// yazıcısına verilseydi (json.RawMessage ile) belge her istekte bir
		// kez daha taranıp kopyalanırdı; ölçülen bedel, önbelleğin kazandırdığı
		// sürenin çoğunu geri alacak kadar büyüktür.
		if _, err := w.Write(govde); err != nil {
			// Durum kodu çoktan gönderildi (istemci bağlantıyı kapatmış
			// olabilir); yapılabilecek tek şey kaydetmektir — corehttp.WriteJSON
			// de aynısını yapar.
			corehttp.LoggerFromContext(req.Context()).ErrorContext(req.Context(),
				"openapi belgesi yazılamadı",
				"error", err,
				"request_id", corehttp.RequestIDFromContext(req.Context()),
			)
		}
	}
}

// kodlanmisBelge belgeyi önbellekten döner, girdiler değiştiyse yeniden üretir.
func (d *Doc) kodlanmisBelge(r chi.Routes) ([]byte, error) {
	// Ağaç kilidin DIŞINDA gezilir: gezme belgeye dokunmaz ve kilit, üretimin
	// gerçekten gerektiği ana saklanır.
	kimlik, err := routeKimligi(r)
	if err != nil {
		return nil, err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	kimlik.anlatimSurumu = d.anlatimSurumu

	if d.onbellek != nil && d.onbellek.kimlik == kimlik {
		return d.onbellek.govde, d.onbellek.hata
	}

	govde, uretimHatasi := d.uretVeKodla(r)
	d.onbellek = &onbellekGirdisi{kimlik: kimlik, govde: govde, hata: uretimHatasi}

	return govde, uretimHatasi
}

// uretVeKodla belgeyi üretip JSON'a kodlar; kilidin TUTULDUĞUNU varsayar.
//
// Gövde GİRİNTİLİ ve satır sonuyla biter. İkisi de önbellekten önceki
// davranışla aynıdır ve aynı kalması bilinçlidir: belge yayımlanan bir
// SÖZLEŞMEDİR, kaydedilmiş çıktılarla karşılaştırılır (make openapi-schema) ve
// bir hızlandırmanın onu bayt düzeyinde değiştirmesi, değişikliği gerçek bir
// şema değişikliğinden ayırt edilemez kılardı. Girintinin bedeli de artık
// istek başına değil, üretim başına ödenir.
func (d *Doc) uretVeKodla(r chi.Routes) ([]byte, error) {
	belge, err := d.uret(r)
	if err != nil {
		return nil, err
	}

	govde, err := json.MarshalIndent(belge, "", "  ")
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
			"openapi belgesi kodlanamadı")
	}

	// MarshalIndent satır sonu koymaz; json.Encoder koyardı.
	return append(govde, '\n'), nil
}

// routeKimligi router ağacının O ANDAKİ içeriğini kimliğe indirger.
//
// Karma SIRADAN BAĞIMSIZ birleştirilir (XOR). chi'nin yürüyüş sırası bugün
// deterministiktir ama ona bağlanmanın bedeli sessizdir: sıra bir gün
// değişirse kimlik her istekte farklı çıkar, önbellek hiç tutmaz ve çıktı yine
// doğru olduğu için kimse fark etmez.
func routeKimligi(r chi.Routes) (belgeKimligi, error) {
	var kimlik belgeKimligi

	err := chi.Walk(r, func(
		method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler,
	) error {
		karma := karmaEkle(karmaEkle(karmaBaslangici, method), route)

		kimlik.routeKarmasi ^= karma
		kimlik.routeSayisi++

		return nil
	})
	if err != nil {
		return belgeKimligi{}, errors.Wrap(err, errors.KindInternal, codeDocumentUnavailable,
			"route ağacı gezilemedi")
	}

	return kimlik, nil
}

// FNV-1a 64-bit karmasının sabitleri.
//
// Karma elle yazılır çünkü hash/fnv'nin arayüzü her route için bir nesne
// AYIRIR; kimlik her istekte hesaplandığı için ayırma sayısı doğrudan route
// sayısıyla çarpılırdı. Kriptografik güç aranmıyor: değer yalnızca "girdi
// değişti mi" sorusuna cevap verir ve çakışma hâlinde bedel, belgenin bir kez
// fazladan üretilmemesidir — bu yüzden sayı ([belgeKimligi.routeSayisi])
// karmayla BİRLİKTE karşılaştırılır.
const (
	karmaBaslangici uint64 = 14695981039346656037
	karmaCarpani    uint64 = 1099511628211
)

// karmaEkle bir dizeyi mevcut karmaya karıştırır.
func karmaEkle(karma uint64, s string) uint64 {
	for i := range len(s) {
		karma ^= uint64(s[i])
		karma *= karmaCarpani
	}

	// Ayraç: "GET" + "/a" ile "GE" + "T/a" aynı karmayı vermemelidir.
	karma ^= uint64(' ')
	karma *= karmaCarpani

	return karma
}
