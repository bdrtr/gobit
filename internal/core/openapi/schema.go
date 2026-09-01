package openapi

import (
	"encoding"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
)

// semaAlanData zarfların kayıt taşıyan alanının adıdır (plan Bölüm 8).
const semaAlanData = "data"

// codeSchemaNameConflict iki FARKLI Go tipinin aynı bileşen adını istediğini
// bildirir.
const codeSchemaNameConflict = "openapi_schema_name_conflict"

// Yansımayla tanınması gereken tipler.
//
// Paket düzeyinde tutulmalarının sebebi maliyet değil, tekrarın kendisidir:
// [reflect.TypeOf] çağrısını her alanda yeniden yazmak, birinde yanlış tip
// yazıldığında SESSİZ kalırdı.
var (
	// zamanTipi time.Time'ın yansıma tipidir.
	zamanTipi = reflect.TypeOf(time.Time{})
	// hamJSONTipi json.RawMessage'ın yansıma tipidir.
	hamJSONTipi = reflect.TypeOf(json.RawMessage(nil))
	// marshalerTipi json.Marshaler arayüzünün yansıma tipidir.
	marshalerTipi = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	// metinMarshalerTipi encoding.TextMarshaler arayüzünün yansıma tipidir.
	metinMarshalerTipi = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

// ayrilmisSemaAdlari çekirdeğin kendi bileşenlerinin adlarıdır.
//
// Türetilen bir şema bu adlardan birini alsaydı ORTAK hata zarfının ya da
// liste zarfının şemasını sessizce ezerdi: belgedeki her uç aynı "Error"
// bileşenine atıf yapar, yani tek bir modülün "Error" adlı DTO'su TÜM hata
// yanıtlarını yanlış anlatırdı.
var ayrilmisSemaAdlari = map[string]struct{}{
	semaAdiError: {},
	semaAdiList:  {},
}

// SchemaOf verilen değerin Go TİPİNDEN JSON Schema üretir.
//
// # Neden yansıma
//
// Gerekçe [Doc.Build]'inkiyle aynıdır: elle yazılan bir alan listesi, DTO'ya
// alan eklendiği gün eksik kalır ve kimse fark etmez. Tel üzerinde ne
// gönderildiğini bilen tek şey encoding/json'dur; bu yüzden şema, onun
// DAVRANIŞINDAN türetilir — json etiketi, gölgelenme, omitempty ve dışa
// kapalı alan kuralları burada birebir taklit edilir. Taklidin eksik olduğu
// yerde şema, hiç şema olmamasından KÖTÜDÜR: istemci, doğru sandığı bir alan
// adını gönderir.
//
// # Adlandırılmış struct'lar bileşendir
//
// Adı olan bir struct components/schemas altına kaydedilir ve burada yalnızca
// "$ref" döner. İki sebebi var. Birincisi ÖZYİNELEME: kendine referans veren
// bir tip (örn. kategori ağacı) satır içi yazılsaydı üretici sonsuz döngüye
// girerdi. Derinlik sınırı da bir çözümdü ama kestiği yerde "her şey serbest"
// yazmak zorunda kalırdı ve bu, tipli bir alanı istemci üretecinde 'any'
// yapardı — yani tam olarak kaçınmaya çalıştığımız yalan. $ref, sınırı hiç
// koymadan döngüyü kırar. İkincisi TEKRAR: aynı DTO yirmi uçta geçtiğinde
// belgede bir kez görünür ve istemci üreteci tek bir sınıf üretir.
//
// # Bilinen sınır
//
// json etiketindeki ",string" seçeneği (sayıyı JSON DİZESİ olarak yazar)
// taklit EDİLMEZ; böyle bir alan şemada sayı görünür. Bu depoda hiç
// kullanılmıyor ve her ek dal yanlış olabilecek bir yer daha demek. Sınırın
// yazılması bilinçlidir: eksik olduğunu bilmek, eksik olduğunu sanmamaktan
// iyidir.
func (d *Doc) SchemaOf(v any) map[string]any {
	return d.semaTipten(reflect.TypeOf(v), map[reflect.Type]bool{})
}

// Schemas türetilmiş bileşen şemalarını döner.
//
// [Doc.Build] bunları components/schemas altına yazar; ayrıca dışa
// verilmelerinin sebebi [Doc.SchemaOf]'un bir "$ref" döndürmesidir — atfın
// hedefini görmek isteyen çağıranın (ve testin) haritayı okuyabilmesi gerekir.
func (d *Doc) Schemas() map[string]any {
	kopya := make(map[string]any, len(d.semalar))
	for ad, sema := range d.semalar {
		kopya[ad] = sema
	}

	return kopya
}

// Item tekil yanıt zarfının şemasını KAYIT tipinden üretir.
//
// Zarf biçimi plan Bölüm 8'de sabittir: {semaAlanData: <kayıt>}. Her modülün kendi
// zarfını elle yazması, biçim bir gün değiştiğinde ötekilerin sessizce
// eskimesi demekti.
func (d *Doc) Item(v any) map[string]any {
	return tekilSemasi(d.SchemaOf(v))
}

// List liste yanıt zarfının şemasını KAYIT tipinden üretir.
//
// Zarf biçimi plan Bölüm 8'de sabittir:
// {semaAlanData: [...], "count": N, "offset": N, "limit": N}.
func (d *Doc) List(v any) map[string]any {
	oge := d.semaTipten(listeKaydi(reflect.TypeOf(v)), map[reflect.Type]bool{})

	return listeSemasi(oge)
}

// RequestBody verilen tipten ZORUNLU bir JSON istek gövdesi tanımı üretir.
func (d *Doc) RequestBody(v any) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{"schema": d.SchemaOf(v)},
		},
	}
}

