// Package link modüller arası ilişkiyi foreign key OLMADAN kuran katmandır.
//
// Plan Bölüm 2.2 farklı modüllerin tabloları arasında foreign key kurulmasını
// yasaklar: her veri tam olarak bir modüle aittir ve o modül ileride ayrı bir
// servise çıkarılabilmelidir. Bir FK, iki modülün tablolarını aynı veritabanına
// ve aynı yaşam döngüsüne çiviler.
//
// Bu paket ilişkiyi ÜÇÜNCÜ bir tabloya taşır: her link kendi tablosunda yaşar
// (örn. "link_product_price") ve o tablo hiçbir modülün tablosuna REFERENCES
// VERMEZ. Link tablosu yalnızca iki serbest kimlik dizgesi tutar; kimliklerin
// gerçekten var olup olmadığı sahibi modülün sorumluluğundadır ve gerekirse
// workflow'un telafi (compensation) adımıyla temizlenir. Böylece:
//
//   - Modüller birbirinin şemasını tanımak zorunda kalmaz.
//   - Bir modülün tablosu bırakılıp yeniden yaratılabilir; link tablosu
//     etkilenmez (FK olsaydı DROP engellenirdi).
//   - İlişki, modül ayrı servise çıktığında da aynı yüzeyle çalışmaya devam
//     eder; değişen tek şey link tablosunun nerede durduğudur.
//
// # Şema neden migration dosyasında değil
//
// Link tabloları statik bir küme değildir: hangi linklerin var olduğunu
// MODÜLLER açılışta [LinkService.Define] ile bildirir (plan Bölüm 5.1,
// Module.Register) ve bir plugin kendi linkini çekirdeğe dokunmadan
// ekleyebilmelidir. Bu yüzden şema, sabit bir migration dosyasında değil,
// bildirim anında ve idempotent olarak (CREATE ... IF NOT EXISTS) kurulur.
// Bildirimin kendisi tek bir işlemde ve danışma kilidi altında yürür; aynı anda
// açılan iki süreç birbirinin DDL'iyle yarışmaz.
//
// Bunun bedeli, tanımın kalıcı bir deftere (link_definitions) yazılıp her
// açılışta karşılaştırılmasıdır: sürümler arasında sessizce değişen bir tanım
// böyle yakalanır.
//
// # Kardinalite veritabanı kısıtıyla zorlanır
//
// [Cardinality] uygulama katmanında "önce oku sonra yaz" ile değil, benzersiz
// indeksle zorlanır. Uygulama katmanı kontrolü eşzamanlı iki istek arasında
// yarışa açıktır (ikisi de okur, ikisi de yazar); indeks yarışı veritabanının
// kendisine bırakır ve ihlali tipli bir errors.Conflict'e çevirir.
//
// # Tablo adı ve enjeksiyon
//
// Tablo adları SQL'de parametrelenemez; ad zorunlu olarak dizge birleştirmesiyle
// üretilir. Bu yüzden link adı [LinkDefinition.Validate] içinde katı bir desene
// göre doğrulanır ve tablo adı YALNIZCA [Define] anında, doğrulanmış addan bir
// kez üretilir (bkz. [TableName]). Çalışma zamanındaki Create/Delete/List
// yolları önceden üretilmiş ifadeleri kullanır; kullanıcıdan gelen hiçbir dizge
// SQL metnine karışmaz. Aynı titizliğin migration tarafındaki karşılığı için
// bkz. internal/core/db MigrationsTable.
package link

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// Hata kodları; çağıran taraf errors.CodeOf ile bunlara bakabilir.
const (
	codeNameInvalid          = "link_name_invalid"
	codeSideInvalid          = "link_side_invalid"
	codeCardinalityInvalid   = "link_cardinality_invalid"
	codeIDInvalid            = "link_id_invalid"
	codeNotDefined           = "link_not_defined"
	codeDefinitionConflict   = "link_definition_conflict"
	codeCardinalityViolation = "link_cardinality_violation"
	codeUnavailable          = "link_db_unavailable"
	codeDefineFailed         = "link_define_failed"
	codeQueryFailed          = "link_query_failed"
	codeCanceled             = "link_canceled"
)

