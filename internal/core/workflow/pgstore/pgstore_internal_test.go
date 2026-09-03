package pgstore

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
)

// TestNewExecutionIDBicimi kimliğin önek ve uzunluk sözleşmesini doğrular.
func TestNewExecutionIDBicimi(t *testing.T) {
	t.Parallel()

	id := newExecutionID(time.Now())

	assert.True(t, strings.HasPrefix(id, "wfx_"), "kimlik %q, wfx_ önekiyle başlamalı", id)
	body := strings.TrimPrefix(id, idPrefix)
	assert.Len(t, body, idBodyLen, "gövde 16 baytın Base32 karşılığı kadar olmalı")
	assert.Equal(t, strings.ToUpper(body), body, "Crockford Base32 alfabesi büyük harftir")
	assert.NotContains(t, body, "I", "Crockford alfabesinde I yoktur")
	assert.NotContains(t, body, "U", "Crockford alfabesinde U yoktur")
}

// TestNewExecutionIDTekil aynı milisaniyede üretilen kimliklerin çakışmadığını
// doğrular: tekillik zaman damgasına değil, 80 bitlik rastgeleliğe dayanır.
func TestNewExecutionIDTekil(t *testing.T) {
	t.Parallel()

	an := time.Now()
	gorulen := make(map[string]struct{}, 1000)
	for range 1000 {
		id := newExecutionID(an)
		_, tekrar := gorulen[id]
		require.False(t, tekrar, "aynı kimlik iki kez üretildi: %s", id)
		gorulen[id] = struct{}{}
	}
}

// TestNewExecutionIDZamanSirali kimliklerin sözlüksel sırasının zaman sırasıyla
// aynı olduğunu doğrular. Zaman damgası başta olmasaydı bu tutmazdı.
func TestNewExecutionIDZamanSirali(t *testing.T) {
	t.Parallel()

	temel := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	onceki := newExecutionID(temel)
	for i := 1; i < 50; i++ {
		sonraki := newExecutionID(temel.Add(time.Duration(i) * time.Second))
		assert.Less(t, onceki, sonraki, "sonraki zamanın kimliği sözlüksel olarak büyük olmalı")
		onceki = sonraki
	}
}

// TestNewExecutionIDGecmisZaman 1970 öncesi zaman damgasının kimliği bozmadığını
// doğrular (negatif milisaniye tabana çekilir).
func TestNewExecutionIDGecmisZaman(t *testing.T) {
	t.Parallel()

	id := newExecutionID(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC))

	assert.True(t, strings.HasPrefix(id, idPrefix))
	assert.Len(t, strings.TrimPrefix(id, idPrefix), idBodyLen)
	// Zaman damgası tabana çekildiği için gövde sıfırlarla başlar.
	assert.True(t, strings.HasPrefix(strings.TrimPrefix(id, idPrefix), "000"),
		"1970 öncesi zaman sıfıra çekilmeli")
}

// TestJSONParamNullVeBosAyrimi JSON alanlarının NULL/boş/JSON-null ayrımını
// doğrular. Bu ayrım korunmazsa "değer yok" ile "değer null" karışır.
func TestJSONParamNullVeBosAyrimi(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ad      string
		girdi   json.RawMessage
		bekleme any
	}{
		{"nil SQL NULL olur", nil, nil},
		{"boş dilim SQL NULL olur", json.RawMessage{}, nil},
		{"yalnızca boşluk SQL NULL olur", json.RawMessage("  \n\t "), nil},
		{"JSON null değeri korunur", json.RawMessage(`null`), []byte(`null`)},
		{"nesne korunur", json.RawMessage(`{"a":1}`), []byte(`{"a":1}`)},
		{"dizi korunur", json.RawMessage(`[1,2]`), []byte(`[1,2]`)},
		{"sayı korunur", json.RawMessage(`0`), []byte(`0`)},
		{"boş dize değeri korunur", json.RawMessage(`""`), []byte(`""`)},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			t.Parallel()

			got, err := jsonParam(tc.girdi, "input")

			require.NoError(t, err)
			assert.Equal(t, tc.bekleme, got)
		})
	}
}

