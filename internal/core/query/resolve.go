package query

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/link"
)

// validateSpec sorgu tanımını çekirdeğe girmeden önce doğrular.
func validateSpec(spec GraphSpec) error {
	if spec.Entity == "" {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Entity boş olamaz")
	}
	if spec.Limit < 0 {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Limit negatif olamaz (verilen: %d)", spec.Limit)
	}
	if spec.Offset < 0 {
		return errors.Invalid(codeInvalidSpec, "GraphSpec.Offset negatif olamaz (verilen: %d)", spec.Offset)
	}

	total, err := validateExpansions(spec.Expand, 0)
	if err != nil {
		return err
	}
	if total > maxExpansions {
		return errors.Invalid(codeInvalidSpec,
			"genişletme sayısı %d sınırını aştı (verilen: %d)", maxExpansions, total)
	}
	return nil
}

// validateExpansions genişletme ağacını doğrular: boş link adı, birleştirme
// anahtarını ezen çıktı anahtarı, aynı seviyede çakışan çıktı anahtarı ve aşırı
// derinlik reddedilir. Dönen sayı ağaçtaki TOPLAM genişletme sayısıdır; genişlik
// sınırını çağıran uygular.
//
// Doğrulama sağlayıcıya gidilmeden ÖNCE, ağacın tamamı için yapılır; bozuk bir
// spec yüzünden yarım iş yapılmaz.
func validateExpansions(exps []Expansion, depth int) (int, error) {
	if len(exps) == 0 {
		return 0, nil
	}
	if depth >= maxExpandDepth {
		return 0, errors.Invalid(codeInvalidSpec,
			"genişletme derinliği %d sınırını aştı", maxExpandDepth)
	}

	total := len(exps)
	seen := make(map[string]struct{}, len(exps))
	for _, exp := range exps {
		if exp.Link == "" {
			return 0, errors.Invalid(codeInvalidSpec, "Expansion.Link boş olamaz")
		}
		key := outputKey(exp)
		if key == IDField {
			return 0, errors.Invalid(codeInvalidSpec,
				"genişletme çıktı anahtarı %q olamaz; %q kaydın birleştirme anahtarıdır ve üzerine yazılırsa kaydın kimliği kaybolur (%q genişletmesine As ile başka bir ad verin)",
				IDField, IDField, exp.Link)
		}
		if _, dup := seen[key]; dup {
			return 0, errors.Invalid(codeInvalidSpec,
				"aynı seviyede %q anahtarı birden çok genişletme tarafından yazılıyor; As ile ayırın", key)
		}
		seen[key] = struct{}{}

		nested, err := validateExpansions(exp.Expand, depth+1)
		if err != nil {
			return 0, err
		}
		total += nested
	}
	return total, nil
}

// outputKey genişletmenin sonuca yazılacağı anahtarı döner; As boşsa Link.
func outputKey(exp Expansion) string {
	if exp.As != "" {
		return exp.As
	}
	return exp.Link
}

// ctxErr bağlam iptal edilmişse tipli hata döner; edilmemişse nil.
//
// Sağlayıcıya ve link servisine gitmeden ÖNCE çağrılır: iptal edilmiş bir
// bağlamla yeni iş başlatmanın anlamı yoktur. what hangi adımın iptal edildiğini
// mesaja yazar.
func ctxErr(ctx context.Context, what string) error {
	if err := ctx.Err(); err != nil {
		return errors.Wrap(err, errors.KindUnavailable, codeCanceled, "%s iptal edildi", what)
	}
	return nil
}

// fieldsWithID alan listesini kopyalar ve gerekiyorsa kimlik alanını ekler.
//
// Liste boşsa (sağlayıcının varsayılan alanları isteniyorsa) dokunulmaz;
// kimlik alanının varsayılan kümede bulunduğu varsayılır. Çağıranın dilimi asla
// değiştirilmez.
func fieldsWithID(fields []string, need bool) []string {
	if len(fields) == 0 {
		return nil
	}
	out := slices.Clone(fields)
	if need && !slices.Contains(out, IDField) {
		out = append(out, IDField)
	}
	return out
}

