package link

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// testDef paket içi testlerin ortak tanımıdır.
func testDef(name string, c Cardinality) LinkDefinition {
	return LinkDefinition{
		Name:        name,
		From:        LinkSide{Module: "product", Field: "variant_id"},
		To:          LinkSide{Module: "pricing", Field: "price_set_id"},
		Cardinality: c,
	}
}

// mustLinkTable doğrulanmış tanımdan çalışma zamanı bilgisini üretir.
func mustLinkTable(t *testing.T, def LinkDefinition) *linkTable {
	t.Helper()

	lt, err := newLinkTable(def)
	require.NoError(t, err)
	return lt
}

// TestDefinitionsRegistry kayıt defterinin temel sözleşmesini doğrular:
// yazılan okunur, yazılmayan bulunmaz, adlar sıralı döner.
func TestDefinitionsRegistry(t *testing.T) {
	defs := newDefinitions()

	assert.Empty(t, defs.names())
	_, ok := defs.lookup("product_price")
	assert.False(t, ok, "boş defterde kayıt bulunmamalı")

	defs.put(mustLinkTable(t, testDef("product_price", OneToMany)))
	defs.put(mustLinkTable(t, testDef("cart_customer", OneToOne)))

	assert.Equal(t, []string{"cart_customer", "product_price"}, defs.names(),
		"adlar sıralı dönmeli ki hata mesajları tekrarlanabilir olsun")

	lt, ok := defs.lookup("product_price")
	require.True(t, ok)
	assert.Equal(t, "link_product_price", lt.table)
	assert.Equal(t, OneToMany, lt.def.Cardinality)
}

// TestDefinitionsRegistryIsConcurrencySafe defterin eşzamanlı kullanımda
// yarışmadığını doğrular (-race ile anlamlıdır).
func TestDefinitionsRegistryIsConcurrencySafe(t *testing.T) {
	defs := newDefinitions()
	lt := mustLinkTable(t, testDef("product_price", ManyToMany))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			defs.put(lt)
		}()
		go func() {
			defer wg.Done()
			defs.lookup("product_price")
			defs.names()
		}()
	}
	wg.Wait()

	assert.Equal(t, []string{"product_price"}, defs.names())
}

// TestLinkTableNaming türetilen tablo ve indeks adlarının beklenen biçimde
// olduğunu doğrular; indeks adları hata eşlemesinde de kullanılır.
func TestLinkTableNaming(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", OneToOne))

	assert.Equal(t, "link_product_price", lt.table)
	assert.Equal(t, "link_product_price_from_uniq", lt.fromIndex)
	assert.Equal(t, "link_product_price_to_uniq", lt.toIndex)
}

// TestLinkTableSQLUsesParameters çalışma zamanı ifadelerinin kimlikleri
// PARAMETRE olarak taşıdığını ve sıralamanın belirli olduğunu doğrular.
func TestLinkTableSQLUsesParameters(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", ManyToMany))

	assert.Contains(t, lt.insert, "$1")
	assert.Contains(t, lt.insert, "$2")
	assert.Contains(t, lt.insert, "ON CONFLICT (from_id, to_id) DO NOTHING",
		"çakışma hedefi AÇIK olmalı; hedefsiz DO NOTHING kardinalite ihlalini yutardı")
	assert.Contains(t, lt.remove, "WHERE from_id = $1 AND to_id = $2")
	assert.Contains(t, lt.list, "ORDER BY to_id", "sıralama belirli olmalı")
	assert.Contains(t, lt.listMany, "= ANY($1)", "batch okuma tek sorguda olmalı")
	assert.Contains(t, lt.listMany, "ORDER BY from_id, to_id")

	for _, stmt := range []string{lt.insert, lt.remove, lt.list, lt.listMany} {
		assert.Contains(t, stmt, lt.table)
	}
}