// TestJSONParamGecersizJSON geçersiz JSON'un veritabanına GİTMEDEN tipli
// hataya çevrildiğini doğrular.
func TestJSONParamGecersizJSON(t *testing.T) {
	t.Parallel()

	for _, girdi := range []string{`{`, `{"a":}`, `merhaba`, `{"a":1},`} {
		got, err := jsonParam(json.RawMessage(girdi), "input")

		require.Error(t, err, "%q geçersiz sayılmalı", girdi)
		assert.Nil(t, got)
		assert.True(t, coreerrors.IsInvalid(err), "hata Invalid sınıfında olmalı: %v", err)
		assert.Contains(t, err.Error(), "input")
	}
}

// TestJSONParamJSONBinReddettikleri json.Valid'i GEÇEN ama JSONB'nin kabul
// etmediği gövdelerin depoya gitmeden Invalid'e çevrildiğini doğrular.
//
// Denetim olmasaydı hata sürücüden dönerdi (SQLSTATE 22P05 / 22021) ve
// KindInternal'a düşerdi: çağıranın verisi yüzünden HTTP 500 üretilir, üstelik
// Create başarısız olduğu için o idempotency anahtarıyla açılan kayıt asla
// tamamlanamazdı.
func TestJSONParamJSONBinReddettikleri(t *testing.T) {
	t.Parallel()

	// nulEscape ters bölü + u0000'dır; kaynağa doğrudan yazılırsa derleyici
	// onu gerçek NUL karakterine çevirir ve sınanan durum kalmaz.
	tests := []struct {
		ad    string
		girdi string
	}{
		{"değerdeki NUL kaçışı", `{"x":"a` + nulEscape + `b"}`},
		{"anahtardaki NUL kaçışı", `{"` + nulEscape + `":1}`},
		{"kök dizgedeki NUL kaçışı", `"` + nulEscape + `"`},
		{"dizideki NUL kaçışı", `["` + nulEscape + `"]`},
		{"geçersiz UTF-8 dizisi", "{\"x\":\"\xff\xfe\"}"},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			t.Parallel()

			require.True(t, json.Valid([]byte(tc.girdi)),
				"vaka anlamlı olsun diye gövde json.Valid'i GEÇMELİ")

			got, err := jsonParam(json.RawMessage(tc.girdi), "input")

			require.Error(t, err)
			assert.Nil(t, got)
			assert.True(t, coreerrors.IsInvalid(err), "hata Invalid sınıfında olmalı: %v", err)
			assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), "input")
		})
	}
}

// TestJSONParamKacirilmisTersBolu NUL kaçışı denetiminin YANLIŞ POZİTİF
// vermediğini doğrular: kaçırılmış bir ters bölünün ardından gelen u0000 kaçış
// değildir, sıradan metindir ve JSONB onu sorunsuz saklar.
func TestJSONParamKacirilmisTersBolu(t *testing.T) {
	t.Parallel()

	// İki ters bölü + u0000: JSON'da ters bölü + u0000 METNİ (altı karakter).
	govde := `{"x":"a` + nulEscape[:1] + nulEscape + `b"}`
	require.True(t, json.Valid([]byte(govde)))

	got, err := jsonParam(json.RawMessage(govde), "input")

	require.NoError(t, err, "kaçırılmış ters bölü NUL kaçışı değildir")
	assert.Equal(t, []byte(govde), got)
}

// TestJSONValueNullAyrimi okuma yönünde NULL ile JSON null'ın ayrıldığını
// doğrular.
func TestJSONValueNullAyrimi(t *testing.T) {
	t.Parallel()

	assert.Nil(t, jsonValue(nil), "SQL NULL nil RawMessage olmalı")
	assert.Equal(t, json.RawMessage(`null`), jsonValue([]byte(`null`)),
		"JSONB null değeri 'null' baytları olarak dönmeli")
	assert.Equal(t, json.RawMessage(`{"a": 1}`), jsonValue([]byte(`{"a": 1}`)))
}