// Response verilen şemayla bir JSON yanıt tanımı üretir.
//
// [Doc.Item] ve [Doc.List] ile birlikte kullanılır; ayrı durmasının sebebi
// aynı zarfın farklı durum kodlarıyla (200/201) anlatılabilmesidir.
func Response(aciklama string, sema map[string]any) map[string]any {
	return map[string]any{
		semaAciklama: aciklama,
		"content": map[string]any{
			"application/json": map[string]any{"schema": sema},
		},
	}
}

// listeKaydi [Doc.List]'e verilen değerin KAYIT tipini döner.
//
// Hem List(Product{}) hem List([]Product{}) aynı şeyi anlatır. İkinci biçim
// olduğu gibi kullanılsaydı dilim şeması bir kez daha diziye sarılır ve
// belgede "dizi dizisi" çıkardı; bu, kimse şemayı satır satır okumadan fark
// edilmezdi. Bayt dilimi DIŞARIDADIR: encoding/json onu base64 DİZE olarak
// yazar, dizi olarak değil.
func listeKaydi(t reflect.Type) reflect.Type {
	if t == nil {
		return nil
	}

	if k := t.Kind(); (k == reflect.Slice || k == reflect.Array) && !baytDilimi(t) {
		return t.Elem()
	}

	return t
}

// semaTipten tek bir tipin şemasını üretir.
//
// izlenen, O ANDAKİ özyineleme yolundaki tipleri tutar ve yalnızca $ref'e
// konamayan (struct olmayan) kendine referanslı tipler için gerekir; struct
// döngüleri bileşen kaydıyla zaten kırılır (bkz. [Doc.SchemaOf]).
func (d *Doc) semaTipten(t reflect.Type, izlenen map[reflect.Type]bool) map[string]any {
	if t == nil {
		// Tipi olmayan bir değer (nil arayüz) hakkında söylenebilecek doğru
		// şey yoktur; serbest şema "bilmiyorum" demenin dürüst yoludur.
		return map[string]any{}
	}

	// İşaretçi ÖNCE ele alınır. Aşağıdaki kodlayıcı denetimi işaretçiyi de
	// yakalardı ve sonuç YANLIŞ olurdu: time.Time'ın MarshalJSON'ı DEĞER
	// alıcılıdır, dolayısıyla *time.Time de onu taşır ve "şekli bilinmiyor"
	// denip serbest şema yazılırdı. Oysa *time.Time tel üzerinde ya RFC 3339
	// dizesi ya null'dır ve ikisi de söylenebilir.
	if t.Kind() == reflect.Pointer {
		return d.isaretciSemasi(t, izlenen)
	}

	switch {
	case t == zamanTipi:
		// time.Time'ın alanları dışa kapalıdır; yansıma onu BOŞ nesne sanırdı.
		// Tel üzerinde ise RFC 3339 dizesidir.
		return map[string]any{semaTip: tipDize, semaBicim: bicimTarihSaat}
	case t == hamJSONTipi:
		// Ham JSON'un şekli tanımı gereği bilinmez.
		return map[string]any{}
	case t.Implements(marshalerTipi):
		// Tip kendi kodlayıcısını taşıyorsa alanlarını okumak YALAN olurdu:
		// MarshalJSON istediği her şeyi yazabilir.
		//
		// İşaretçi ALICILI bir MarshalJSON değer tipinde aranmaz; encoding/json
		// de adreslenemeyen bir değerde onu çağırmaz (klasik tuzak), yani şema
		// tel üzerindeki davranışla aynı kalır.
		return map[string]any{}
	case t.Implements(metinMarshalerTipi):
		// TextMarshaler taşıyan bir tip JSON'a DİZE olarak yazılır.
		return map[string]any{semaTip: tipDize}
	}

	if k := t.Kind(); k == reflect.Slice || k == reflect.Array || k == reflect.Map {
		if izlenen[t] {
			// Kendine referans veren adlandırılmış bir dilim/harita bileşene
			// konamaz (bileşenler struct'lar içindir) ve satır içi yazılırsa
			// sonsuz döngüye girer. Serbest şema, "buradan sonrası kendini
			// tekrar ediyor" demenin en dürüst yoludur.
			return map[string]any{}
		}

		izlenen[t] = true
		defer delete(izlenen, t)
	}

	return d.semaKinden(t, izlenen)
}

