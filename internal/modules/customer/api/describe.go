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

// Describe customer'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (customerRequest,
// customerDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek
// için dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini
// genişletmek olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir
// hâle gelirdi. Sorgu parametreleri de burada durmalıdır, çünkü onları
// GERÇEKTEN okuyan kod ([pageParams], [boolParam], [stringParam]) bu
// pakettedir; anlatım başka bir pakette dursaydı ikisi sessizce ayrışırdı.
// Modülün [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # ADRES UÇLARI ANLATILMADI — bileşen adı çakışması
//
// Şema bileşeninin adı Go tip adından türetilir (baş harf büyür, "DTO" soneki
// düşer; bkz. çekirdekteki bilesenAdi). Bu paketteki [addressDTO] ile
// [addressRequest], cart/api'deki AYNI ADLI tiplerle aynı bileşen adını
// ("Address", "AddressRequest") ister. İki farklı tip aynı adı istediğinde
// [openapi.Doc.Build] hata döner ve belge TÜMDEN üretilemez — yani anlatılmış
// öteki uçlar da kaybolurdu.
//
// Tipi yeniden adlandırmak bu paketin tek başına verebileceği bir karar
// DEĞİLDİR: ad, istemci üreteçlerinin sınıf adı olarak kullandığı YAYIMLANAN
// sözleşmedir ve cart tarafındaki "Address" istemcilerde çoktan üretilmiş
// olabilir. Bu yüzden adres uçlarının hiçbiri anlatılmadı; belgede yolları,
// metotları ve güvenlikleriyle görünürler, yalnızca gövdeleri olmaz. Çözüm
// bir ad alanı kararıdır (örn. bileşen adına modül önekinin girmesi) ve
// çekirdekte verilmelidir.
//
// Eksiğin yazılması bilinçlidir: eksik olduğunu bilmek, eksik olduğunu
// sanmamaktan iyidir.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /store/v1/customers, boş
// bırakılabilen phone ve metadata'yı da ister. Alan ADLARI ve TİPLERİ
// doğrudur, yani şema yanlış bir alan uydurmaz; yalnızca fazla şey ister.
// Doğru çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required"
// politikası); tag'lere omitempty serpiştirmek zorunluluğu servisin
// doğrulamasından json etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeMusteriler(d)
	describeGruplar(d)
	describeVitrin(d)
}