// TestKeyParam yalnızca BOŞ DİZENİN "anahtar yok" sayıldığını, dolu anahtarın
// OLDUĞU GİBİ saklandığını doğrular.
func TestKeyParam(t *testing.T) {
	t.Parallel()

	bos, err := keyParam("")
	require.NoError(t, err)
	assert.Nil(t, bos, "boş anahtar NULL olmalı")

	got, err := keyParam(" ord_1 ")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, " ord_1 ", *got, "dolu anahtar kırpılmadan saklanmalı")
}

// TestKeyParamBoslukAnahtarReddedilir yalnızca boşluktan oluşan anahtarın
// SESSİZCE NULL'a çevrilmediğini doğrular.
//
// Sessiz çevirme, çağıranın istediği tekrar korumasını hiçbir uyarı vermeden
// kaldırırdı: NULL'lar kısmi benzersiz indekste çakışmadığı için aynı anahtarla
// ikinci, üçüncü yürütme sorunsuz açılır ve iş iki kez yapılırdı.
func TestKeyParamBoslukAnahtarReddedilir(t *testing.T) {
	t.Parallel()

	for _, girdi := range []string{" ", "   ", "\t", "\n", " \t\n "} {
		got, err := keyParam(girdi)

		require.Errorf(t, err, "%q anahtarı reddedilmeli", girdi)
		assert.Nil(t, got)
		assert.True(t, coreerrors.IsInvalid(err), "hata Invalid sınıfında olmalı: %v", err)
		assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
	}
}

// TestKeyParamSinirlar uzunluk ve kodlama sınırlarının yazma yolunda
// uygulandığını doğrular.
func TestKeyParamSinirlar(t *testing.T) {
	t.Parallel()

	tam, err := keyParam(strings.Repeat("k", maxKeyLen))
	require.NoError(t, err, "tam sınırdaki anahtar kabul edilmeli")
	assert.NotNil(t, tam)

	_, err = keyParam(strings.Repeat("k", maxKeyLen+1))
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = keyParam("ord\x00_1")
	require.Error(t, err, "NUL baytı TEXT sütununa yazılamaz")
	assert.True(t, coreerrors.IsInvalid(err))
}

// TestKeyValue NULL anahtarın boş dizeye döndüğünü doğrular.
func TestKeyValue(t *testing.T) {
	t.Parallel()

	assert.Empty(t, keyValue(nil))

	deger := "ord_1"
	assert.Equal(t, "ord_1", keyValue(&deger))
}

// TestTimeParam sıfır zamanın NULL'a, dolu zamanın UTC'ye dönüştüğünü doğrular.
func TestTimeParam(t *testing.T) {
	t.Parallel()

	assert.Nil(t, timeParam(time.Time{}), "sıfır zaman NULL olmalı")

	yer := time.FixedZone("UTC+3", 3*60*60)
	an := time.Date(2026, 8, 23, 15, 4, 5, 0, yer)
	got := timeParam(an)
	require.NotNil(t, got)
	assert.Equal(t, time.UTC, got.Location(), "zaman UTC'ye taşınmalı (plan Bölüm 8)")
	assert.True(t, got.Equal(an))
}

// TestTimeValue NULL zamanın sıfır time.Time'a döndüğünü doğrular.
func TestTimeValue(t *testing.T) {
	t.Parallel()

	assert.True(t, timeValue(nil).IsZero())

	an := time.Date(2026, 8, 23, 15, 4, 5, 0, time.FixedZone("UTC+3", 3*60*60))
	got := timeValue(&an)
	assert.Equal(t, time.UTC, got.Location())
	assert.True(t, got.Equal(an))
}

