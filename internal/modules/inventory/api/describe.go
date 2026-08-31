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

// Describe inventory'nin TÜM uçlarını OpenAPI belgesine işler.
//
// # Neden bu pakette
//
// Anlatılan gövdeler bu paketin DIŞA KAPALI tipleridir (createItemRequest,
// inventoryLevelDTO …) ve şema onlardan yansımayla türetilir. Tipleri
// anlatabilmek için dışa açmak, yalnızca belge üretmek uğruna modülün
// yüzeyini genişletmek olurdu: dışa açık bir tip sözleşmedir ve dışarıdan
// kurulabilir hâle gelirdi. Sorgu parametreleri de handler'ın GERÇEKTEN
// okuduklarıdır ([parsePage], [Handler.listItems]); anlatım başka bir pakette
// dursaydı okumadan uzaklaşır ve ikisi sessizce ayrışırdı. Modülün
// [openapi.Describer] uygulaması bu yüzden buraya delege eder.
//
// # Neden paket düzeyinde bir fonksiyon
//
// Anlatım hiçbir çalışma zamanı durumuna bakmaz — şema TİPLERDEN gelir.
// Metodu [Handler]'a bağlamak, belgenin servis kurulmuş olmasına bağlı
// OLDUĞUNU söylerdi; oysa Routes hiç çalışmamışken de belge üretilebilir ve
// üretilmelidir.
//
// # Yalnızca /admin/v1 vardır
//
// Modülün vitrin ucu YOKTUR (bkz. paket belgesi): müşteri stoğu ürün
// listelemesi üzerinden, Query katmanının sağlayıcısıyla görür. Yani buradaki
// on uç modülün TAMAMIDIR; "vitrin anlatılmamış" diye okunmamalıdır.
//
// # Yol sabitleri route'larla ORTAKTIR
//
// Anlatım yolları [pathItems] gibi sabitlerle verilir, elle yazılmış dizelerle
// değil. Elle yazılsaydı bir yolun değişmesi anlatımı sessizce boşa
// düşürürdü; çekirdek bunu [openapi.Doc.UnmatchedDescriptions] ile raporlar
// ama rapor okunmayabilir — sabit, arızayı hiç doğurmaz.
//
// # Bilinen sınır: istek gövdelerinin "required" kümesi GENİŞTİR
//
// Çekirdek "required"ı encoding/json'un HER ZAMAN yazdığı alanlardan türetir
// ([openapi.Doc.SchemaOf]) ve bu, YANIT gövdeleri için doğru cevaptır. İstek
// gövdesinde ise "required" istemcinin GÖNDERMEK ZORUNDA olduğu alan demektir
// ve bunu tip bilemez: bu paketin istek tipleri omitempty taşımadığı için
// hepsi zorunlu görünür — örneğin POST /admin/v1/stock-locations, boş
// bırakılabilen address_2 ve province alanlarını da ister. Alan ADLARI ve
// TİPLERİ doğrudur, yani şema yanlış bir alan uydurmaz; yalnızca fazla şey
// ister. Doğru çözüm ÇEKİRDEKTEDİR (istek gövdeleri için ayrı bir "required"
// politikası); tag'lere omitempty serpiştirmek zorunluluğu servisin
// doğrulamasından json etiketine taşır ve ikisi sessizce ayrışırdı.
func Describe(d *openapi.Doc) {
	describeLokasyonlar(d)
	describeKalemler(d)
	describeSeviyeler(d)
}