// isaretciSemasi işaretçi tipinin şemasını üretir.
//
// Kodlayıcı YALNIZCA işaretçide varsa (işaretçi alıcılı MarshalJSON) öğeye
// inmek yalan olurdu: encoding/json böyle bir alanda gerçekten o kodlayıcıyı
// çağırır ve tel üzerindeki biçimi buradan bilemeyiz.
func (d *Doc) isaretciSemasi(t reflect.Type, izlenen map[reflect.Type]bool) map[string]any {
	oge := t.Elem()

	if ozelKodlayici(t) && !ozelKodlayici(oge) {
		return map[string]any{}
	}

	if izlenen[t] {
		return map[string]any{}
	}

	izlenen[t] = true
	defer delete(izlenen, t)

	return bosDegerli(d.semaTipten(oge, izlenen))
}

// ozelKodlayici tipin JSON biçimini KENDİSİNİN belirleyip belirlemediğini
// bildirir.
func ozelKodlayici(t reflect.Type) bool {
	return t.Implements(marshalerTipi) || t.Implements(metinMarshalerTipi)
}

// semaKinden tipin Go türüne (Kind) göre şemasını üretir.
func (d *Doc) semaKinden(t reflect.Type, izlenen map[reflect.Type]bool) map[string]any {
	switch t.Kind() {
	case reflect.Bool:
		return map[string]any{semaTip: tipMantiksal}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return tamSayiSemasi(t)
	case reflect.Float32:
		return map[string]any{semaTip: tipSayi, semaBicim: bicimFloat}
	case reflect.Float64:
		return map[string]any{semaTip: tipSayi, semaBicim: bicimDouble}
	case reflect.String:
		return map[string]any{semaTip: tipDize}
	case reflect.Slice, reflect.Array:
		if baytDilimi(t) {
			// encoding/json bayt dilimini base64 DİZE yazar; "dizi" demek,
			// istemcinin sayı listesi beklemesine yol açardı.
			return map[string]any{semaTip: tipDize, semaBicim: bicimBayt}
		}

		return map[string]any{semaTip: tipDizi, semaOgeler: d.semaTipten(t.Elem(), izlenen)}
	case reflect.Map:
		// Anahtar tipi şemaya girmez: JSON nesne anahtarı her zaman dizedir ve
		// encoding/json sayısal/TextMarshaler anahtarları da dizeye çevirir.
		return map[string]any{
			semaTip:          tipNesne,
			semaEkOzellikler: d.semaTipten(t.Elem(), izlenen),
		}
	case reflect.Struct:
		return d.structSemasiVeyaRef(t, izlenen)
	case reflect.Pointer:
		return bosDegerli(d.semaTipten(t.Elem(), izlenen))
	case reflect.Interface:
		return map[string]any{}
	case reflect.Invalid, reflect.Complex64, reflect.Complex128, reflect.Chan,
		reflect.Func, reflect.UnsafePointer:
		// encoding/json bu tipleri kodlayamaz; şemanın yapabileceği tek doğru
		// şey bir şey İDDİA ETMEMEKTİR.
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

// tamSayiSemasi tam sayı tipinin şemasını genişliğiyle birlikte döner.
//
// Genişlik ("format") bilgi amaçlı değildir: int64 bir JSON sayısıdır ve
// JavaScript onu 2^53'ten sonra SESSİZCE bozar. İstemci üreteçleri format'ı
// görünce 64 bitlik bir tip (long, BigInt) seçer.
func tamSayiSemasi(t reflect.Type) map[string]any {
	sema := map[string]any{semaTip: tipTamSayi}

	switch t.Bits() {
	case 32:
		sema[semaBicim] = bicimInt32
	case 64:
		sema[semaBicim] = bicimInt64
	}

	return sema
}

// baytDilimi tipin encoding/json tarafından base64 DİZE yazılan bir bayt
// dilimi olup olmadığını bildirir.
func baytDilimi(t reflect.Type) bool {
	if t.Kind() != reflect.Slice {
		return false
	}

	oge := t.Elem()
	if oge.Kind() != reflect.Uint8 {
		return false
	}

	// Öğe tipinin kendi kodlayıcısı varsa encoding/json base64'e düşmez,
	// diziyi öğe öğe yazar.
	return !oge.Implements(marshalerTipi) && !oge.Implements(metinMarshalerTipi)
}

// bosDegerli şemaya JSON null'ını ekler.
//
// İşaretçi tipinin nil olabilmesi bir kaza değil, yazarın seçimidir: alanı
// işaretçi yapmanın tek sebebi "yok olabilir" demektir. Dilim ve haritalar
// BİLİNÇLİ olarak null'lanmaz; onların nil'liği Go'nun sıfır değerinden gelen
// bir kazadır ve API sözleşmesi onları BOŞ sayar. Takas açıkça kabul
// ediliyor: nil bırakılmış bir dilim alanı tel üzerinde null olur ve şema
// bunu söylemez — karşılığında "her liste null olabilir" dalını her istemciye
// yazdırmamış oluruz.
func bosDegerli(sema map[string]any) map[string]any {
	if len(sema) == 0 {
		// Serbest şema null'ı zaten kabul eder.
		return sema
	}

	if _, refli := sema[semaRef]; refli {
		// "$ref"in yanına type yazmak JSON Schema 2020-12'de $ref ile
		// BİRLİKTE değerlendirilir: null hem hedef şemaya hem type'a uymak
		// zorunda kalır ve hiçbir değer geçemez. Doğrusu anyOf'tur.
		return map[string]any{semaHerhangi: []any{sema, map[string]any{semaTip: tipBos}}}
	}

	switch tip := sema[semaTip].(type) {
	case string:
		sema[semaTip] = []any{tip, tipBos}
	case []any:
		for _, mevcut := range tip {
			if mevcut == tipBos {
				return sema
			}
		}

		sema[semaTip] = append(tip, tipBos)
	}

	return sema
}

// bilesenAdi Go tip adını YAYIMLANAN şema bileşeni adına çevirir.
//
// # Neden Go adı olduğu gibi kullanılamaz
//
// Bileşen adı bir İÇ AYRINTI DEĞİL, YAYIMLANAN SÖZLEŞMEDİR: istemci üreteçleri
// ondan sınıf adı üretir ve bir kez istemci üretildikten sonra adı değiştirmek
// kırıcıdır. Go adı olduğu gibi kullanılsaydı sözleşme, Go'nun dışa açma
// kuralına ve paket içi adlandırma alışkanlığına bağlı kalırdı: aynı belgede
// "StoreProduct" (models paketinden, dışa açık) ile "cartDTO" (api paketinden,
// dışa kapalı) yan yana dururdu. Üretilen istemcide bir sınıf StoreProduct,
// öteki cartDTO olurdu — aynı API'nin iki farklı adlandırma düzeni.
//
// İki normalleştirme yapılır ve ikisi de KAYIPSIZDIR:
//
//   - Baş harf büyütülür: dışa kapalı olmak bir Go kavramıdır, HTTP
//     sözleşmesinin değil.
//   - Sondaki "DTO" atılır: taşıma nesnesi olmak da bir Go kavramıdır.
//     İstemci "Cart" ister, "cartDTO" değil.
//
// Çakışma riski vardır (örn. hem "cartDTO" hem "Cart" tipi olsaydı ikisi de
// "Cart" isterdi) ve SESSİZ DEĞİLDİR: [Doc.cakismaBildir] iki tipin aynı adı
// istediğini bildirir ve belge üretimi hata döner. Sessiz ezilme, bir DTO'nun
// şemasının başka bir tiple anlatılması demek olurdu.
func bilesenAdi(goAdi string) string {
	if goAdi == "" {
		return ""
	}

	ad := strings.TrimSuffix(goAdi, "DTO")
	if ad == "" {
		// Adı yalnızca "DTO" olan bir tip; kırpma onu yok ederdi.
		ad = goAdi
	}

	r := []rune(ad)
	r[0] = unicode.ToUpper(r[0])

	return string(r)
}

// structSemasiVeyaRef adlandırılmış struct'ı bileşene kaydedip "$ref" döner.
func (d *Doc) structSemasiVeyaRef(t reflect.Type, izlenen map[reflect.Type]bool) map[string]any {
	ad := bilesenAdi(t.Name())
	if ad == "" {
		// Anonim struct'ın adı yoktur, bileşene konamaz — ama KENDİNE de
		// referans veremez, dolayısıyla satır içi yazmak güvenlidir.
		return d.structSemasi(t, izlenen)
	}

	if _, ayrilmis := ayrilmisSemaAdlari[ad]; ayrilmis {
		d.cakismaBildir(ad + " adı çekirdeğin ortak bileşenine ait; " + t.PkgPath() + " bu adı kullanamaz")

		return map[string]any{}
	}

	if sahip, kayitli := d.semaSahipleri[ad]; kayitli {
		if sahip != t {
			d.cakismaBildir(ad + " adını iki tip istiyor: " + sahip.PkgPath() + " ve " + t.PkgPath())

			return map[string]any{}
		}

		return refSemasi(ad)
	}

	// Kayıt, alanlara İNMEDEN ÖNCE yapılır: kendine referans veren bir tip
	// aşağıda yeniden buraya geldiğinde adı bulur ve $ref ile döner. Sıra ters
	// olsaydı özyineleme hiç bitmezdi.
	d.semaSahipleri[ad] = t
	d.semalar[ad] = map[string]any{}
	d.semalar[ad] = d.structSemasi(t, izlenen)
	// Bileşen kümesi belgenin ikinci girdisidir; üretilmiş bir belge artık
	// eksik kalmıştır (bkz. [Doc.Handler]).
	d.anlatimSurumu++

	return refSemasi(ad)
}

// structSemasi struct'ın alanlarından nesne şemasını kurar.
func (d *Doc) structSemasi(t reflect.Type, izlenen map[reflect.Type]bool) map[string]any {
	ozellikler := map[string]any{}

	var zorunlu []string

	for _, a := range jsonAlanlari(t) {
		ozellikler[a.ad] = d.semaTipten(a.tip, izlenen)

		if !a.atlanabilir {
			zorunlu = append(zorunlu, a.ad)
		}
	}

	sema := map[string]any{semaTip: tipNesne, semaOzellikler: ozellikler}

	if len(zorunlu) > 0 {
		sort.Strings(zorunlu)
		sema[semaZorunlu] = zorunlu
	}

	return sema
}

// cakismaBildir bir bileşen adı çakışmasını kaydeder.
//
// Çakışma [Doc.Build]'i BAŞARISIZ kılar; sessizce ezmek, iki uçtan birinin
// şemasının yanlış olması ve bunun ancak istemci yanlış alan gönderdiğinde
// anlaşılması demekti.
func (d *Doc) cakismaBildir(mesaj string) {
	for _, mevcut := range d.semaCakismalari {
		if mevcut == mesaj {
			return
		}
	}

	d.semaCakismalari = append(d.semaCakismalari, mesaj)
	// Çakışma belgeyi üretilemez kılar; önbellekteki sağlam belge artık
	// GEÇERSİZDİR (bkz. [Doc.Handler]).
	d.anlatimSurumu++
}

// refSemasi bir bileşene atıf şeması üretir.
func refSemasi(ad string) map[string]any {
	return map[string]any{semaRef: "#/components/schemas/" + ad}
}

// alan bir struct alanının şema üretimi için gereken bilgisidir.
type alan struct {
	// ad alanın JSON'daki adıdır.
	ad string
	// tip alanın Go tipidir (işaretçi sarmalayıcısı KORUNUR; nullable ondan
	// türer).
	tip reflect.Type
	// derinlik alanın gömülme derinliğidir; gölgelenmede sığ olan kazanır.
	derinlik int
	// etiketli alanın json etiketiyle ADLANDIRILMIŞ olduğunu bildirir; aynı
	// derinlikte etiketli olan etiketsizi yener.
	etiketli bool
	// atlanabilir alanın JSON'dan düşebileceğini bildirir (omitempty/omitzero).
	atlanabilir bool
}

// jsonAlanlari encoding/json'un bir struct için ÜRETECEĞİ alan kümesini döner.
//
// Uygulama encoding/json'un typeFields algoritmasını izler ve iki kuralı
// birebir taşır:
//
//   - GÖMÜLÜ struct'ların alanları düzleştirilir (json etiketiyle
//     adlandırılmış gömülü alan ise düz bir alandır, düzleştirilmez).
//   - GÖLGELENME: aynı ada sahip alanlardan SIĞ olanı kazanır; eşit
//     derinlikte tek bir etiketli varsa o kazanır, yoksa hepsi DÜŞER.
//
// İkinci kural bu paketin varlık sebebine dokunur: service.StoreProduct
// gömülü Product'ın Variants alanını gölgeler ve encoding/json yalnızca
// gölgeleyeni yazar. Şema gölgelenen alanı yazsaydı, istemci üreteci ürün
// varyantlarını YANLIŞ tiple üretirdi.
func jsonAlanlari(t reflect.Type) []alan {
	toplayici := &alanToplayici{sonrakiSayim: map[reflect.Type]int{}}

	gorulen := map[reflect.Type]bool{}
	gecerli := []reflect.Type{}
	sonraki := []reflect.Type{t}

	// sayim ATAMASIZ bildirilir: döngünün ilk turu onu toplayıcının haritasıyla
	// takas eder, yani buraya konan her değer okunmadan atılırdı.
	// encoding/json'un aynı algoritmasında da bildirim atamasızdır.
	var sayim map[reflect.Type]int

	for derinlik := 0; len(sonraki) > 0; derinlik++ {
		gecerli, sonraki = sonraki, gecerli[:0]
		sayim, toplayici.sonrakiSayim = toplayici.sonrakiSayim, map[reflect.Type]int{}
		toplayici.sonraki = sonraki

		for _, tip := range gecerli {
			if gorulen[tip] {
				continue
			}

			gorulen[tip] = true
			toplayici.tara(tip, derinlik, sayim[tip])
		}

		sonraki = toplayici.sonraki
	}

	return golgelenmemisler(toplayici.bulunan)
}

// alanToplayici gömülü struct taramasının bir seviyedeki durumudur.
type alanToplayici struct {
	// bulunan o ana kadar toplanmış alanlardır (henüz gölgelenme uygulanmadan).
	bulunan []alan
	// sonraki bir sonraki seviyede taranacak gömülü struct tipleridir.
	sonraki []reflect.Type
	// sonrakiSayim bir gömülü tipe bu seviyeden kaç yoldan ulaşıldığını tutar.
	sonrakiSayim map[reflect.Type]int
}

// tara tek bir struct'ın alanlarını toplar ve gömülüleri kuyruğa alır.
//
// tekrar, bu tipe BİR ÖNCEKİ seviyeden kaç ayrı yoldan ulaşıldığıdır.
func (c *alanToplayici) tara(t reflect.Type, derinlik, tekrar int) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		gomuluTip := sf.Type
		if gomuluTip.Kind() == reflect.Pointer {
			gomuluTip = gomuluTip.Elem()
		}

		if !alanGorunur(sf, gomuluTip) {
			continue
		}

		etiket := sf.Tag.Get("json")
		if etiket == "-" {
			continue
		}

		ad, secenekler, _ := strings.Cut(etiket, ",")
		if !gecerliEtiketAdi(ad) {
			ad = ""
		}

		// Adı olan ya da struct OLMAYAN gömülü alan, düz bir alandır.
		if ad != "" || !sf.Anonymous || gomuluTip.Kind() != reflect.Struct {
			etiketli := ad != ""
			if ad == "" {
				ad = sf.Name
			}

			c.bulunan = append(c.bulunan, alan{
				ad:          ad,
				tip:         sf.Type,
				derinlik:    derinlik,
				etiketli:    etiketli,
				atlanabilir: secenekVar(secenekler, "omitempty") || secenekVar(secenekler, "omitzero"),
			})

			// Aynı gömülü tipe bu seviyede birden çok yoldan ulaşıldıysa alan
			// da birden çok kez görünür; kopyayı eklemek yok etme kuralının
			// (bkz. [golgelenmemisler]) belirsizliği görmesini sağlar.
			if tekrar > 1 {
				c.bulunan = append(c.bulunan, c.bulunan[len(c.bulunan)-1])
			}

			continue
		}

		c.sonrakiSayim[gomuluTip]++
		if c.sonrakiSayim[gomuluTip] == 1 {
			c.sonraki = append(c.sonraki, gomuluTip)
		}
	}
}