// TestDDLEnforcesCardinality kardinalitenin UYGULAMA katmanında değil,
// veritabanı kısıtıyla zorlandığını doğrular. Bu testin düşmesi, eşzamanlı iki
// isteğin kardinaliteyi sessizce bozabileceği anlamına gelir.
func TestDDLEnforcesCardinality(t *testing.T) {
	tests := map[string]struct {
		cardinality Cardinality
		fromUnique  bool
		toUnique    bool
	}{
		"one_to_one":   {OneToOne, true, true},
		"one_to_many":  {OneToMany, false, true},
		"many_to_many": {ManyToMany, false, false},
	}

	for ad, tc := range tests {
		t.Run(ad, func(t *testing.T) {
			lt := mustLinkTable(t, testDef("product_price", tc.cardinality))
			stmts := lt.ddl()
			hepsi := strings.Join(stmts, "\n")

			assert.Contains(t, stmts[0], "CREATE TABLE IF NOT EXISTS link_product_price")
			assert.Contains(t, stmts[0], "PRIMARY KEY (from_id, to_id)",
				"çiftin benzersizliği her kardinalitede geçerlidir")
			assert.NotContains(t, hepsi, "REFERENCES",
				"link tablosu HİÇBİR modül tablosuna FK vermez (plan Bölüm 2.2)")

			fromIdx := "CREATE UNIQUE INDEX IF NOT EXISTS " + lt.fromIndex
			toIdx := "CREATE UNIQUE INDEX IF NOT EXISTS " + lt.toIndex
			if tc.fromUnique {
				assert.Contains(t, hepsi, fromIdx+" ON link_product_price (from_id)")
			} else {
				assert.NotContains(t, hepsi, fromIdx)
			}
			if tc.toUnique {
				assert.Contains(t, hepsi, toIdx+" ON link_product_price (to_id)")
			} else {
				assert.NotContains(t, hepsi, toIdx)
			}

			for _, stmt := range stmts {
				assert.Contains(t, stmt, "IF NOT EXISTS",
					"Define her açılışta çağrılır; DDL idempotent olmalı")
			}
		})
	}
}

// TestDefineFastPathDetectsConflict aynı adla farklı bir tanımın veritabanına
// HİÇ gitmeden çakışma olarak raporlandığını doğrular; havuz yoktur, yine de
// dönen hata Unavailable değil Conflict'tir.
func TestDefineFastPathDetectsConflict(t *testing.T) {
	ctx := context.Background()
	svc := newService(nil, nil)
	svc.defs.put(mustLinkTable(t, testDef("product_price", OneToMany)))

	t.Run("aynı tanım idempotenttir", func(t *testing.T) {
		require.NoError(t, svc.Define(ctx, testDef("product_price", OneToMany)))
	})

	t.Run("kardinalite değişimi çakışmadır", func(t *testing.T) {
		err := svc.Define(ctx, testDef("product_price", ManyToMany))

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err),
			"hata sınıfı KindConflict olmalı, %v alındı", errors.KindOf(err))
		assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "one_to_many", "mesaj kayıtlı tanımı göstermeli")
		assert.Contains(t, err.Error(), "many_to_many", "mesaj gelen tanımı göstermeli")
	})

	t.Run("uç değişimi çakışmadır", func(t *testing.T) {
		def := testDef("product_price", OneToMany)
		def.To.Module = "inventory"

		err := svc.Define(ctx, def)

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, "link_definition_conflict", errors.CodeOf(err))
	})
}

// TestStoredDefinitionMatches kalıcı defterle karşılaştırmanın her alanı
// gerçekten karşılaştırdığını doğrular. Eksik bir alan, sürümler arasında
// değişmiş bir tanımın sessizce kabul edilmesi demektir.
func TestStoredDefinitionMatches(t *testing.T) {
	def := testDef("product_price", OneToMany)
	esit := storedDefinition{
		fromModule:  "product",
		fromField:   "variant_id",
		toModule:    "pricing",
		toField:     "price_set_id",
		cardinality: "one_to_many",
	}

	assert.True(t, esit.matches(def))
	assert.Contains(t, esit.String(), "product.variant_id -> pricing.price_set_id")

	farkli := map[string]func(s *storedDefinition){
		"from modülü":  func(s *storedDefinition) { s.fromModule = "cart" },
		"from alanı":   func(s *storedDefinition) { s.fromField = "cart_id" },
		"to modülü":    func(s *storedDefinition) { s.toModule = "inventory" },
		"to alanı":     func(s *storedDefinition) { s.toField = "item_id" },
		"kardinalite":  func(s *storedDefinition) { s.cardinality = "many_to_many" },
		"bilinmeyen k": func(s *storedDefinition) { s.cardinality = "unknown(9)" },
	}
	for ad, boz := range farkli {
		t.Run(ad, func(t *testing.T) {
			s := esit
			boz(&s)
			assert.False(t, s.matches(def), "değişen alan çakışma olarak görülmeli")
		})
	}
}

