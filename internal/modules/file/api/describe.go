package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Şemalarda geçen JSON Schema adları.
//
// Çekirdeğin karşılıkları dışa kapalıdır ve burada tekrarlanmalarının sebebi
// maliyet değil SESSİZLİK: "strig" yazılmış bir tip adı derlenir, belge
// üretilir ve yalnızca şemayı okuyan istemci parametreyi yanlış tiple
// ürettiğinde ortaya çıkar.
const (
	semaTip        = "type"
	semaBicim      = "format"
	semaOzellikler = "properties"
	semaZorunlu    = "required"
	semaAciklama   = "description"
	tipDize        = "string"
	tipNesne       = "object"
	tipTamSayi     = "integer"
	bicimIkili     = "binary"
)

// icerikMultipart yükleme ucunun istek gövdesinin ortam tipidir.
const icerikMultipart = "multipart/form-data"

// Describe file'ın uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövde bu paketin DIŞA KAPALI DTO'sudur ([uploadDTO]) ve şema ondan
// yansımayla türetilir. Tipi anlatabilmek için dışa açmak, yalnızca belge
// üretmek uğruna modülün yüzeyini genişletmek olurdu. Sorgu parametreleri de
// aynı sebeple burada durur — hangi parametrenin GERÇEKTEN okunduğunu bilen
// kod api.go içindedir.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa [Handler.Routes] hiç çağrılmamışken de belge üretilebilir.
//
// # SUNUM UCU BELGEDE YOKTUR
//
// GET /files/{key} anlatılmaz ve anlatılamaz: çekirdek belgeye yalnızca
// /admin/v1 ve /store/v1 öneklerini alır (bkz. openapi paketi). Eksiklik
// bilinçlidir ve doğrudur — o uç bir API çağrısı değil, bir <img> etiketinin
// hedefidir; istemci üretecinin ona metot üretmesinin bir anlamı olmazdı.
// Adresi zaten yükleme yanıtındaki "url" alanı verir.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathAdminUploads, openapi.Operation{
		Summary: "Tek bir dosyayı yükler ve erişilebilir adresini döner.",
		Description: "Gövde JSON DEĞİL " + icerikMultipart + "'dır ve tek bir " +
			"\"" + fieldFile + "\" alanı taşır. İçerik tipi istemcinin " +
			"Content-Type başlığından değil, dosyanın İÇERİĞİNDEN tespit edilir; " +
			"tespit edilen tip izin listesinde yoksa 422 döner. Boyut sınırı " +
			"FILE_MAX_UPLOAD_BYTES ile yapılandırılır ve aşılması da 422'dir " +
			"(kod: file_upload_too_large). İstemcinin dosya adı yalnızca " +
			"gösterim için saklanır; depodaki yeri sunucu üretir.",
		RequestBody: dosyaGovdesi(),
		Responses: map[string]any{
			"201": openapi.Response("Yüklenen dosya", d.Item(uploadDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathAdminUploads, openapi.Operation{
		Summary: "Yükleme defterini en yeniden eskiye sayfalayarak listeler.",
		Parameters: []openapi.Parameter{
			sorguParametresi(queryLimit, tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi(queryOffset, tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Yükleme kayıtları", d.List(uploadDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathAdminUpload, openapi.Operation{
		Summary: "Yüklemeyi depodan ve defterden siler.",
		Description: "İDEMPOTENTTİR: zaten silinmiş (ya da hiç var olmamış) bir " +
			"kimlik de 204 döner. Silme bir son durum iddiasıdır ve yeniden " +
			"denenen bir temizlik akışı ikinci turunda hata almamalıdır.",
		Responses: map[string]any{
			// Gövdesiz yanıt: 204'e içerik şeması yazmak, istemci üretecine
			// okunacak bir gövde vaat etmek olurdu.
			"204": map[string]any{semaAciklama: "Yükleme silindi; gövde yoktur."},
		},
	})
}

// dosyaGovdesi yükleme ucunun multipart istek gövdesini anlatır.
//
// [openapi.Doc.RequestBody] BURADA KULLANILAMAZ: o yardımcı gövdeyi
// "application/json" olarak yazar ve bu uç JSON okumaz. Kolaylık olsun diye
// onu çağırmak, şemanın en görünür yerinde doğrudan yalan söylemek olurdu —
// üretilen istemci dosyayı JSON gövdesinde göndermeye çalışır ve her istek
// reddedilirdi.
func dosyaGovdesi() map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			icerikMultipart: map[string]any{
				"schema": map[string]any{
					semaTip:     tipNesne,
					semaZorunlu: []string{fieldFile},
					semaOzellikler: map[string]any{
						fieldFile: map[string]any{
							semaTip:      tipDize,
							semaBicim:    bicimIkili,
							semaAciklama: "Yüklenecek dosyanın ham içeriği.",
						},
					},
				},
			},
		},
	}
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// "zorunlu" bayrağı YOKTUR çünkü bu modülde zorunlu sorgu parametresi de
// yoktur: sayfalama parametreleri verilmezse servis varsayılanı uygular.
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}