// alanGorunur alanın encoding/json tarafından ele alınıp alınmadığını bildirir.
//
// Dışa kapalı alanlar yazılmaz — TEK istisnası dışa kapalı bir tipin GÖMÜLÜ
// hâlidir: encoding/json onun içindeki dışa AÇIK alanları yazar, dolayısıyla
// şema da yazmalıdır.
func alanGorunur(sf reflect.StructField, gomuluTip reflect.Type) bool {
	if sf.Anonymous {
		return sf.IsExported() || gomuluTip.Kind() == reflect.Struct
	}

	return sf.IsExported()
}

// golgelenmemisler aynı adı isteyen alanlardan kazananı seçer.
func golgelenmemisler(bulunan []alan) []alan {
	sort.Slice(bulunan, func(i, j int) bool {
		a, b := bulunan[i], bulunan[j]

		if a.ad != b.ad {
			return a.ad < b.ad
		}

		if a.derinlik != b.derinlik {
			return a.derinlik < b.derinlik
		}

		return a.etiketli && !b.etiketli
	})

	var sonuc []alan

	for i := 0; i < len(bulunan); {
		j := i + 1
		for j < len(bulunan) && bulunan[j].ad == bulunan[i].ad {
			j++
		}

		if kazanan, ok := baskinAlan(bulunan[i:j]); ok {
			sonuc = append(sonuc, kazanan)
		}

		i = j
	}

	return sonuc
}