// describeMusteriler müşterinin yönetim uçlarını anlatır.
func describeMusteriler(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/customers", openapi.Operation{
		Summary: "Kayıtlı müşteri hesabı oluşturur.",
		Description: "Yönetim ucu daima HESAP açar; misafir kaydı vitrin " +
			"akışının parçasıdır (POST /store/v1/customers).",
		RequestBody: d.RequestBody(customerRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan müşteri", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customers", openapi.Operation{
		Summary: "Müşterileri süzerek ve sayfalayarak listeler.",
		// Parametreler handler'ın OKUDUKLARIDIR, isteyebileceklerimiz değil:
		// [Handler.adminListCustomers] tam olarak bu beşini okur.
		Parameters: []openapi.Parameter{
			sorguParametresi("email", tipDize,
				"E-postaya göre süzer; MİSAFİR kayıtları da getirir, "+
					"dolayısıyla birden çok satır dönebilir."),
			sorguParametresi("has_account", tipMantiksal,
				"true yalnızca kayıtlı hesapları, false yalnızca misafirleri getirir."),
			sorguParametresi("group_id", tipDize, "Müşterileri tek bir grupla sınırlar."),
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
			sorguParametresi("after", tipDize,
				"Bir önceki sayfanın \"next_cursor\" değeri. Derin sayfalarda \"offset\"ten "+
					"ucuzdur: offset, veritabanına atladığı her satırı yürütüp ATTIRIR ve "+
					"maliyeti derinlikle büyür; cursor ise indeks koşuluna girer ve düz kalır. "+
					"\"after\" ile \"offset\" iki ayrı konum adlandırır ve birlikte "+
					"REDDEDİLİR. Yanıt \"next_cursor\" taşımıyorsa liste tükenmiştir."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Müşteri sayfası",
				d.List(customerDTO{}, openapi.WithCursor())),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Tek bir müşteriyi kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Müşteri", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Müşterinin verilen alanlarını günceller.",
		Description: "Semantik KISMİDİR: gövdede olmayan alan değişmez, " +
			"verilen boş dize ise gerçek bir temizlemedir.",
		RequestBody: d.RequestBody(updateCustomerRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen müşteri", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customers/{id}", openapi.Operation{
		Summary: "Müşteriyi ve adreslerini yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Müşteri silindi"),
		},
	})

	// Uç bir gövde OKUMAZ (bkz. [Handler.adminConvertGuest]); requestBody
	// yazmak, istemci üretecinin metoda doldurulacak bir argüman koyması ve
	// sunucunun onu sessizce yok sayması demekti.
	d.Describe(http.MethodPost, "/admin/v1/customers/{id}/convert-to-account", openapi.Operation{
		Summary: "Misafir kaydını kayıtlı hesaba çevirir.",
		Description: "Yanıt kaydın DÖNÜŞÜM SONRASI hâlidir; istemcinin " +
			"has_account alanını görmek için ikinci bir istek yapması gerekmez.",
		Responses: map[string]any{
			"200": openapi.Response("Hesaba çevrilmiş müşteri", d.Item(customerDTO{})),
		},
	})

	// Sayfalanmayan bir listedir ve sorgu dizesini OKUMAZ; zarf yine de liste
	// zarfıdır (bkz. [writeItems]), böylece istemcinin gördüğü zarf şekli uç
	// noktaya göre değişmez.
	d.Describe(http.MethodGet, "/admin/v1/customers/{id}/groups", openapi.Operation{
		Summary: "Müşterinin üye olduğu grupları döner.",
		Responses: map[string]any{
			"200": openapi.Response("Müşterinin grupları", d.List(customerGroupDTO{})),
		},
	})
}

// describeGruplar müşteri grubu uçlarını anlatır.
func describeGruplar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/customer-groups", openapi.Operation{
		Summary:     "Yeni müşteri grubu oluşturur.",
		RequestBody: d.RequestBody(groupRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan grup", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customer-groups", openapi.Operation{
		Summary: "Müşteri gruplarını sayfalayarak listeler.",
		Parameters: []openapi.Parameter{
			sorguParametresi("limit", tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Grup sayfası", d.List(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary: "Tek bir müşteri grubunu kimliğiyle döner.",
		Responses: map[string]any{
			"200": openapi.Response("Grup", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary:     "Grubun verilen alanlarını günceller.",
		RequestBody: d.RequestBody(updateGroupRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen grup", d.Item(customerGroupDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customer-groups/{id}", openapi.Operation{
		Summary: "Müşteri grubunu yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Grup silindi"),
		},
	})

	// Gövde ALIR ama gövde DÖNMEZ: üyelik bir kayıt değil bir bağdır ve
	// handler 204 yazar. Item/List kullanmak, istemcinin okuyacağı bir gövde
	// beklemesine yol açardı.
	d.Describe(http.MethodPost, "/admin/v1/customer-groups/{id}/customers", openapi.Operation{
		Summary:     "Müşteriyi gruba ekler.",
		Description: "İşlem idempotenttir; zaten üye olan müşteri için de 204 döner.",
		RequestBody: d.RequestBody(groupMemberRequest{}),
		Responses: map[string]any{
			"204": bosYanit("Müşteri gruba eklendi"),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/customer-groups/{id}/customers/{customer_id}",
		openapi.Operation{
			Summary: "Müşteriyi gruptan çıkarır.",
			Responses: map[string]any{
				"204": bosYanit("Müşteri gruptan çıkarıldı"),
			},
		})
}

// describeVitrin müşterinin kendi profiliyle ilgili vitrin uçlarını anlatır.
//
// Adres uçları burada YOKTUR; gerekçesi [Describe] belgesindeki bileşen adı
// çakışmasıdır.
func describeVitrin(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/store/v1/customers", openapi.Operation{
		Summary: "Misafir müşteri kaydı açar.",
		Description: "Aynı e-postayla birden çok misafir kaydı olabilir: " +
			"misafir kaydı bir kimlik değil, tek seferlik bir alışverişin " +
			"iletişim bilgisidir.",
		RequestBody: d.RequestBody(customerRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan misafir", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/store/v1/customers/{id}", openapi.Operation{
		Summary: "Müşterinin kendi profilini döner.",
		Responses: map[string]any{
			"200": openapi.Response("Müşteri profili", d.Item(customerDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/store/v1/customers/{id}", openapi.Operation{
		Summary:     "Müşterinin kendi profilini günceller.",
		RequestBody: d.RequestBody(updateCustomerRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen profil", d.Item(customerDTO{})),
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
// YOKTUR (bkz. corehttp.WriteJSON'a nil verilen çağrılar). Boş bir şema
// yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
