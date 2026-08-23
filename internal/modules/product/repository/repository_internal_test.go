package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// Bu dosya paketin içindedir: sınanan şey sürücü hatasının TİPLİ HATAYA
// çevrilmesidir ve eşleme dışa açık değildir. Eşlemenin gerçekten çalıştığı
// (yani Postgres'in bu SQLSTATE'leri gerçekten ürettiği) entegrasyon
// testlerinde kanıtlanır; burada sınanan, verilen hatanın doğru sınıfa
// düşmesidir.

// TestWrapDBMapsNoRowsToNotFound satır bulunamamasının NotFound olduğunu
// doğrular.
func TestWrapDBMapsNoRowsToNotFound(t *testing.T) {
	t.Parallel()

	err := wrapDB(pgx.ErrNoRows, "ürün bulunamadı: %s", "prod_1")
	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "beklenen sınıf not_found: %v", err)
	assert.Equal(t, codeNotFound, coreerrors.CodeOf(err))
	assert.Contains(t, err.Error(), "prod_1", "mesaj teşhis için kimliği taşımalı")
}

// TestWrapDBMapsUniqueViolation benzersizlik ihlalinin Conflict'e ve kısıt
// adına göre kararlı bir koda düştüğünü doğrular.
func TestWrapDBMapsUniqueViolation(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"product_handle_uniq":            codeHandleTaken,
		"product_collection_handle_uniq": codeHandleTaken,
		"product_variant_sku_uniq":       codeSKUTaken,
		"product_tag_value_uniq":         codeDuplicate,
		"product_option_title_uniq":      codeDuplicate,
		"bilinmeyen_kisit":               codeConflict,
		"":                               codeConflict,
	}

	for constraint, wantCode := range cases {
		t.Run(constraint, func(t *testing.T) {
			t.Parallel()

			pgErr := &pgconn.PgError{Code: pgUniqueViolation, ConstraintName: constraint}
			err := wrapDB(pgErr, "ürün oluşturulamadı (%s)", "tisort")

			require.Error(t, err)
			assert.True(t, coreerrors.IsConflict(err), "beklenen sınıf conflict: %v", err)
			assert.Equal(t, wantCode, coreerrors.CodeOf(err))
			assert.ErrorIs(t, err, error(pgErr), "özgün sürücü hatası zincirde kalmalı")
		})
	}
}

// TestWrapDBMapsForeignKeyAndCheck referans ve kısıt ihlallerinin İSTEMCİ
// hatası (Invalid) sayıldığını doğrular.
//
// Sınıflandırma önemlidir: var olmayan bir koleksiyona ürün bağlamak istemcinin
// düzeltebileceği bir hatadır; 500 dönmek onu sunucu arızası gibi gösterirdi.
func TestWrapDBMapsForeignKeyAndCheck(t *testing.T) {
	t.Parallel()

	fkErr := wrapDB(&pgconn.PgError{Code: pgForeignKeyViolation, ConstraintName: "product_collection_id_fkey"},
		"ürün oluşturulamadı")
	assert.True(t, coreerrors.IsInvalid(fkErr), "beklenen sınıf invalid: %v", fkErr)
	assert.Equal(t, codeInvalidRef, coreerrors.CodeOf(fkErr))
	assert.Contains(t, fkErr.Error(), "product_collection_id_fkey", "kısıt adı teşhis için mesajda kalmalı")

	checkErr := wrapDB(&pgconn.PgError{Code: pgCheckViolation, ConstraintName: "product_status_check"},
		"ürün oluşturulamadı")
	assert.True(t, coreerrors.IsInvalid(checkErr), "beklenen sınıf invalid: %v", checkErr)
	assert.Equal(t, codeCheckFailed, coreerrors.CodeOf(checkErr))
}

// TestWrapDBMapsCancellation iptalin Internal DEĞİL Unavailable olduğunu
// doğrular.
//
// pgx bağlam iptalinde ham context.Canceled döner; sınıflandırılmasaydı
// bütçesi dolan bir istek istemciye opak bir 500 olarak görünürdü.
func TestWrapDBMapsCancellation(t *testing.T) {
	t.Parallel()

	for _, base := range []error{context.Canceled, context.DeadlineExceeded} {
		err := wrapDB(base, "ürünler listelenemedi")
		require.Error(t, err)
		assert.True(t, coreerrors.HasKind(err, coreerrors.KindUnavailable),
			"beklenen sınıf unavailable: %v", err)
		assert.Equal(t, codeCanceled, coreerrors.CodeOf(err))
	}
}

// TestWrapDBDefaultsToInternal sınıflandırılamayan hatanın güvenli tarafa
// (Internal) düştüğünü doğrular.
func TestWrapDBDefaultsToInternal(t *testing.T) {
	t.Parallel()

	err := wrapDB(errors.New("beklenmeyen"), "ürünler listelenemedi")
	require.Error(t, err)
	assert.True(t, coreerrors.HasKind(err, coreerrors.KindInternal), "beklenen sınıf internal: %v", err)
	assert.Equal(t, codeDBFailed, coreerrors.CodeOf(err))
}

// TestWrapDBNilStaysNil hatasız yolun hata üretmediğini doğrular.
func TestWrapDBNilStaysNil(t *testing.T) {
	t.Parallel()

	assert.NoError(t, wrapDB(nil, "hiçbir şey"))
}

