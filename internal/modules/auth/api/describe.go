package api

import (
	"net/http"
	"strings"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Şema ve parametre tanımlarında geçen JSON Schema adları.
//
// Çekirdeğin karşılıkları dışa kapalıdır; burada tekrarlanmalarının sebebi
// maliyet değil SESSİZLİK: "strig" yazılmış bir tip adı derlenir, belge
// üretilir ve yalnızca şemayı okuyan istemci alanı yanlış tiple ürettiğinde
// ortaya çıkar.
const (
	semaTip        = "type"
	semaBicim      = "format"
	semaOzellikler = "properties"
	semaRef        = "$ref"
	tipDize        = "string"
	tipTamSayi     = "integer"
	tipMantiksal   = "boolean"
)

// refOneki bileşen şemalarına yapılan atıfların yol önekidir.
const refOneki = "#/components/schemas/"

// İstek gövdesi tanımında JSON şemasına giden yol.
const (
	govdeIcerik = "content"
	govdeTur    = "application/json"
	govdeSema   = "schema"
)

// Parola alanının adı ve biçimi.
const (
	// alanParola düz parolanın istek gövdelerindeki JSON adıdır.
	alanParola = "password"
	// bicimParola JSON Schema'nın parola BİÇİMİDİR; alan adıyla aynı olması
	// rastlantıdır, bağ değildir.
	bicimParola = "password"
)

// Describe auth'un YÖNETİM uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI DTO'larıdır (loginRequest,
// userDTO …) ve şema onlardan yansımayla türetilir. Tipleri anlatabilmek için
// dışa açmak, yalnızca belge üretmek uğruna modülün yüzeyini genişletmek
// olurdu: dışa açık bir tip sözleşmedir ve dışarıdan kurulabilir hâle
// gelirdi. Sorgu parametreleri de handler'ın GERÇEKTEN okuduklarıdır ve o
// okuma bu paketteki admin.go ile [pageParams] içindedir; anlatım başka bir
// pakette dursaydı ikisi sessizce ayrışırdı. Modülün [openapi.Describer]
// uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa Register hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Yalnızca /admin/v1 vardır
//
// auth'un vitrin ucu YOKTUR; mağaza tarafındaki karşılığı bir uç değil,
// publishable anahtarı okuyan corehttp.RequireStore middleware'idir.
//
// # GÜVENLİK: parola şemada nasıl görünür
//
// Parola alanları istek şemasında GÖRÜNÜR — istemci onu göndermek zorundadır
// ve alanın adını bilmelidir — ama format: "password" ile işaretlenir (bkz.
// [parolaliGovde]). Yanıt şemalarında parola HİÇ GEÇMEZ ve geçemez: yanıt
// gövdeleri [userDTO] gibi ayrı tiplerden türer ve o tiplerde parola alanı
// yoktur.
//
// # GÜVENLİK: giriş ucunun korumasızlığı BURADA YAZILMAZ
//
// [LoginPath] işleminde Security alanı BİLİNÇLİ OLARAK boş bırakılır. Çekirdek
// giriş yolunu tanır ve oraya "açıkça korumasız" anlamına gelen boş dizi yazar
// (bkz. internal/core/openapi, guvenlik). Buraya elle bir değer yazmak kararı
// iki yere kopyalardı; ikisi ayrıştığı gün jetonu veren uç şemada jeton ister
// görünür ve istemci üreteci hiç çağrılamayan bir metot üretirdi.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek DTO'ları omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /admin/v1/users, boş bırakılabilen
// parolayı da ister. Alan ADLARI ve TİPLERİ doğrudur, yani şema yanlış bir
// alan uydurmaz; yalnızca fazla şey ister. Doğru çözüm ÇEKİRDEKTEDİR (istek
// gövdeleri için ayrı bir "required" politikası); tag'lere omitempty
// serpiştirmek zorunluluğu servisin doğrulamasından json etiketine taşır ve
// ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeKimlik(d)
	describeKullanicilar(d)
	describeAnahtarlar(d)
	describeKanallar(d)
}