// TestWriteErrorMapsCardinalityViolation veritabanının benzersizlik hatasının
// tipli ve OKUNABİLİR bir çakışmaya çevrildiğini doğrular: çağıran hangi ucun
// dolu olduğunu hatadan anlayabilmelidir.
func TestWriteErrorMapsCardinalityViolation(t *testing.T) {
	lt := mustLinkTable(t, testDef("product_price", OneToOne))

	t.Run("from ucu dolu", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: lt.fromIndex}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err),
			"hata sınıfı KindConflict olmalı, %v alındı", errors.KindOf(err))
		assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "var_1")
		assert.Contains(t, err.Error(), "one_to_one")
	})

	t.Run("to ucu dolu", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: lt.toIndex}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Equal(t, "link_cardinality_violation", errors.CodeOf(err))
		assert.Contains(t, err.Error(), "ps_1")
	})

	t.Run("tanınmayan kısıt yine çakışmadır", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "23505", ConstraintName: "baska_kisit"}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.IsConflict(err))
		assert.Contains(t, err.Error(), "baska_kisit", "mesaj hangi kısıtın ihlal edildiğini yazmalı")
	})

	t.Run("benzersizlik dışı hata iç hatadır", func(t *testing.T) {
		err := lt.writeError(&pgconn.PgError{Code: "42P01", Message: "relation does not exist"}, "var_1", "ps_1")

		require.Error(t, err)
		assert.True(t, errors.HasKind(err, errors.KindInternal),
			"hata sınıfı KindInternal olmalı, %v alındı", errors.KindOf(err))
		assert.Equal(t, "link_query_failed", errors.CodeOf(err))
	})
}

// TestWrapDB sürücü hatalarının sınıflandırılmasını doğrular; özellikle iptalin
// "veritabanı bozuk" ile karıştırılmaması gerekir.
func TestWrapDB(t *testing.T) {
	assert.NoError(t, wrapDB(nil, codeQueryFailed, "olmaz"))

	iptal := wrapDB(context.Canceled, codeQueryFailed, "%q linki okunamadı", "product_price")
	require.Error(t, iptal)
	assert.True(t, errors.HasKind(iptal, errors.KindUnavailable),
		"hata sınıfı KindUnavailable olmalı, %v alındı", errors.KindOf(iptal))
	assert.Equal(t, "link_canceled", errors.CodeOf(iptal))
	assert.True(t, errors.Is(iptal, context.Canceled), "sarmalanan hata zincirde kalmalı")
	assert.Contains(t, iptal.Error(), "product_price")

	sure := wrapDB(context.DeadlineExceeded, codeQueryFailed, "olmaz")
	assert.True(t, errors.HasKind(sure, errors.KindUnavailable))
	assert.Equal(t, "link_canceled", errors.CodeOf(sure))

	diger := wrapDB(errors.New("bilinmeyen"), codeDefineFailed, "olmadı")
	assert.True(t, errors.HasKind(diger, errors.KindInternal))
	assert.Equal(t, "link_define_failed", errors.CodeOf(diger))
}

// TestReservedNameDerivesFromDefinitionsTable ayrılmış adın defter adından
// TÜRETİLDİĞİNİ doğrular; defterin adı değişirse yasak da değişmelidir.
func TestReservedNameDerivesFromDefinitionsTable(t *testing.T) {
	require.Len(t, reservedNames, 1)

	table, err := TableName(reservedNames[0])
	require.Error(t, err, "ayrılmış ad tablo adına çevrilememeli")
	assert.Empty(t, table)

	// Yasak gerçekten defterle çakışmayı önlüyor mu?
	assert.Equal(t, definitionsTable, tablePrefix+reservedNames[0])
}

// TestJoinNames hata mesajlarının boş defterde de okunabilir kaldığını
// doğrular.
func TestJoinNames(t *testing.T) {
	assert.Equal(t, "(tanım yok)", joinNames(nil))
	assert.Equal(t, "a, b", joinNames([]string{"a", "b"}))
}