// TestRequireText zorunlu metin doğrulamasını sınar.
func TestRequireText(t *testing.T) {
	t.Parallel()

	got, err := requireText("  sipariş  ", "workflow adı", maxNameLen)
	require.NoError(t, err)
	assert.Equal(t, "sipariş", got, "değer kırpılmış dönmeli")

	for _, girdi := range []string{"", "   ", "\t\n"} {
		_, err = requireText(girdi, "workflow adı", maxNameLen)
		require.Error(t, err)
		assert.True(t, coreerrors.IsInvalid(err))
		assert.Contains(t, err.Error(), "workflow adı")
	}

	_, err = requireText(strings.Repeat("a", maxNameLen+1), "workflow adı", maxNameLen)
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err), "sınırı aşan değer Invalid olmalı")

	_, err = requireText(strings.Repeat("a", maxNameLen), "workflow adı", maxNameLen)
	assert.NoError(t, err, "tam sınırdaki değer kabul edilmeli")
}

// TestRequireTextYazilamayanBaytlar TEXT sütununa yazılamayan baytların
// veritabanına GİTMEDEN Invalid'e çevrildiğini doğrular.
//
// Denetim olmasaydı sürücü SQLSTATE 22021 dönerdi ve hata KindInternal'a
// düşerdi: çağıranın verisinden doğan arıza 500 olarak görünürdü.
func TestRequireTextYazilamayanBaytlar(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"NUL baytı":               "siparis\x00tamamla",
		"geçersiz UTF-8":          "siparis\xff",
		"kırpma sonrası NUL":      "  \x00  ",
		"yalnızca geçersiz UTF-8": "\xc3\x28",
	}

	for ad, girdi := range tests {
		t.Run(ad, func(t *testing.T) {
			t.Parallel()

			_, err := requireText(girdi, "workflow adı", maxNameLen)

			require.Error(t, err)
			assert.True(t, coreerrors.IsInvalid(err), "hata Invalid sınıfında olmalı: %v", err)
			assert.Equal(t, CodeInvalid, coreerrors.CodeOf(err))
		})
	}
}

// TestSafeText tanı metninin REDDEDİLMEK yerine yazılabilir hâle getirildiğini
// doğrular.
//
// Ayrım bilinçlidir: tanımlayıcı alanlar (requireText) reddedilir, arıza
// açıklaması temizlenir. Açıklama yüzünden uç durumun yazılamaması, kaydı
// sonsuza dek "running" bırakır ve idempotency anahtarını kullanılamaz kılardı.
func TestSafeText(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "adım patladı", safeText("adım patladı"), "temiz metin değişmemeli")
	assert.Empty(t, safeText(""))
	assert.Equal(t, "ab", safeText("a\x00b"), "NUL baytı atılmalı")
	assert.Equal(t, "ab", safeText("a\xffb"), "geçersiz UTF-8 dizisi atılmalı")
	assert.Equal(t, "ab", safeText("a\xff\x00b"), "ikisi bir arada da temizlenmeli")

	temiz := safeText("stok\x00 servisi \xff yanıt vermedi")
	assert.True(t, isStorableText(temiz), "sonuç TEXT sütununa yazılabilir olmalı: %q", temiz)
	assert.Contains(t, temiz, "stok", "okunabilir kısım korunmalı")
	assert.Contains(t, temiz, "yanıt vermedi")
}

// TestRequireCount sayaç doğrulamasını sınar; INTEGER sütunun taşması
// veritabanına ulaşmadan yakalanmalıdır.
func TestRequireCount(t *testing.T) {
	t.Parallel()

	got, err := requireCount(3, "adım sırası")
	require.NoError(t, err)
	assert.Equal(t, int32(3), got)

	got, err = requireCount(0, "adım sırası")
	require.NoError(t, err)
	assert.Equal(t, int32(0), got)

	_, err = requireCount(-1, "adım sırası")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = requireCount(math.MaxInt32+1, "adım sırası")
	require.Error(t, err)
	assert.True(t, coreerrors.IsInvalid(err))

	_, err = requireCount(math.MaxInt32, "adım sırası")
	assert.NoError(t, err, "int32 sınırındaki değer kabul edilmeli")
}