const (
	// tablePrefix link tablolarının ortak önekidir. Önek, link tablolarını
	// modüllerin kendi tablolarından tek bakışta ayırır.
	tablePrefix = "link_"
	// definitionsTable link tanımlarının kalıcı kayıt defteridir.
	definitionsTable = tablePrefix + "definitions"
	// fromIndexSuffix ve toIndexSuffix kardinaliteyi zorlayan indekslerin
	// adlarını üretir. Adlar hata eşlemesinde de kullanılır: PostgreSQL ihlal
	// eden kısıtın adını bildirir, biz de hangi ucun ihlal edildiğini yazarız.
	fromIndexSuffix = "_from_uniq"
	toIndexSuffix   = "_to_uniq"
	// toLookupSuffix ManyToMany'de ters yön aramasını hızlandıran benzersiz
	// OLMAYAN indeksin sonekidir.
	toLookupSuffix = "_to_lookup"
	// relkindTable ve relkindIndex pg_class.relkind değerleridir.
	relkindTable = "r"
	relkindIndex = "i"
	// maxNameLen link adının azami uzunluğudur. PostgreSQL tanımlayıcıları 63
	// bayta kırpar; en uzun türetilmiş ad tablePrefix + ad + toIndexSuffix
	// (5 + 40 + 10 = 55) olduğu için 40 sınırı sessiz kırpılmayı imkânsız kılar.
	maxNameLen = 40
	// maxIDLen bağlanan kimliklerin azami uzunluğudur. Kimlikler benzersiz
	// btree indekse girer; indeks girdisi ~2704 baytı aşarsa PostgreSQL anlaşılmaz
	// bir hata verir. Sınır, bunu okunabilir bir doğrulama hatasına çevirir
	// (plan Bölüm 8'deki önekli ULID/KSUID kimlikler ~30 karakterdir).
	maxIDLen = 255
)

// namePattern link, modül ve alan adlarının uyması gereken desendir.
//
// internal/core/db'deki modül adı deseniyle bilinçli olarak aynıdır: her ikisi
// de doğrulanmamış bir dizgenin SQL tanımlayıcısına dönüşmesini engeller.
// Büyük harf ve tırnak yasağı, alıntılanmamış tanımlayıcıların PostgreSQL
// tarafından küçük harfe indirgenmesinden doğacak sürprizleri de kapatır.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,` + fmt.Sprint(maxNameLen-1) + `}$`)

// reservedNames link adı olarak kullanılamayacak adlardır.
//
// "definitions" adı [definitionsTable] ile aynı tabloya çözülürdü; link kendi
// kayıt defterinin üstüne yazardı. Bu yüzden ad, defter adından TÜRETİLİR;
// defterin adı değişirse yasak da kendiliğinden değişir.
var reservedNames = []string{strings.TrimPrefix(definitionsTable, tablePrefix)}

// Cardinality bir linkin kaç kayda bağlanabileceğini belirler.
//
// Sıfır değeri [OneToOne]'dır; yani bildirilmemiş bir kardinalite EN KATI
// kısıtı seçer. Tersi (serbest ManyToMany) sıfır değer olsaydı, eksik bir
// bildirim sessizce fazladan bağ oluşmasına izin verir ve hata ancak veri
// bozulduktan sonra fark edilirdi.
type Cardinality uint8

// Kardinalite türleri.
const (
	// OneToOne her iki uçta da benzersizliktir: bir fromID tek bir toID'ye,
	// bir toID tek bir fromID'ye bağlanabilir.
	OneToOne Cardinality = iota
	// OneToMany bir fromID'nin çok sayıda toID'ye bağlanmasına izin verir,
	// ama bir toID yalnızca tek bir fromID'ye bağlanabilir.
	OneToMany
	// ManyToMany serbesttir; yalnızca (fromID, toID) çifti benzersizdir.
	ManyToMany
)

// String Cardinality'nin okunabilir adını döner.
//
// Bu gösterim kayıt defterine YAZILDIĞI için kararlıdır: sayısal iota değeri
// saklansaydı, sabitlerin arasına yeni bir tür eklemek diskteki tüm tanımların
// anlamını sessizce kaydırırdı.
func (c Cardinality) String() string {
	switch c {
	case OneToOne:
		return "one_to_one"
	case OneToMany:
		return "one_to_many"
	case ManyToMany:
		return "many_to_many"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(c))
	}
}

// Valid değerin tanımlı bir kardinalite olup olmadığını bildirir.
func (c Cardinality) Valid() bool {
	return c == OneToOne || c == OneToMany || c == ManyToMany
}

// LinkSide bir linkin tek ucudur: hangi modülün hangi alanı bağlanıyor.
//
// Field, link tablosunda bir SÜTUN ADI DEĞİLDİR (bkz. [TableName] yorumları);
// bağlanan kimliğin sahibi modüldeki karşılığını bildiren üstveridir. Query
// katmanı (plan Bölüm 5.3, ADR 0004) kök kayıttaki hangi alanın link'e girdiğini
// bu bilgiyle bulur.
type LinkSide struct { //nolint:revive // ad, plan Bölüm 5.2'deki bağlayıcı sözleşmeden gelir
	// Module ucun sahibi modülün adıdır (örn. "product").
	Module string
	// Field o modüldeki kimlik alanının adıdır (örn. "product_id").
	Field string
}

// String ucu "modül.alan" biçiminde yazar.
func (s LinkSide) String() string {
	return s.Module + "." + s.Field
}