// describeKimlik giriş, kimlik okuma ve çıkış uçlarını anlatır.
func describeKimlik(d *openapi.Doc) {
	d.Describe(http.MethodPost, LoginPath, openapi.Operation{
		Summary: "E-posta ve parolayla yönetim oturumu jetonu üretir.",
		Description: "Jetonu almanın tek yolu budur ve uç KORUMASIZDIR. " +
			"Hatalı e-posta ile hatalı parola AYNI 401'i döner; ayrım yapılsaydı " +
			"yanıtın kendisi 'bu e-posta kayıtlı' bilgisini verirdi.",
		RequestBody: parolaliGovde(d, loginRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Oturum jetonu, türü ve son kullanma anı",
				d.Item(loginResponse{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/auth/me", openapi.Operation{
		Summary: "Doğrulanmış çağıranın kimliğini ve yetkilerini döner.",
		Responses: map[string]any{
			"200": openapi.Response("Çağıranın kimliği", d.Item(principalResponse{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/auth/logout", openapi.Operation{
		Summary: "Çağıranın TÜM oturumlarını kapatır.",
		Description: "İptal TOPTANDIR: telefonundan çıkan yönetici dizüstündeki " +
			"oturumunu da kapatmış olur. Yanıt 204 değil 200'dür, çünkü status " +
			"kodu iptalin kapsamını ve dayandığı anı söyleyemez.",
		// Uç gövde OKUMAZ: kimin çıkacağını jetondan bilir (bkz.
		// [Handler.adminLogout]). Şemaya gövde yazmak hem okunmayan bir alan
		// vaat etmek hem de "kimin oturumu" sorusunun gövdeden sorulabileceğini
		// ima etmek olurdu.
		Responses: map[string]any{
			"200": openapi.Response("İptalin kapsamı ve anı", d.Item(logoutResponse{})),
		},
	})
}

// describeKullanicilar yönetim kullanıcısı uçlarını anlatır.
func describeKullanicilar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/users", openapi.Operation{
		Summary: "Yeni bir yönetim kullanıcısı oluşturur.",
		Description: "Gövdedeki parola boş bırakılabilir; o durumda kullanıcı " +
			"parolasız oluşturulur ve giriş yapabilmesi için önce " +
			"POST /admin/v1/users/{id}/password çağrılır. İstenen yetkiler " +
			"çağıranınkileri AŞAMAZ. Yanıtta parola GEÇMEZ.",
		RequestBody: parolaliGovde(d, createUserRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan kullanıcı", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/users", openapi.Operation{
		Summary: "Yönetim kullanıcılarını süzerek ve sayfalayarak listeler.",
		// Parametreler handler'ın OKUDUKLARIDIR, isteyebileceklerimiz değil:
		// [Handler.adminListUsers] yalnızca bu dördünü okur.
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("email", tipDize, "Kullanıcıları tek bir e-postayla sınırlar."),
			sorguParametresi("scope", tipDize, "Verilen yetkiyi taşıyan kullanıcıları döner."),
		),
		Responses: map[string]any{
			"200": openapi.Response("Kullanıcı sayfası", d.List(userDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Tek bir yönetim kullanıcısını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Kullanıcı", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Kullanıcının verilen alanlarını günceller.",
		// Gövdede parola YOKTUR ve bu bilinçlidir (bkz. [updateUserRequest]):
		// aynı gövdede olsaydı, adını güncelleyen bir isteğin yanlışlıkla
		// parolayı da değiştirmesi mümkün olurdu.
		RequestBody: d.RequestBody(updateUserRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen kullanıcı", d.Item(userDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/users/{id}", openapi.Operation{
		Summary: "Kullanıcıyı ve giriş kimliklerini yumuşak siler.",
		Responses: map[string]any{
			"204": bosYanit("Kullanıcı silindi"),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/users/{id}/password", openapi.Operation{
		Summary: "Kullanıcının parolasını belirler.",
		Description: "Uç ayrıdır: profil güncellemesiyle aynı gövdeye konsaydı, " +
			"adını değiştiren bir isteğin yanlışlıkla parolayı da sıfırlaması " +
			"mümkün olurdu. Yanıt GÖVDESİZDİR; parolayla ilgili hiçbir şeyi geri " +
			"yazmanın anlamı yoktur.",
		RequestBody: parolaliGovde(d, setPasswordRequest{}),
		Responses: map[string]any{
			"204": bosYanit("Parola değiştirildi"),
		},
	})
}

// describeAnahtarlar API anahtarı ve kanal bağı uçlarını anlatır.
func describeAnahtarlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/api-keys", openapi.Operation{
		Summary: "Yeni bir API anahtarı üretir ve düz metnini BİR KEZ döner.",
		Description: "Düz anahtar YALNIZCA bu yanıtın \"key\" alanında döner ve " +
			"bir daha hiçbir uçtan okunamaz: saklama yalnızca özet üzerinden " +
			"yapılır. İstemci değeri şimdi saklamazsa anahtar kaybolur ve tek " +
			"çare iptal edip yenisini üretmektir. Diğer tüm uçlar anahtarın " +
			"yalnızca maskelenmiş gösterimini (\"redacted\") döner. " +
			"created_by gövdeden değil doğrulanmış kimlikten doldurulur; " +
			"istenen yetkiler çağıranınkileri AŞAMAZ.",
		RequestBody: d.RequestBody(createAPIKeyRequest{}),
		Responses: map[string]any{
			"201": openapi.Response(
				"Anahtar kaydı ve DÜZ metni; düz metin bir daha dönmez",
				d.Item(createAPIKeyResponse{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys", openapi.Operation{
		Summary: "API anahtarlarını süzerek ve sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("type", tipDize,
				"Anahtar türü: \"publishable\" ya da \"secret\"."),
			sorguParametresi("revoked", tipMantiksal,
				"true yalnızca iptallileri, false yalnızca etkinleri döner."),
		),
		Responses: map[string]any{
			"200": openapi.Response("Anahtar sayfası; düz metin İÇERMEZ",
				d.List(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys/{id}", openapi.Operation{
		Summary: "Tek bir API anahtarını döner; düz metin İÇERMEZ.",
		Responses: map[string]any{
			"200": openapi.Response("Anahtar kaydı", d.Item(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/api-keys/{id}", openapi.Operation{
		Summary: "API anahtarını yumuşak siler.",
		Description: "Sızıntı sonrası tercih edilmesi gereken işlem iptaldir " +
			"(POST /admin/v1/api-keys/{id}/revoke); silme, yanlışlıkla " +
			"oluşturulmuş bir kaydı temizlemek içindir.",
		Responses: map[string]any{
			"204": bosYanit("Anahtar silindi"),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/api-keys/{id}/revoke", openapi.Operation{
		Summary: "API anahtarını iptal eder.",
		Description: "İptal SİLME DEĞİLDİR: kayıt listede kalır ve ne zaman, kim " +
			"tarafından kapatıldığı görünür. Zaten iptalli bir anahtarda 409 döner.",
		// Uç gövde OKUMAZ: iptal edilecek anahtarı yoldan, iptali yapanı
		// jetondan bilir (bkz. [Handler.adminRevokeAPIKey]).
		Responses: map[string]any{
			"200": openapi.Response("İptal edilmiş anahtar kaydı", d.Item(apiKeyDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/api-keys/{id}/sales-channels", openapi.Operation{
		Summary: "Anahtarın bağlı olduğu satış kanallarını döner.",
		// Sorgu parametresi YOKTUR: uç sayfalamaz, tek sayfada tüm bağları
		// yazar (bkz. [writeItems]). limit/offset duyurmak, sunucunun sessizce
		// yok sayacağı bir özellik vaat etmek olurdu.
		Responses: map[string]any{
			"200": openapi.Response("Bağlı kanallar; devre dışı olanlar da listelenir",
				d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodPost, "/admin/v1/api-keys/{id}/sales-channels", openapi.Operation{
		Summary:     "Publishable anahtarı bir satış kanalına bağlar.",
		RequestBody: d.RequestBody(linkChannelRequest{}),
		// Yanıt 201 DEĞİL 200'dür ve TEKİL değil LİSTEDİR: bağ kurulduktan
		// sonra anahtarın GÜNCEL kanal listesi döner (bkz.
		// [Handler.adminLinkKeyChannel]). 201 yazmak istemci üretecinde yanlış
		// dallanma, tekil zarf yazmak da okunamayan bir gövde üretirdi.
		Responses: map[string]any{
			"200": openapi.Response("Bağ kurulduktan sonraki güncel kanal listesi",
				d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}",
		openapi.Operation{
			Summary: "Anahtar ile satış kanalı arasındaki bağı kaldırır.",
			Responses: map[string]any{
				"204": bosYanit("Bağ kaldırıldı"),
			},
		})
}

// describeKanallar satış kanalı uçlarını anlatır.
//
// # Bileşen adı ÇAKIŞMASI çözüldü
//
// Bu modülün [salesChannelRequest] tipi kanalın KENDİSİNİ oluşturur; product
// modülünde de aynı Go adını taşıyan bir tip vardı ama o, ürünü bir kanala
// BAĞLAR. İki farklı şey aynı yayımlanan bileşen adını ("SalesChannelRequest")
// isteyince [openapi.Doc.Build] hata döner ve belgenin TAMAMI üretilemez —
// yalnızca o uç değil, /openapi.json'ın kendisi 500 olur.
//
// Çözüm product tarafındaki tipi gerçekte ne olduğuna göre adlandırmaktı
// (linkSalesChannelRequest). Bileşen adı yayımlanan sözleşmedir; Go adlandırma
// tesadüfünün onu belirlemesine izin verilmez.
func describeKanallar(d *openapi.Doc) {
	d.Describe(http.MethodPost, "/admin/v1/sales-channels", openapi.Operation{
		Summary:     "Yeni satış kanalı oluşturur.",
		RequestBody: d.RequestBody(salesChannelRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan kanal", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/sales-channels", openapi.Operation{
		Summary: "Satış kanallarını süzerek ve sayfalayarak listeler.",
		Parameters: append(sayfaParametreleri(),
			sorguParametresi("name", tipDize, "Kanalları ada göre süzer."),
			sorguParametresi("is_disabled", tipMantiksal,
				"Verilmezse süzme yapılmaz; false yalnızca etkin kanalları döner."),
		),
		Responses: map[string]any{
			"200": openapi.Response("Kanal sayfası", d.List(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodGet, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary: "Tek bir satış kanalını döner.",
		Responses: map[string]any{
			"200": openapi.Response("Kanal", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodPut, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary:     "Satış kanalının verilen alanlarını günceller.",
		RequestBody: d.RequestBody(updateSalesChannelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen kanal", d.Item(salesChannelDTO{})),
		},
	})

	d.Describe(http.MethodDelete, "/admin/v1/sales-channels/{id}", openapi.Operation{
		Summary: "Satış kanalını yumuşak siler ve anahtar bağlarını kaldırır.",
		Responses: map[string]any{
			"204": bosYanit("Kanal silindi"),
		},
	})
}

// parolaliGovde parola taşıyan istek gövdesini DTO'dan türetir ve parola
// alanını format: "password" ile işaretler.
//
// # Neden sonradan işaretleniyor
//
// Çekirdek şemayı Go TİPİNDEN türetir ve [secret] tel üzerinde sıradan bir
// dizedir; "bu bir paroladır" bilgisini tip taşımaz. Çekirdeğe bir kanca
// eklemek auth'un kavramını çekirdeğe taşırdı (Prensip 2.4), bu yüzden işaret
// türetilen şemanın ÜZERİNE, burada yazılır.
//
// # İşaretin karşılığı
//
// format: "password" bir doğrulama değil GÖSTERİM sözleşmesidir: istemci
// üreteçleri ve şema görüntüleyiciler alanı maskeler, örnek istek üreten
// araçlar onu ekrana açık yazmaz. İşaretsiz bırakılan bir parola, e-posta ile
// aynı görünen sıradan bir dizedir.
//
// # Neden BİLEŞENE yazmak güvenlidir
//
// İşaret [openapi.Doc.Schemas] üzerinden bileşenin kendisine işlenir. Harita
// üst seviyede kopyalanır ama bileşen şemaları PAYLAŞILAN haritalardır ve
// yazma onlara işler. Bileşene yazmak doğrudur çünkü parola taşıyan üç tip de
// YALNIZCA istek gövdesidir; hiçbir yanıt onlara atıf yapmaz. Bağ incedir ve
// bilinçli olarak testle kilitlenir (describe_internal_test.go): çekirdek bir
// gün derin kopya dönerse test düşer, şema sessizce işaretsiz kalmaz.
func parolaliGovde(d *openapi.Doc, govdeTipi any) map[string]any {
	govde := d.RequestBody(govdeTipi)

	sema := altHarita(govde, govdeIcerik, govdeTur, govdeSema)

	ref, _ := sema[semaRef].(string)
	bilesen, _ := d.Schemas()[strings.TrimPrefix(ref, refOneki)].(map[string]any)

	if parola := altHarita(bilesen, semaOzellikler, alanParola); parola != nil {
		parola[semaBicim] = bicimParola
	}

	return govde
}

// altHarita iç içe haritalarda bir yolu izler; yol kırılırsa nil döner.
//
// nil kök de güvenlidir: Go'da nil haritadan okumak sıfır değer verir ve yol
// ilk adımda kesilir.
func altHarita(kok map[string]any, yol ...string) map[string]any {
	dugum := kok

	for _, ad := range yol {
		alt, ok := dugum[ad].(map[string]any)
		if !ok {
			return nil
		}

		dugum = alt
	}

	return dugum
}

// sayfaParametreleri sayfalama parametrelerini döner.
//
// İkisi de ZORUNLU DEĞİLDİR: verilmediklerinde servis varsayılanı uygulanır
// (bkz. [pageParams]). Ortak bir yardımcıya alınmalarının sebebi tekrarın
// kendisi değil, açıklamaların ayrışmasıdır — üç liste ucunda elle
// yazılsalardı biri değiştiğinde ötekiler sessizce eskirdi.
func sayfaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
		sorguParametresi("offset", tipTamSayi, "Atlanacak kayıt sayısı."),
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
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
// YOKTUR (bkz. admin.go, corehttp.WriteJSON'a nil verilen çağrılar). Boş bir
// şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve istemci
// üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