// collectIDs bu seviyedeki tüm kayıtların kimliklerini SIRAYI KORUYARAK ve
// tekilleştirerek toplar; ayrıca kimlikten o kimliğe sahip kayıtlara eşleyen
// haritayı döner.
//
// Aynı kimlik birden çok kayıtta görünebilir (örn. aynı kaydın iki kez
// listelenmesi); bu yüzden harita dilim tutar ve genişletme sonucu hepsine
// yazılır.
//
// Kimliği okunamayan TEK bir kayıt bile tipli hata üretir. O kayıt link'e
// sokulamadığı için genişletme anahtarını hiç almaz; atlanırsa aynı çağrıdan
// dönen kayıtların bir kısmı anahtarı taşır bir kısmı taşımaz ve eksik veri
// doğru sonuç gibi görünür. Kural, getirilen kayıtlar için indexByID'nin
// uyguladığı kuralla ve paketin "kısmi sonuç yoktur" politikasıyla aynıdır.
func collectIDs(records []Record, entity, linkName string) (ids []string, byID map[string][]Record, err error) {
	ids = make([]string, 0, len(records))
	byID = make(map[string][]Record, len(records))

	for _, rec := range records {
		id, ok := recordID(rec)
		if !ok {
			return nil, nil, errors.Internal(codeMissingID,
				"%q genişletmesi yapılamıyor: %q kayıtlarının birinde kimlik okunamadı (%q %s)",
				linkName, entity, IDField, recordIDProblem(rec)).
				WithDetails(map[string]any{detailLink: linkName, detailEntity: entity, detailField: IDField})
		}
		if _, seen := byID[id]; !seen {
			ids = append(ids, id)
		}
		byID[id] = append(byID[id], rec)
	}
	return ids, byID, nil
}

// indexByID getirilen kayıtları kimliğe göre indeksler.
//
// Kimliği okunamayan bir kayıt üst kayda bağlanamaz; sessizce düşürmek eksik
// veriyi doğru sonuç gibi gösterirdi, bu yüzden tipli hata döner.
func indexByID(records []Record, entity string) (map[string]Record, error) {
	byID := make(map[string]Record, len(records))
	for _, rec := range records {
		id, ok := recordID(rec)
		if !ok {
			return nil, errors.Internal(codeMissingID,
				"%q sağlayıcısının döndürdüğü bir kayıtta kimlik okunamadı (%q %s); birleştirme yapılamaz",
				entity, IDField, recordIDProblem(rec)).
				WithDetails(map[string]any{detailEntity: entity, detailField: IDField})
		}
		byID[id] = rec
	}
	return byID, nil
}

// recordID kaydın kimliğini döner. Alan yoksa, string değilse veya boşsa
// ikinci dönüş false olur.
func recordID(rec Record) (string, bool) {
	raw, ok := rec[IDField]
	if !ok {
		return "", false
	}
	id, ok := raw.(string)
	if !ok || id == "" {
		return "", false
	}
	return id, true
}

// recordIDProblem kimliğin NEDEN okunamadığını mesaja yazılacak biçimde anlatır.
//
// "alan yok" ile "alan var ama tipi yanlış" ayrımı teşhis için kritiktir:
// pgx.RowToMap ile beslenen bir sağlayıcıda uuid kolonu string değil [16]byte
// olarak gelir ve tek başına "alan yok" diyen bir mesaj yanlış tarafı suçlar.
func recordIDProblem(rec Record) string {
	raw, ok := rec[IDField]
	if !ok {
		return "alanı yok"
	}
	if s, isString := raw.(string); isString && s == "" {
		return "alanı boş dize"
	}
	return fmt.Sprintf("alanı %T tipinde, string bekleniyordu", raw)
}

