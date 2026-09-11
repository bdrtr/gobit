package api

import (
	"net/http"

	"github.com/bdrtr/gobit/core/openapi"
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
			"şirketin çalışanı değilse 404 döner. Where this installation has " +
			"bound a customer identity, a path naming a customer that identity " +
			"CONTRADICTS is refused.",
		Responses: storefrontClaimResponses("200",
			openapi.Response("Müşterinin şirketi", d.Item(companyDTO{}))),
	})

	d.Describe(http.MethodGet, "/store/v1/b2b/customers/{customer_id}/employee", openapi.Operation{
		Summary: "Müşterinin KENDİ çalışan kaydını döner.",
		Description: "Harcama limitini, sıfırlanma aralığını ve geçerli " +
			"pencerenin başlangıcını taşır. KALAN hak hesaplanmaz: pencere " +
			"içindeki sipariş toplamı order modülünün verisidir. Where this " +
			"installation has bound a customer identity, a path naming a " +
			"customer that identity CONTRADICTS is refused.",
		Responses: storefrontClaimResponses("200",
			openapi.Response("Müşterinin çalışan kaydı", d.Item(storeEmployeeDTO{}))),
	})
}

// storefrontClaimResponses merges a storefront operation's success response
// with the refusals ADR 0057 gave both of these routes.
//
// # What an installation that bound NOTHING answers
//
// 401 identity_not_bound, on every one of these routes, since ADR 0125. Between
// ADR 0057 and that record it served the path's claim unchecked, and the
// sentence that stood here said so; the old answer is now a setting an operator
// asks for by name (STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM).
//
// The 403 below is still conditional: contradicting a claim needs something to
// contradict it with.
//
// # Why FIVE statuses and not the two this module returns
//
// Because [Handler.storeCustomerID] passes the bound identity's error through
// UNWRAPPED, which is the decision rather than an accident: the embedder picks
// the status by picking its error's kind. An expired session is the embedder's
// 401, a suspended account its 403, an unreachable identity provider its 503.
// Describing only the codes this package itself returns would document half the
// surface and leave the other half to be discovered in production.
//
// The wording is this module's own rather than a shared helper's, because the
// sentence a reader needs names the surface: what these two routes hand back is
// an employer and a spending allowance, and the customer module's copy speaks
// about an address book.
func storefrontClaimResponses(success string, response any) map[string]any {
	return map[string]any{
		success: response,
		"401": openapi.ErrorResponse(
			"The publishable key was accepted and the request is not proven. Either the " +
				"customer identity this installation bound refused it with an error of its " +
				"own that asks the shopper to sign in — the code is the embedder's — or NO " +
				"identity is bound at all, which answers \"identity_not_bound\" since ADR " +
				"0125. An installation that wants the pre-0125 answer, where the path's " +
				"claim was served unchecked, sets " +
				"STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM."),
		"403": openapi.ErrorResponse(
			"Either the request proves a DIFFERENT customer than the one named in the " +
				"path — code \"identity_mismatch\", which does not depend on whether that " +
				"customer is anybody's employee, because the comparison is between the path " +
				"and the proof and no record is read to make it — or the bound identity " +
				"refused this request with a forbidding error of its own."),
		"404": openapi.ErrorResponse(
			"The customer is the employee of no company. Where an identity is bound it is " +
				"a fact about the PROVEN customer, because a contradicted claim never " +
				"reaches the lookup; where none is bound it is a fact about the customer " +
				"the path named, whoever asked."),
		"500": openapi.ErrorResponse(
			"Either the bound customer identity returned neither an identifier nor an " +
				"error — code \"identity_unproven\", a fault in the installation's " +
				"implementation rather than in this request — or something else failed on " +
				"the server."),
		"503": openapi.ErrorResponse(
			"The bound customer identity could not answer: its own dependency, such as the " +
				"identity provider it calls, is unreachable. The code is the embedder's and " +
				"the request is worth retrying."),
	}
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
