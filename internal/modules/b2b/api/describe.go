package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Parametre şemalarında geçen JSON Schema adları.
//
// Çekirdeğin karşılıkları dışa kapalıdır ve burada tekrarlanmalarının sebebi
// maliyet değil SESSİZLİK: "strig" yazılmış bir tip adı derlenir, belge
// üretilir ve yalnızca şemayı okuyan istemci parametreyi yanlış tiple
// ürettiğinde ortaya çıkar.
const (
	semaTip      = "type"
	tipDize      = "string"
	tipTamSayi   = "integer"
	tipMantiksal = "boolean"
)

// Describe b2b'nin uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (companyRequest,
// employeeDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu. Sorgu parametreleri de burada durmalıdır, çünkü onları GERÇEKTEN
// okuyan kod ([pageParams], [boolParam], [stringParam]) bu pakettedir;
// anlatım başka bir pakette dursaydı ikisi sessizce ayrışırdı.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir ve
// bu, YANIT gövdeleri için doğru cevaptır. İstek gövdesinde ise "required"
// istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir ve bunu tip bilemez: bu
// paketin istek DTO'ları omitempty taşımadığı için hepsi zorunlu görünür —
// örneğin POST /admin/v1/b2b/companies, boş bırakılabilen adres alanlarını da
// ister. Alan ADLARI ve TİPLERİ doğrudur; şema yalnızca fazla şey ister. Doğru
// çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required" politikası);
// tag'lere omitempty serpiştirmek zorunluluğu servisin doğrulamasından json
// etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeSirketler(d)
	describeCalisanlar(d)
	describeVitrin(d)
}

// describeSirketler şirketin yönetim uçlarını anlatır.
func describeSirketler(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/b2b/companies", openapi.Operation{
		Summary: "Yeni şirket oluşturur.",
		Description: "Para birimi ZORUNLUDUR: çalışanların harcama limitleri " +
			"o para biriminde ifade edilir. E-posta benzersiz DEĞİLDİR.",
		RequestBody: d.RequestBody(companyRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan şirket", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/companies", openapi.Operation{
		Summary: "Şirketleri süzerek ve sayfalayarak listeler.",
		// Parametreler handler'ın OKUDUKLARIDIR, isteyebileceklerimiz değil:
		// [Handler.adminListCompanies] tam olarak bu üçünü okur.
		Parameters: []openapi.Parameter{
			sorguParametresi("email", tipDize,
				"E-postaya göre süzer; e-posta benzersiz olmadığı için birden çok kayıt dönebilir."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Şirket sayfası", d.List(companyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Tek bir şirketi kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Şirket", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Şirketin verilen alanlarını günceller.",
		Description: "Semantik KISMİDİR: gövdede olmayan alan değişmez, " +
			"adres alanlarında verilen boş dize gerçek bir temizlemedir.",
		RequestBody: d.RequestBody(updateCompanyRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen şirket", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/b2b/companies/{id}", openapi.Operation{
		Summary: "Şirketi ve ÇALIŞANLARINI yumuşak siler.",
		Description: "Çalışan kayıtları da silinir ve müşteri bağları kaldırılır: " +
			"canlı bir çalışan kaydı daima canlı bir şirkete aittir.",
		Responses: map[string]any{
			"204": bosYanit("Şirket silindi"),
		},
	})
}

// describeCalisanlar çalışanın yönetim uçlarını anlatır.
func describeCalisanlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/b2b/employees", openapi.Operation{
		Summary: "Şirkete çalışan ekler.",
		Description: "Bir müşteri en fazla BİR şirketin çalışanı olabilir; " +
			"zaten bağlı bir müşteri için 409 döner. spending_limit boş " +
			"bırakılırsa çalışan sınırsız harcayabilir.",
		RequestBody: d.RequestBody(employeeRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan çalışan", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/employees", openapi.Operation{
		Summary: "Çalışanları süzerek ve sayfalayarak listeler.",
		Parameters: []openapi.Parameter{
			sorguParametresi("company_id", tipDize, "Çalışanları tek bir şirketle sınırlar."),
			sorguParametresi("is_company_admin", tipMantiksal,
				"true yalnızca şirket yöneticilerini, false yalnızca diğerlerini getirir."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Çalışan sayfası", d.List(employeeDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Tek bir çalışanı kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Çalışan", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Çalışanın harcama yetkisini günceller.",
		Description: "Limiti KALDIRMAK için clear_spending_limit gönderilir: " +
			"JSON'da null ile alanın hiç gönderilmemesi ayırt edilemez.",
		RequestBody: d.RequestBody(updateEmployeeRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen çalışan", d.Item(employeeDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/b2b/employees/{id}", openapi.Operation{
		Summary: "Çalışanı yumuşak siler ve müşteri bağını kaldırır.",
		Description: "Bağın kaldırılması şarttır: kalsaydı müşteri bir daha " +
			"hiçbir şirkete çalışan olarak eklenemezdi.",
		Responses: map[string]any{
			"204": bosYanit("Çalışan silindi"),
		},
	})
}

// describeVitrin müşterinin kendi şirketiyle ilgili vitrin uçlarını anlatır.
func describeVitrin(d *openapi.Doc) {
	d.Describe(http.MethodGet, "/store/v1/b2b/customers/{customer_id}/company", openapi.Operation{
		Summary: "Müşterinin KENDİ şirketini döner.",
		Description: "Şirket, müşterinin kendi çalışan kaydından türetilir; " +
			"şirket kimliğiyle çağrılabilen bir uç YOKTUR. Müşteri hiçbir " +
			"şirketin çalışanı değilse 404 döner.",
		Responses: map[string]any{
			"200": openapi.Response("Müşterinin şirketi", d.Item(companyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/b2b/customers/{customer_id}/employee", openapi.Operation{
		Summary: "Müşterinin KENDİ çalışan kaydını döner.",
		Description: "Harcama limitini, sıfırlanma aralığını ve geçerli " +
			"pencerenin başlangıcını taşır. KALAN hak hesaplanmaz: pencere " +
			"içindeki sipariş toplamı order modülünün verisidir.",
		Responses: map[string]any{
			"200": openapi.Response("Müşterinin çalışan kaydı", d.Item(storeEmployeeDTO{})),
		},
	})
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// Hiçbiri zorunlu DEĞİLDİR: verilmediklerinde handler süzgeci uygulamaz ya da
// servisin varsayılanıyla devam eder (bkz. [pageParams], [boolParam],
// [stringParam]).
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}

// bosYanit GÖVDESİZ bir yanıt tanımı üretir.
//
// [openapi.Response] her zaman bir gövde şeması yazar; 204'ün gövdesi ise
// YOKTUR (bkz. corehttp.WriteJSON'a nil verilen çağrılar). Boş bir şema yazmak
// "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci üreteci
// okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