// LinkDefinition iki modül arasındaki bir ilişkinin bildirimidir.
//
// Tanım DEĞİŞMEZ sayılır: aynı adla farklı bir tanım bildirmek errors.Conflict
// üretir (bkz. [LinkService.Define]).
type LinkDefinition struct { //nolint:revive // ad, plan Bölüm 5.2'deki bağlayıcı sözleşmeden gelir
	// Name linkin benzersiz adıdır (örn. "product_price"); tablo adı bundan
	// türetilir.
	Name string
	// From linkin kaynak ucudur.
	From LinkSide
	// To linkin hedef ucudur.
	To LinkSide
	// Cardinality ilişkinin çokluğudur; veritabanı kısıtına çevrilir.
	Cardinality Cardinality
}

// String tanımı hata ve log mesajlarında okunabilir biçimde yazar.
func (d LinkDefinition) String() string {
	return fmt.Sprintf("%s(%s -> %s, %s)", d.Name, d.From, d.To, d.Cardinality)
}

// Validate tanımın tutarlı ve SQL tanımlayıcısına güvenle çevrilebilir
// olduğunu doğrular.
//
// Geçersiz her durum errors.Invalid sınıfında döner. Doğrulama Define'ın en
// başında çalışır; geçersiz bir ad veritabanına HİÇ ulaşmaz.
func (d LinkDefinition) Validate() error {
	if err := validateName(d.Name); err != nil {
		return err
	}
	if err := validateSide(d.From, "From"); err != nil {
		return err
	}
	if err := validateSide(d.To, "To"); err != nil {
		return err
	}
	if !d.Cardinality.Valid() {
		return errors.Invalid(codeCardinalityInvalid,
			"%q linkinin kardinalitesi tanımsız (%s)", d.Name, d.Cardinality)
	}
	return nil
}

// validateName link adının tablo adına güvenle çevrilebileceğini doğrular.
func validateName(name string) error {
	if !namePattern.MatchString(name) {
		return errors.Invalid(codeNameInvalid,
			"geçersiz link adı %q (beklenen desen: %s)", name, namePattern.String())
	}
	for _, reserved := range reservedNames {
		if name == reserved {
			return errors.Invalid(codeNameInvalid,
				"%q ayrılmış bir link adıdır; %s tablosuyla çakışır", name, definitionsTable)
		}
	}
	// PostgreSQL'de tablolar ve indeksler AYNI ad uzayını (pg_class) paylaşır.
	// "x_from_uniq" adlı bir link, "x" linkinin benzersizlik indeksiyle aynı
	// ilişki adına çözülür; CREATE ... IF NOT EXISTS bu durumda hata değil
	// NOTICE üretip ATLAR, yani kardinalite kısıtı sessizce hiç kurulmaz.
	// Sonekler sabitlerden türetilir ki adlandırma şeması değişirse yasak da
	// kendiliğinden değişsin.
	for _, suffix := range []string{fromIndexSuffix, toIndexSuffix, toLookupSuffix} {
		if strings.HasSuffix(name, suffix) {
			return errors.Invalid(codeNameInvalid,
				"%q adı link indeks ad uzayıyla çakışır (%q soneki ayrılmıştır)", name, suffix)
		}
	}
	return nil
}

// validateSide bir ucun modül ve alan adlarını doğrular. label hata mesajında
// hangi ucun kastedildiğini söyler.
func validateSide(side LinkSide, label string) error {
	if !namePattern.MatchString(side.Module) {
		return errors.Invalid(codeSideInvalid,
			"%s ucunun modül adı geçersiz: %q (beklenen desen: %s)",
			label, side.Module, namePattern.String())
	}
	if !namePattern.MatchString(side.Field) {
		return errors.Invalid(codeSideInvalid,
			"%s ucunun alan adı geçersiz: %q (beklenen desen: %s)",
			label, side.Field, namePattern.String())
	}
	return nil
}

// validateID bağlanan bir kimliğin kullanılabilir olduğunu doğrular.
//
// Kimlikler SQL'e PARAMETRE olarak gider, yani enjeksiyon riski taşımaz;
// buradaki doğrulama anlamsız kayıtları (boş dizge, yalnızca boşluk, baş/son
// boşluk) ve indeks sınırını aşan devasa kimlikleri erken yakalamak içindir.
// label hata mesajında hangi ucun kastedildiğini söyler.
//
// Boşluklu kimlik KIRPILMAZ, reddedilir; gerekçe için bkz. [LinkService].
func validateID(id, label string) error {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return errors.Invalid(codeIDInvalid, "%s boş olamaz", label)
	}
	if trimmed != id {
		return errors.Invalid(codeIDInvalid, "%s baş/son boşluk içeremez: %q", label, id)
	}
	if len(id) > maxIDLen {
		return errors.Invalid(codeIDInvalid,
			"%s en fazla %d bayt olabilir, %d bayt verildi", label, maxIDLen, len(id))
	}
	return nil
}