// baskinAlan aynı adlı adaylardan kazananı döner.
//
// Adaylar sığdan derine, eşit derinlikte önce etiketli olacak şekilde
// sıralıdır. İlk iki aday hem derinlikte hem etiketlilikte eşitse kazanan
// YOKTUR: encoding/json belirsiz kalan böyle bir alanı HİÇ yazmaz ve şema da
// yazmamalıdır.
func baskinAlan(adaylar []alan) (alan, bool) {
	if len(adaylar) > 1 &&
		adaylar[0].derinlik == adaylar[1].derinlik &&
		adaylar[0].etiketli == adaylar[1].etiketli {
		return alan{}, false
	}

	return adaylar[0], true
}

// gecerliEtiketAdi json etiketindeki adın encoding/json tarafından kabul
// edilip edilmediğini bildirir.
//
// Kabul edilmeyen bir ad YOK SAYILIR ve alan Go adıyla yazılır; şemanın da
// aynı şeyi yapması gerekir.
func gecerliEtiketAdi(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range s {
		switch {
		case strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", c):
			// encoding/json'un açıkça izin verdiği noktalama.
		case !unicode.IsLetter(c) && !unicode.IsDigit(c):
			return false
		}
	}

	return true
}

// secenekVar json etiketinin seçenek listesinde bir seçeneğin bulunup
// bulunmadığını bildirir.
func secenekVar(secenekler, ad string) bool {
	for secenekler != "" {
		var s string

		s, secenekler, _ = strings.Cut(secenekler, ",")
		if s == ad {
			return true
		}
	}

	return false
}

// tekilSemasi tekil yanıt zarfını verilen kayıt şemasıyla kurar.
func tekilSemasi(kayit map[string]any) map[string]any {
	return map[string]any{
		semaTip:        tipNesne,
		semaZorunlu:    []string{semaAlanData},
		semaOzellikler: map[string]any{semaAlanData: kayit},
	}
}

// listeSemasi liste yanıt zarfını verilen öğe şemasıyla kurar.
func listeSemasi(oge map[string]any) map[string]any {
	return map[string]any{
		semaTip:     tipNesne,
		semaZorunlu: []string{"data", "count", "offset", "limit"},
		semaOzellikler: map[string]any{
			semaAlanData: map[string]any{semaTip: tipDizi, semaOgeler: oge},
			"count":      map[string]any{semaTip: tipTamSayi},
			"offset":     map[string]any{semaTip: tipTamSayi},
			"limit":      map[string]any{semaTip: tipTamSayi},
		},
	}
}