// describeLokasyonlar stok lokasyonu uçlarını anlatır.
func describeLokasyonlar(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathStockLocations, openapi.Operation{
		Summary:     "Yeni bir stok lokasyonu oluşturur.",
		RequestBody: d.RequestBody(createStockLocationRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan lokasyon", d.Item(stockLocationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStockLocations, openapi.Operation{
		Summary: "Stok lokasyonlarını sayfalayarak listeler.",
		// [Handler.listStockLocations] sorgu dizesinden YALNIZCA bu ikisini
		// okur ([parsePage]); başka bir parametre yazmak istemciye çalışmayan
		// bir süzgeç vaat etmek olurdu.
		Parameters: sayfalamaParametreleri(),
		Responses: map[string]any{
			"200": openapi.Response("Lokasyon sayfası", d.List(stockLocationDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathStockLocation, openapi.Operation{
		Summary: "Tek bir stok lokasyonunu döner.",
		Responses: map[string]any{
			"200": openapi.Response("Stok lokasyonu", d.Item(stockLocationDTO{})),
		},
	})
}

// describeKalemler stok kalemi uçlarını anlatır.
func describeKalemler(d *openapi.Doc) {
	d.Describe(http.MethodPost, pathItems, openapi.Operation{
		Summary:     "Yeni bir stok kalemi oluşturur.",
		RequestBody: d.RequestBody(createItemRequest{}),
		Responses: map[string]any{
			"201": openapi.Response("Oluşturulan stok kalemi", d.Item(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathItems, openapi.Operation{
		Summary: "Stok kalemlerini süzerek ve sayfalayarak listeler.",
		// Parametreler [Handler.listItems]'ın OKUDUKLARIDIR: sayfalama ikilisi
		// ve iki süzgeç. requires_shipping MANTIKSAL bir değerdir ve şemada da
		// öyle görünür — dize olarak anlatılsaydı istemci üreteci "true"
		// yerine serbest metin gönderilebilen bir argüman üretir, sunucu da
		// çözemediği değeri hata sayardı.
		Parameters: append(sayfalamaParametreleri(),
			sorguParametresi("sku", tipDize,
				"Kalemleri tek bir SKU ile sınırlar."),
			sorguParametresi("requires_shipping", tipMantiksal,
				"true yalnızca gönderim gerektiren, false yalnızca gerektirmeyen kalemleri döner."),
		),
		Responses: map[string]any{
			"200": openapi.Response("Stok kalemi sayfası", d.List(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodGet, pathItem, openapi.Operation{
		Summary: "Tek bir stok kalemini döner.",
		Responses: map[string]any{
			"200": openapi.Response("Stok kalemi", d.Item(inventoryItemDTO{})),
		},
	})

	d.Describe(http.MethodDelete, pathItem, openapi.Operation{
		Summary: "Stok kalemini siler.",
		Responses: map[string]any{
			"204": bosYanit("Stok kalemi silindi"),
		},
	})
}

// describeSeviyeler stok seviyesi uçlarını anlatır.
//
// İkisi de YAZMA ucudur ve ikisi de 200 döner, 201 DEĞİL: seviye satırı
// kalem ile lokasyonun kesişiminde zaten vardır (ya da servis onu sessizce
// açar) ve uçlar yeni bir kaynak YARATMAZ, var olan adedi değiştirir (bkz.
// [Handler.setLevel], [Handler.adjustLevel]). 201 yazmak istemci üretecinde
// "oluşturuldu" dalına düşen bir metot üretirdi.
func describeSeviyeler(d *openapi.Doc) {
	d.Describe(http.MethodGet, pathItemLevels, openapi.Operation{
		Summary: "Kalemin tüm lokasyonlardaki stok seviyelerini döner.",
		// Uç SAYFALANMAZ ([Handler.listLevels] sorgu dizesini hiç okumaz) ama
		// zarfı yine liste zarfıdır: istemcinin gördüğü şekil uç noktaya göre
		// değişmez.
		Responses: map[string]any{
			"200": openapi.Response("Kalemin stok seviyeleri", d.List(inventoryLevelDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemLevels, openapi.Operation{
		Summary:     "Bir lokasyondaki FİZİKSEL stok adedini mutlak olarak yazar.",
		RequestBody: d.RequestBody(setLevelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Yazılan stok seviyesi", d.Item(inventoryLevelDTO{})),
		},
	})

	d.Describe(http.MethodPost, pathItemLevelAdjust, openapi.Operation{
		Summary:     "Bir lokasyondaki fiziksel stok adedini delta kadar değiştirir.",
		RequestBody: d.RequestBody(adjustLevelRequest{}),
		Responses: map[string]any{
			"200": openapi.Response("Güncellenen stok seviyesi", d.Item(inventoryLevelDTO{})),
		},
	})
}

// sayfalamaParametreleri [parsePage]'in okuduğu sorgu parametrelerini döner.
//
// İkisi de zorunlu DEĞİLDİR: verilmediklerinde varsayılan sayfa uygulanır
// (bkz. [parsePage], service.DefaultLimit).
//
// Her çağrıda YENİ bir dilim üretir; paket düzeyinde tek bir değer paylaşmak,
// çağıranlardan birinin append ile üzerine süzgeç eklediği yerde
// ([describeKalemler]) ötekinin listesini de sessizce değiştirebilirdi.
func sayfalamaParametreleri() []openapi.Parameter {
	return []openapi.Parameter{
		sorguParametresi("limit", tipTamSayi,
			"Sayfa boyutu; verilmezse varsayılan sayfa boyu uygulanır."),
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
// YOKTUR (bkz. [Handler.deleteItem], corehttp.WriteJSON'a nil verilen çağrı).
// Boş bir şema yazmak "bir şey dönüyor ama şekli bilinmiyor" demek olurdu ve
// istemci üreteci okunacak bir gövde bekleyen bir metot üretirdi.
func bosYanit(aciklama string) map[string]any {
	return map[string]any{"description": aciklama}
}
