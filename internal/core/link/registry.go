package link

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// linkTable tanımlanmış tek bir linkin çalışma zamanı bilgisidir: tanım, tablo
// adı, kardinaliteyi zorlayan indeks adları ve önceden üretilmiş SQL ifadeleri.
//
// İfadelerin BURADA, Define anında bir kez üretilmesi bilinçlidir: tablo adı
// SQL'de parametrelenemediği için ifadeler dizge birleştirmesiyle kurulur ve bu
// birleştirme yalnızca doğrulanmış bir tanımdan (bkz. [LinkDefinition.Validate])
// beslenir. Create/Delete/List yolları hazır ifadeyi kullanır; çağıranın verdiği
// hiçbir dizge SQL metnine karışmaz, hepsi parametre olarak gider.
type linkTable struct {
	def       LinkDefinition
	table     string
	fromIndex string
	toIndex   string

	insert   string
	remove   string
	list     string
	listMany string
	// listManyByTo ters yönü (to_id -> from_id) toplu çözer. Query katmanı,
	// kök entity link'in To ucundayken bunu kullanır (bkz. ADR 0004).
	listManyByTo string
	// lookupIndex ManyToMany'de ters yön sorgusunun tablo taraması yapmasını
	// engelleyen BENZERSİZ OLMAYAN indekstir. Diğer kardinalitelerde to_id
	// zaten benzersiz indekslidir.
	lookupIndex string
}

// newLinkTable doğrulanmış bir tanımdan çalışma zamanı bilgisini üretir.
// Çağıran, def'i daha önce doğrulamış olmalıdır.
func newLinkTable(def LinkDefinition) (*linkTable, error) {
	table, err := TableName(def.Name)
	if err != nil {
		return nil, err
	}

	return &linkTable{
		def:       def,
		table:     table,
		fromIndex: table + fromIndexSuffix,
		toIndex:   table + toIndexSuffix,

		// ON CONFLICT hedefi AÇIKÇA (from_id, to_id)'dir. Hedefsiz bir
		// "ON CONFLICT DO NOTHING" kardinaliteyi zorlayan indekslerin
		// ihlallerini de sessizce yutar; o zaman aynı kaydı iki ayrı hedefe
		// bağlamak hata değil, sessiz bir kayıp olurdu.
		insert: fmt.Sprintf(
			`INSERT INTO %s (from_id, to_id) VALUES ($1, $2) ON CONFLICT (from_id, to_id) DO NOTHING`,
			table),
		remove: fmt.Sprintf(`DELETE FROM %s WHERE from_id = $1 AND to_id = $2`, table),
		// Sıralama belirli olmalı ki API yanıtları ve testler tekrarlanabilir
		// olsun; (from_id, to_id) birincil anahtar olduğu için to_id'ye göre
		// sıralama TAM sıra verir (eşitlik olamaz).
		list: fmt.Sprintf(`SELECT to_id FROM %s WHERE from_id = $1 ORDER BY to_id`, table),
		listMany: fmt.Sprintf(
			`SELECT from_id, to_id FROM %s WHERE from_id = ANY($1) ORDER BY from_id, to_id`, table),
		listManyByTo: fmt.Sprintf(
			`SELECT to_id, from_id FROM %s WHERE to_id = ANY($1) ORDER BY to_id, from_id`, table),
		lookupIndex: table + toLookupSuffix,
	}, nil
}

// requiredIndexes kardinaliteyi zorlayan indekslerin adlarını döner.
//
// [linkTable.ddl] ile AYNI switch'ten türetilir; ikisi ayrışırsa doğrulama
// yanlış şeyi arar. Yeni bir kardinalite eklenirse her ikisi de güncellenmelidir.
func (lt *linkTable) requiredIndexes() []string {
	switch lt.def.Cardinality {
	case OneToOne:
		return []string{lt.fromIndex, lt.toIndex}
	case OneToMany:
		return []string{lt.toIndex}
	case ManyToMany:
		return []string{lt.lookupIndex}
	default:
		return nil
	}
}

// ddl link tablosunu ve kardinalite kısıtlarını oluşturan ifadeleri döner.
//
// Hepsi IF NOT EXISTS'tir: Define her açılışta yeniden çağrılır ve var olan bir
// şemayı bulmak normal durumdur.
//
// Link tablosu HİÇBİR modülün tablosuna REFERENCES vermez (plan Bölüm 2.2);
// kimlikler serbest metindir. Bağın işaret ettiği kaydın gerçekten var olması
// sahibi modülün ve workflow telafisinin sorumluluğundadır.
func (lt *linkTable) ddl() []string {
	stmts := []string{fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
	from_id    TEXT NOT NULL,
	to_id      TEXT NOT NULL,
	created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
	PRIMARY KEY (from_id, to_id)
)`, lt.table)}

	// Birincil anahtar (from_id, to_id) çiftin benzersizliğini zaten verir;
	// aşağıdaki indeksler kardinaliteyi DARALTIR.
	switch lt.def.Cardinality {
	case OneToOne:
		// Her iki uç da tek bağa sahip olabilir.
		stmts = append(stmts,
			uniqueIndexDDL(lt.fromIndex, lt.table, "from_id"),
			uniqueIndexDDL(lt.toIndex, lt.table, "to_id"))
	case OneToMany:
		// Bir fromID çok toID'ye bağlanabilir; bir toID tek fromID'ye bağlıdır.
		stmts = append(stmts, uniqueIndexDDL(lt.toIndex, lt.table, "to_id"))
	case ManyToMany:
		// Kardinalite kısıtı yok, ama ters yön sorgusu (to_id = ANY(...))
		// indekssiz kalırsa tablo taramasına düşer. OneToOne/OneToMany'de
		// to_id zaten benzersiz indekslidir.
		stmts = append(stmts, fmt.Sprintf(
			`CREATE INDEX IF NOT EXISTS %s ON %s (to_id)`, lt.lookupIndex, lt.table))
	}
	return stmts
}

// uniqueIndexDDL verilen sütun üzerinde benzersiz indeks oluşturan ifadeyi üretir.
func uniqueIndexDDL(indexName, table, column string) string {
	return fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS %s ON %s (%s)`, indexName, table, column)
}

// definitions bildirilmiş linklerin süreç içi kayıt defteridir.
//
// Defter iki iş görür: (a) aynı adla farklı bir tanımı veritabanına gitmeden
// yakalar, (b) Create/Delete/List çağrılarının yalnızca TANIMLI linkler
// üzerinde çalışmasını sağlar — böylece SQL'e giren tablo adı her zaman
// doğrulanmış bir tanımdan gelir.
//
// Defterin süreç içi olması yeterli DEĞİLDİR: başka bir sürüm/süreç aynı adı
// farklı tanımlarsa bunu ancak kalıcı defter (bkz. definitionsTable) yakalar.
// İkisi birlikte çalışır; buradaki kopya hızlı yoldur.
type definitions struct {
	mu     sync.RWMutex
	byName map[string]*linkTable
}

// newDefinitions boş bir kayıt defteri üretir.
func newDefinitions() *definitions {
	return &definitions{byName: make(map[string]*linkTable)}
}

// lookup adı verilen linkin çalışma zamanı bilgisini döner.
func (d *definitions) lookup(name string) (*linkTable, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	lt, ok := d.byName[name]
	return lt, ok
}

// put linki deftere yazar; var olan kaydı ezer.
func (d *definitions) put(lt *linkTable) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.byName[lt.def.Name] = lt
}

// names tanımlı link adlarını sıralı döner; hata mesajlarında kullanılır.
func (d *definitions) names() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return slices.Sorted(maps.Keys(d.byName))
}