// TestCreateErrorCakismaEslemesi benzersizlik ihlalinin hangi kısıttan geldiğine
// göre ayrı KOD ve ayrı SINIFA çevrildiğini doğrular.
//
// Sınıf ayrımı sözleşmenin parçasıdır: motor Conflict'i "bu istek daha önce
// yapıldı" diye okuyup tekrar (replay) yoluna gider. Kimlik çakışmasında ise
// anahtar depoda yoktur; Conflict dönseydi motor aramadığı bir anahtarı arar,
// FindByIdempotencyKey NotFound döner ve çağıran gerçek arızayı ("kimlik zaten
// kullanılmış") mesajda hiç görmezdi.
func TestCreateErrorCakismaEslemesi(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ad     string
		kisit  string
		kod    string
		sinif  coreerrors.Kind
		icerir string
	}{
		{"idempotency indeksi", idempotencyIndex, CodeDuplicateKey, coreerrors.KindConflict, "idempotency"},
		{"birincil anahtar", executionsPKConstraint, CodeDuplicateID, coreerrors.KindInvalid, "already exists"},
		{"tanınmayan kısıt", "baska_kisit", CodeConflict, coreerrors.KindInternal, "baska_kisit"},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			t.Parallel()

			ham := &pgconn.PgError{Code: uniqueViolation, ConstraintName: tc.kisit}

			err := createError(ham, "wfx_1", "siparis", "ord_1")

			require.Error(t, err)
			assert.Equal(t, tc.sinif, coreerrors.KindOf(err), "sınıf eşlemesi: %v", err)
			assert.Equal(t, tc.kod, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), tc.icerir)
			assert.ErrorIs(t, err, ham, "ham sürücü hatası zincirde kalmalı")
		})
	}
}

// TestCreateErrorYalnizcaIdempotencyConflict Conflict sınıfının YALNIZCA
// idempotency çakışmasına ayrıldığını doğrular; motorun tekrar yoluna girmesi
// buna bağlıdır.
func TestCreateErrorYalnizcaIdempotencyConflict(t *testing.T) {
	t.Parallel()

	for _, kisit := range []string{executionsPKConstraint, "baska_kisit"} {
		ham := &pgconn.PgError{Code: uniqueViolation, ConstraintName: kisit}

		err := createError(ham, "wfx_1", "siparis", "ord_1")

		require.Error(t, err)
		assert.Falsef(t, coreerrors.IsConflict(err),
			"%s ihlali Conflict OLMAMALI: motor onu idempotent tekrar sanardı (%v)", kisit, err)
	}
}

// TestCreateErrorDigerHatalar benzersizlik dışındaki hataların çakışmaya
// ÇEVRİLMEDİĞİNİ doğrular.
func TestCreateErrorDigerHatalar(t *testing.T) {
	t.Parallel()

	ham := &pgconn.PgError{Code: "42P01", Message: "relation does not exist"}

	err := createError(ham, "wfx_1", "siparis", "")

	require.Error(t, err)
	assert.False(t, coreerrors.IsConflict(err), "tablo yok hatası çakışma değildir")
	assert.Equal(t, coreerrors.KindInternal, coreerrors.KindOf(err))
	assert.Equal(t, CodeQueryFailed, coreerrors.CodeOf(err))
}