// TestMetadataRoundTrip jsonb dönüşümünün veri kaybetmediğini doğrular.
func TestMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	raw, err := fromMetadata(map[string]any{"renk": "mavi", "adet": float64(3)})
	require.NoError(t, err)

	got, err := toMetadata(raw)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"renk": "mavi", "adet": float64(3)}, got)
}

// TestMetadataEmptyBecomesObject boş metadata'nın NOT NULL sütuna uygun boş
// nesne olarak yazıldığını, okunurken de nil'e döndüğünü doğrular.
func TestMetadataEmptyBecomesObject(t *testing.T) {
	t.Parallel()

	raw, err := fromMetadata(nil)
	require.NoError(t, err)
	assert.Equal(t, "{}", string(raw))

	got, err := toMetadata(raw)
	require.NoError(t, err)
	assert.Nil(t, got, "boş nesne haritaya değil nil'e çevrilmeli (JSON'da alan hiç görünmez)")

	got, err = toMetadata(nil)
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = toMetadata([]byte("null"))
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestPatchMetadataDistinguishesUnsetFromEmpty "değiştirme" ile "boşalt"
// ayrımının korunduğunu doğrular.
func TestPatchMetadataDistinguishesUnsetFromEmpty(t *testing.T) {
	t.Parallel()

	unset, err := patchMetadata(nil)
	require.NoError(t, err)
	assert.Nil(t, unset, "nil harita sorguya NULL gitmeli; COALESCE eski değeri korur")

	empty, err := patchMetadata(map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "{}", string(empty), "boş harita metadata'yı boşaltmalı")
}

// TestToMetadataRejectsBrokenJSON bozuk jsonb içeriğinin sessizce yok
// sayılmadığını doğrular.
func TestToMetadataRejectsBrokenJSON(t *testing.T) {
	t.Parallel()

	_, err := toMetadata([]byte("{bozuk"))
	require.Error(t, err)
	assert.Equal(t, codeMetadataInvalid, coreerrors.CodeOf(err))
}

// TestToInt32Clamps sayfalama daraltmasının işaret değiştirmediğini doğrular.
func TestToInt32Clamps(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int32(0), toInt32(-1))
	assert.Equal(t, int32(20), toInt32(20))
	assert.Equal(t, int32(2147483647), toInt32(1<<40))
}

// TestNotFoundCarriesEntityAndID bulunamadı hatasının teşhis bilgisini
// taşıdığını doğrular.
func TestNotFoundCarriesEntityAndID(t *testing.T) {
	t.Parallel()

	err := notFound("varyant", "variant_1")
	assert.True(t, coreerrors.IsNotFound(err))
	assert.Contains(t, err.Error(), "varyant")
	assert.Contains(t, err.Error(), "variant_1")
}

// TestEmptyIDListSkipsQuery boş kimlik listesinin veritabanına hiç gitmediğini
// doğrular.
//
// Depo nil bağlantıyla kurulur: sorgu yapılsaydı test panikle düşerdi. Boş
// listeyle "WHERE id = ANY('{}')" sorgusu göndermek boşuna gidiş-dönüştür.
func TestEmptyIDListSkipsQuery(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	ctx := context.Background()

	products, err := repo.ListProductsByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, products)

	variants, err := repo.ListVariantsByIDs(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, variants)

	byProduct, err := repo.ListVariantsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, byProduct)

	options, err := repo.ListOptionsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, options)

	values, err := repo.ListOptionValuesByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, values)

	images, err := repo.ListImagesByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, images)

	tags, err := repo.ListTagsByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, tags)

	categories, err := repo.ListCategoriesByProductIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, categories)

	optionValues, err := repo.ListVariantOptionValues(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, optionValues)
}

// TestInTxWithoutPoolReusesSameStore işleme bağlı bir deponun İÇ İÇE işlem
// açmadığını doğrular.
//
// İkinci bir işlem havuzdan ayrı bir bağlantı kapardı; o bağlantı dıştaki
// işlemin henüz görünmeyen yazmalarını okuyamaz ve kilit beklerken kendini
// bekleyen bir çıkmaza girebilirdi.
func TestInTxWithoutPoolReusesSameStore(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	var got Store

	require.NoError(t, repo.InTx(context.Background(), func(_ context.Context, s Store) error {
		got = s
		return nil
	}))
	assert.Same(t, repo, got, "işlem içindeki depo aynı örnek olmalı")
}

// TestInTxPropagatesError fn'in hatasının olduğu gibi döndüğünü doğrular.
func TestInTxPropagatesError(t *testing.T) {
	t.Parallel()

	repo := &Repo{}
	want := coreerrors.Invalid("test", "olmadı")

	err := repo.InTx(context.Background(), func(context.Context, Store) error { return want })
	assert.ErrorIs(t, err, want)
}

// TestStoreInterfaceIsSatisfied somut deponun sözleşmeyi karşıladığını
// derleme zamanında sabitler.
func TestStoreInterfaceIsSatisfied(t *testing.T) {
	t.Parallel()

	var store Store = &Repo{}
	assert.NotNil(t, store)

	// models paketinin bu katmandan göründüğünü de sabitler; depo yalnızca
	// domain tipleriyle konuşur, pgtype dışarı sızmaz.
	var product models.Product
	assert.Empty(t, product.ID)
}