// ownRecords sağlayıcıdan gelen kayıtların ÇAĞRIYA AİT kopyalarını üretir.
//
// Query genişletme sonucunu kaydın içine yazar; sağlayıcı döndürdüğü haritayı
// kendi durumuyla paylaşıyorsa bu yazma o durumu kirletir: modülün kendi
// okumaları yabancı bir anahtar taşır, sonraki çağrılara bayat alan sızar ve iki
// eşzamanlı Graph çağrısı aynı haritaya yazarak veri yarışı üretir. Sağlayıcı
// sözleşmesine "kopyala" yazmak yerine sınırda kopyalamak, sağlayıcının
// davranışından BAĞIMSIZ yapısal koruma verir.
//
// Kopya yüzeyseldir: alan değerleri paylaşılmaya devam eder, ama Query yalnızca
// üst seviye anahtar yazar, değerlerin içine hiç dokunmaz.
func ownRecords(records []Record) []Record {
	out := make([]Record, len(records))
	for i, rec := range records {
		out[i] = maps.Clone(rec)
	}
	return out
}

// uniqueValues link çözümünden çıkan tüm ilgili kimlikleri tekilleştirir.
//
// Sıra belirlidir: kök kimlikler sıralanır, her birinin ilgili kimlikleri
// geldikleri sırayla eklenir. Belirli sıra hem sağlayıcı tarafında
// önbelleklemeyi hem testlerde doğrulamayı kolaylaştırır.
func uniqueValues(related map[string][]string) []string {
	out := make([]string, 0, len(related))
	seen := make(map[string]struct{}, len(related))

	for _, key := range slices.Sorted(maps.Keys(related)) {
		for _, id := range related[key] {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// shape ilgili kimlikleri kardinaliteye uygun biçimde sonuç değerine çevirir.
//
// many true ise her zaman []Record (eşleşme yoksa BOŞ dilim, nil değil) döner;
// false ise ilk eşleşen [Record], eşleşme yoksa nil döner. Tek kayıt yazılan
// bir uçta birden çok bağ varsa ilki alınır — böylece kardinalitesi yanlış
// tanımlanmış bir link sessizce dilim üretip sonucun ŞEKLİNİ değiştirmez.
func shape(ids []string, byID map[string]Record, many bool) any {
	if !many {
		for _, id := range ids {
			if rec, ok := byID[id]; ok {
				return rec
			}
		}
		return nil
	}

	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		if rec, ok := byID[id]; ok {
			out = append(out, rec)
		}
	}
	return out
}

// targetSide link'in hangi ucundan hangi ucuna gidileceğini çözer.
//
// Kök entity link'in From ucundaysa ileri (reverse=false), To ucundaysa ters
// (reverse=true) yönde gidilir. İki uç da aynı entity ise (kendine link) ileri
// yön seçilir. Link kök entity'ye hiç dokunmuyorsa errors.KindInvalid döner.
func targetSide(def link.LinkDefinition, entity string) (target string, reverse bool, err error) {
	switch {
	case def.From.Module == entity:
		return def.To.Module, false, nil
	case def.To.Module == entity:
		return def.From.Module, true, nil
	default:
		return "", false, errors.Invalid(codeLinkMismatch,
			"%q link'i %q entity'sine bağlanmıyor; link'in uçları: %q ve %q",
			def.Name, entity, def.From.Module, def.To.Module).
			WithDetails(map[string]any{detailLink: def.Name, detailEntity: entity})
	}
}

// writesMany genişletmenin sonuca dilim mi tek kayıt mı yazacağını bildirir.
//
// Kardinalite YÖNLÜDÜR: [link.OneToMany] "bir From kaydı, birden çok To kaydı"
// demektir. Aynı link ters yönde (To ucundan From ucuna) çözüldüğünde ilişki
// tekildir, bu yüzden tek kayıt yazılır. [link.ManyToMany] her iki yönde de
// dilim, [link.OneToOne] her iki yönde de tek kayıt yazar.
func writesMany(def link.LinkDefinition, reverse bool) (bool, error) {
	switch def.Cardinality {
	case link.OneToOne:
		return false, nil
	case link.OneToMany:
		return !reverse, nil
	case link.ManyToMany:
		return true, nil
	default:
		return false, errors.Invalid(codeCardinality,
			"%q link'inin kardinalitesi tanınmıyor: %s", def.Name, def.Cardinality).
			WithDetails(map[string]any{detailLink: def.Name})
	}
}