// TestWrapDBSiniflandirma sürücü hatalarının sınıflandırmasını doğrular.
func TestWrapDBSiniflandirma(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ad    string
		ham   error
		sinif coreerrors.Kind
		kod   string
	}{
		{"nil", nil, coreerrors.KindInternal, ""},
		{"iptal", context.Canceled, coreerrors.KindUnavailable, CodeCanceled},
		{"süre aşımı", context.DeadlineExceeded, coreerrors.KindUnavailable, CodeCanceled},
		{"satır yok", pgx.ErrNoRows, coreerrors.KindNotFound, CodeNotFound},
		{
			"yabancı anahtar ihlali",
			&pgconn.PgError{Code: foreignKeyViolation, ConstraintName: "fk"},
			coreerrors.KindNotFound, CodeNotFound,
		},
		{
			"CHECK ihlali",
			&pgconn.PgError{Code: checkViolation, ConstraintName: "chk_ad"},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"metinde NUL baytı",
			&pgconn.PgError{Code: notInRepertoire},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"JSONB'nin çeviremediği kaçış",
			&pgconn.PgError{Code: untranslatableCharacter},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{
			"metin hedef tipe ayrıştırılamadı",
			&pgconn.PgError{Code: invalidTextRepresentation},
			coreerrors.KindInvalid, CodeInvalid,
		},
		{"bilinmeyen", errors.New("kopuk bağlantı"), coreerrors.KindInternal, CodeQueryFailed},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			t.Parallel()

			err := wrapDB(tc.ham, CodeQueryFailed, "işlem başarısız")

			if tc.ham == nil {
				assert.NoError(t, err, "nil hata sarmalanmamalı")
				return
			}
			require.Error(t, err)
			assert.Equal(t, tc.sinif, coreerrors.KindOf(err))
			assert.Equal(t, tc.kod, coreerrors.CodeOf(err))
			assert.ErrorIs(t, err, tc.ham, "ham hata zincirde kalmalı")
		})
	}
}

// TestWrapDBCheckKisitAdi CHECK ihlalinin mesajında kısıt adının geçtiğini ve
// çağıranın argümanlarının bozulmadığını doğrular.
func TestWrapDBCheckKisitAdi(t *testing.T) {
	t.Parallel()

	args := []any{"wfx_1"}
	ham := &pgconn.PgError{Code: checkViolation, ConstraintName: "workflow_executions_status_not_blank"}

	err := wrapDB(ham, CodeQueryFailed, "%s yazılamadı", args...)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wfx_1")
	assert.Contains(t, err.Error(), "workflow_executions_status_not_blank")
	assert.Equal(t, []any{"wfx_1"}, args, "çağıranın argüman dilimi değişmemeli")
}

// TestScanTargetsSiniri tarama hedeflerinin yürütme/adım sınırını doğrular.
// execColumnCount kayarsa skipExecColumns yanlış sütunları atlar ve okunan
// kayıt sessizce bozulurdu.
func TestScanTargetsSiniri(t *testing.T) {
	t.Parallel()

	var (
		row  execRow
		step stepRow
	)

	hedefler := scanTargets(&row, &step)

	require.Len(t, hedefler, 17, "birleşim satırı 9 yürütme + 8 adım sütunu taşır")
	assert.Equal(t, &row.id, hedefler[0], "ilk sütun yürütme kimliğidir")
	assert.Equal(t, &row.updatedAt, hedefler[execColumnCount-1],
		"son yürütme sütunu updated_at olmalı")
	assert.Equal(t, &step.index, hedefler[execColumnCount],
		"adım sütunları tam bu sınırdan sonra başlamalı")
}

// TestSkipExecColumnsYalnizcaYurutmeyiAtlar hedef boşaltmasının sınırını
// doğrular: yürütme sütunları atlanır, adım sütunları HER satırda taranır.
func TestSkipExecColumnsYalnizcaYurutmeyiAtlar(t *testing.T) {
	t.Parallel()

	var (
		row  execRow
		step stepRow
	)
	hedefler := scanTargets(&row, &step)
	adimHedefleri := append([]any(nil), hedefler[execColumnCount:]...)

	skipExecColumns(hedefler)

	for i, hedef := range hedefler[:execColumnCount] {
		assert.Nilf(t, hedef, "%d. yürütme sütunu ilk satırdan sonra taranmamalı", i)
	}
	assert.Equal(t, adimHedefleri, hedefler[execColumnCount:],
		"adım sütunlarının hedefleri değişmemeli")
}