// TableName link adından tablo adını üretir.
//
// Ad BURADA doğrulanır ve geçersizse boş ad ile birlikte errors.Invalid döner.
// Doğrulamanın fonksiyonun kendisinde olması, dışarıdan gelen bir adın
// doğrulanmadan tablo adına dönüşmesini yapısal olarak imkânsız kılar
// (internal/core/db MigrationsTable ile aynı gerekçe).
func TableName(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return tablePrefix + name, nil
}

// LinkService modüller arası bağların tanımlanmasını ve yönetilmesini sağlar
// (plan Bölüm 5.2).
//
// Tüm metodlar goroutine-güvenlidir.
//
// # Kimlik sözleşmesi
//
// Bağlanan kimlikler serbest dizgelerdir (link hiçbir modülün şemasını
// tanımaz), ama boş olamaz, BAŞ/SON BOŞLUK İÇEREMEZ ve azami uzunluğu aşamaz;
// ihlal errors.Invalid döner. Boşluk yasağı sessiz veri kaybını kapatır: dış
// kaynaktan (CSV, HTTP başlığı, JSON) sonuna \n almış "var_1\n" ile kurulan
// bir bağ, temiz "var_1" ile HİÇ okunamaz — List boş dilim döner, bu sözleşme
// gereği hata değildir ve telafi adımının Delete'i de no-op olduğu için satır
// kalıcı olarak yetim kalırdı. Aynı kayma "var_1" ile "var_1\n"yi iki ayrı uç
// sayarak [OneToOne] ve [OneToMany] kısıtlarını da fiilen delerdi.
//
// Kimlik sessizce kırpılmaz: kırpma, çağıranın gönderdiği kimlikle saklanan
// kimliği ayırır ve fark ancak veri bozulduktan sonra görünür.
type LinkService interface { //nolint:revive // ad, plan Bölüm 5.2'deki bağlayıcı sözleşmeden gelir
	// Define bir link tanımını bildirir ve tablosunu (yoksa) oluşturur.
	//
	// Çağrı idempotenttir: aynı tanım her açılışta yeniden bildirilebilir.
	// AYNI ADLA FARKLI bir tanım bildirilirse errors.Conflict döner — çünkü
	// tanım değişikliği (özellikle kardinalite) var olan veriyi taşımayı
	// gerektirir ve sessizce yapılamaz.
	Define(ctx context.Context, def LinkDefinition) error

	// Create fromID ile toID arasında bağ kurar.
	//
	// Aynı çift ikinci kez bağlanırsa çağrı NO-OP'tur (hata değildir): saga
	// yeniden denemeleri aynı adımı tekrar çalıştırır ve idempotency plan
	// Bölüm 2.6'nın şartıdır. Buna karşılık KARDİNALİTE ihlali (aynı ucun
	// başka bir kayda bağlı olması) errors.Conflict döner; bu, yeniden deneme
	// değil, veri hatasıdır.
	Create(ctx context.Context, name, fromID, toID string) error

	// Delete fromID ile toID arasındaki bağı kaldırır.
	//
	// Bağ zaten yoksa çağrı NO-OP'tur: telafi (compensation) adımları
	// başarısız bir Create'ten sonra da çalışır ve "yok" istenen sonucun ta
	// kendisidir.
	Delete(ctx context.Context, name, fromID, toID string) error

	// List fromID'ye bağlı toID'leri döner.
	//
	// Sonuç toID'ye göre ARTAN sıralıdır; hiç bağ yoksa boş dilim döner (nil
	// değil). Tanımlı olmayan bir link adı errors.NotFound üretir.
	List(ctx context.Context, name, fromID string) ([]string, error)

	// ListMany birden çok fromID'nin bağlarını TEK sorguda döner.
	//
	// Query katmanı (ADR 0004) genişletmeleri batch yapar; List'i kök kayıt
	// başına çağırmak N+1 üretirdi. Dönen haritada yalnızca en az bir bağı
	// olan fromID'ler bulunur; her dilim toID'ye göre artan sıralıdır.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo ters yönü toplu çözer: verilen toID'lerin her biri için
	// bağlı fromID'leri döner.
	//
	// Query katmanı, genişletmenin kök entity'si link'in To ucundayken bunu
	// kullanır. Bu olmadan ters yönlü her genişletme ya kayıt başına sorguya
	// (N+1) düşer ya da hiç desteklenmez.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)

	// Definition adı verilen linkin tanımını döner.
	//
	// Query katmanı, bir link'in hangi modüle ve hangi alana çözüldüğünü
	// buradan öğrenir. Tanımsız ad errors.NotFound üretir.
	Definition(ctx context.Context, name string) (LinkDefinition, error)
}
