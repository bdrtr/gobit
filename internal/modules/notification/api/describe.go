package api

import (
	"net/http"

	"github.com/bdrtr/gobit/internal/core/openapi"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
)

// Parametre şemalarında geçen JSON Schema adları.
//
// Çekirdeğin karşılıkları dışa kapalıdır ve burada tekrarlanmalarının sebebi
// maliyet değil SESSİZLİK: "strig" yazılmış bir tip adı derlenir, belge
// üretilir ve yalnızca şemayı okuyan istemci parametreyi yanlış tiple
// ürettiğinde ortaya çıkar.
const (
	semaTip    = "type"
	tipDize    = "string"
	tipTamSayi = "integer"
)

// Describe notification'ın ucunu OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövde bu paketin DIŞA KAPALI DTO'sudur ([deliveryDTO]) ve şema
// ondan yansımayla türetilir. Tipi anlatabilmek için dışa açmak, yalnızca belge
// üretmek uğruna modülün yüzeyini genişletmek olurdu. Sorgu parametreleri de
// aynı sebeple burada durur — hangi parametrenin GERÇEKTEN okunduğunu bilen kod
// api.go içindedir; anlatım başka bir pakete taşınsaydı ikisi sessizce
// ayrışırdı.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir. Metodu
// [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı OLDUĞUNU
// söylerdi; oysa [Handler.Routes] hiç çağrılmamışken de belge üretilebilir ve
// üretilmelidir.
func Describe(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathAdminDeliveries, openapi.Operation{
		Summary: "Bildirim teslim günlüğünü sayfalayarak listeler.",
		Description: "Kayıtlar ALICI ADRESİ TAŞIMAZ: günlük \"kime gitti\"yi değil " +
			"\"gitti mi\"yi yanıtlar. Bir siparişin bildirimlerini görmek için " +
			"reference süzgecine sipariş kimliği verilir.",
		Parameters: []openapi.Parameter{
			sorguParametresi(queryReference, tipDize,
				"Listeyi tek bir siparişin kayıtlarıyla sınırlar."),
			sorguParametresi(queryStatus, tipDize,
				"Teslim durumu süzgeci: "+durumlarMetni()+". Tanınmayan bir değer 422 döner."),
			sorguParametresi(queryLimit, tipTamSayi,
				"Sayfa boyutu; verilmezse servisin varsayılanı uygulanır."),
			sorguParametresi(queryOffset, tipTamSayi, "Atlanacak kayıt sayısı."),
		},
		Responses: map[string]any{
			"200": openapi.Response("Teslim günlüğü kayıtları", d.List(deliveryDTO{})),
		},
	})
}

// durumlarMetni geçerli teslim durumlarını belge metnine yazar.
//
// Liste elle yazılmaz: durum kümesi models paketindedir ve oraya eklenen bir
// durum belgeye de girmelidir. Elle yazılan bir liste, eklenen durumun
// belgeden sessizce düşmesi demekti.
func durumlarMetni() string {
	durumlar := []models.DeliveryStatus{
		models.DeliveryPending, models.DeliverySent,
		models.DeliveryFailed, models.DeliverySkipped,
	}

	out := ""
	for i, durum := range durumlar {
		if i > 0 {
			out += ", "
		}
		out += durum.String()
	}
	return out
}

// sorguParametresi sorgu dizesinden okunan bir parametreyi tanımlar.
//
// "zorunlu" bayrağı YOKTUR çünkü bu modülde zorunlu sorgu parametresi de
// yoktur: süzgeçsiz bir liste anlamlıdır ("son bildirimler"). Bayrağı yine de
// taşımak, hiç kullanılmayan bir dalı belge üretimine sokardı.
func sorguParametresi(ad, tip, aciklama string) openapi.Parameter {
	return openapi.Parameter{
		Name:        ad,
		In:          "query",
		Required:    false,
		Schema:      map[string]any{semaTip: tip},
		Description: aciklama,
	}
}