// TestFoldRowsYurutmeSutunlariniBirKezTarar LEFT JOIN'in her adım için TEKRAR
// taşıdığı yürütme sütunlarının yalnızca İLK satırda tarandığını doğrular.
//
// Taransaydı pgx input ve output için her satırda yeni bir bayt dilimi ayırıp
// hemen çöpe atardı; 256 KB girdisi ve sekiz adımı olan bir kaydın okunması
// 0,28 MB yerine 2,17 MB ayırıyordu (gerçek Postgres'te ölçüldü).
func TestFoldRowsYurutmeSutunlariniBirKezTarar(t *testing.T) {
	t.Parallel()

	kaynak := &sahteSatirlar{t: t, adimSayisi: 3}

	exec, err := foldRows(kaynak)

	require.NoError(t, err)
	require.NotNil(t, exec)
	require.Len(t, kaynak.atlananlar, 3, "her adım için bir satır taranmalı")

	assert.NotContains(t, kaynak.atlananlar[0], true,
		"ilk satırda hiçbir sütun atlanmamalı: yürütme oradan kurulur")
	for satir, atlanan := range kaynak.atlananlar[1:] {
		for i := range execColumnCount {
			assert.Truef(t, atlanan[i],
				"%d. satırda %d. yürütme sütunu yeniden taranmamalı", satir+1, i)
		}
		for i := execColumnCount; i < len(atlanan); i++ {
			assert.Falsef(t, atlanan[i],
				"%d. satırda %d. adım sütunu taranmalı", satir+1, i)
		}
	}

	// Atlama, okunan kaydı bozmamalıdır.
	assert.Equal(t, "wfx_1", exec.ID)
	assert.Equal(t, json.RawMessage(`{"a":1}`), exec.Input)
	require.Len(t, exec.Steps, 3)
	for i, adim := range exec.Steps {
		assert.Equal(t, i, adim.Index)
	}
}

// sahteSatirlar veritabanı olmadan birleşim satırı üreten bir rowSource'tur.
//
// Her Scan çağrısında hangi hedeflerin nil (atlanmış) geldiğini kaydeder:
// yürütme sütunlarının yalnızca bir kez tarandığı ancak böyle görülebilir.
type sahteSatirlar struct {
	t          *testing.T
	adimSayisi int
	sira       int
	atlananlar [][]bool
}

func (r *sahteSatirlar) Next() bool {
	if r.sira >= r.adimSayisi {
		return false
	}
	r.sira++
	return true
}

func (r *sahteSatirlar) Scan(dest ...any) error {
	r.t.Helper()
	require.Len(r.t, dest, execColumnCount+8, "birleşim satırı 17 sütundur")

	atlanan := make([]bool, len(dest))
	for i, hedef := range dest {
		atlanan[i] = hedef == nil
	}
	r.atlananlar = append(r.atlananlar, atlanan)

	an := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	anahtar := "ord_1"
	adimAdi := "adim"
	adimDurum := "invoked"
	adimAriza := ""
	index := int32(r.sira - 1)
	deneme := int32(1)

	tara(r.t, dest[0], "wfx_1")
	tara(r.t, dest[1], "siparis")
	tara(r.t, dest[2], &anahtar)
	tara(r.t, dest[3], "running")
	tara(r.t, dest[4], []byte(`{"a":1}`))
	tara(r.t, dest[5], []byte(nil))
	tara(r.t, dest[6], "")
	tara(r.t, dest[7], an)
	tara(r.t, dest[8], an)
	tara(r.t, dest[9], &index)
	tara(r.t, dest[10], &adimAdi)
	tara(r.t, dest[11], &adimDurum)
	tara(r.t, dest[12], []byte(nil))
	tara(r.t, dest[13], &adimAriza)
	tara(r.t, dest[14], &deneme)
	tara(r.t, dest[15], (*time.Time)(nil))
	tara(r.t, dest[16], (*time.Time)(nil))
	return nil
}

// tara sürücünün yaptığını yapar: hedef nil ise sütunu ATLAR, değilse değeri
// yazar (pgx.Rows.Scan: "nil will skip the value entirely").
func tara[T any](t *testing.T, hedef any, deger T) {
	t.Helper()
	if hedef == nil {
		return
	}
	p, uygun := hedef.(*T)
	require.Truef(t, uygun, "beklenmeyen tarama hedefi tipi: %T", hedef)
	*p = deger
}

// TestMigrationsUpDownCiftleri gömülü migration'ların up/down çiftleri hâlinde
// olduğunu doğrular (plan Bölüm 8: geri alınabilir migration).
func TestMigrationsUpDownCiftleri(t *testing.T) {
	t.Parallel()

	girdiler, err := fs.ReadDir(Migrations(), ".")
	require.NoError(t, err)
	require.NotEmpty(t, girdiler, "en az bir migration gömülü olmalı")

	ups := map[string]bool{}
	downs := map[string]bool{}
	for _, girdi := range girdiler {
		ad := girdi.Name()
		icerik, readErr := fs.ReadFile(Migrations(), ad)
		require.NoError(t, readErr)
		assert.NotEmpty(t, strings.TrimSpace(string(icerik)), "%s boş olmamalı", ad)

		switch {
		case strings.HasSuffix(ad, ".up.sql"):
			ups[strings.TrimSuffix(ad, ".up.sql")] = true
		case strings.HasSuffix(ad, ".down.sql"):
			downs[strings.TrimSuffix(ad, ".down.sql")] = true
		default:
			t.Errorf("%s .up.sql ya da .down.sql ile bitmeli", ad)
		}
	}
	assert.Equal(t, ups, downs, "her up dosyasının bir down eşi olmalı")
}

// TestMigrationsSemaSozlesmesi şemanın koddaki varsayımları karşıladığını
// doğrular: hata eşlemesi indeks ADINA, idempotency güvencesi ise indeksin
// KISMİ olmasına dayanır. İkisi de migration dosyasında değişirse bu test düşer.
func TestMigrationsSemaSozlesmesi(t *testing.T) {
	t.Parallel()

	up, err := fs.ReadFile(Migrations(), "000001_workflow_init.up.sql")
	require.NoError(t, err)
	sql := string(up)

	assert.Contains(t, sql, "CREATE TABLE workflow_executions")
	assert.Contains(t, sql, "CREATE TABLE workflow_execution_steps")
	assert.Contains(t, sql, "CREATE UNIQUE INDEX "+idempotencyIndex,
		"hata eşlemesi bu indeks adına dayanıyor")
	assert.Contains(t, sql, "WHERE idempotency_key IS NOT NULL",
		"indeks KISMİ olmalı: anahtarsız yürütmeler birbirini engellememeli")
	assert.Contains(t, sql, "PRIMARY KEY (execution_id, step_index)",
		"adımlar (execution_id, step_index) ile tekil olmalı")
	assert.Contains(t, sql, "JSONB", "JSON alanları JSONB saklanmalı")
	assert.Contains(t, sql, "TIMESTAMPTZ", "zaman alanları TIMESTAMPTZ olmalı")

	down, err := fs.ReadFile(Migrations(), "000001_workflow_init.down.sql")
	require.NoError(t, err)
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS workflow_execution_steps")
	assert.Contains(t, string(down), "DROP TABLE IF EXISTS workflow_executions")
}

// TestMigrationOwnerSabiti sürüm defterinin sahip adını sabitler.
func TestMigrationOwnerSabiti(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "workflow", MigrationOwner)
}
