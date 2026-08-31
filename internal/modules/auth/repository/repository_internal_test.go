package repository

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestWrapDBSQLSTATESiniflariniAyirir hata sınıflandırmasının SQLSTATE'e göre
// yapıldığını kanıtlar.
//
// Neden burada (paket İÇİ) sınanıyor: bu eşlemenin bazı dalları gerçek bir
// sunucuda kasten tetiklenemez — sözdizimi hatası ya da bağlantı tükenmesi
// üretmek testin kendisini kırılgan yapardı. Sınıflandırma saf bir işlevdir ve
// sahte bir *pgconn.PgError ile TAM olarak sınanabilir; kodların gerçekten
// üretildiği entegrasyon testinde ayrıca canlı sunucuda doğrulanır.
func TestWrapDBSQLSTATESiniflariniAyirir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ad    string
		kod   string
		kisit string
		tur   errors.Kind
		hata  string
	}{
		// 22xxx "veri istisnası" sınıfı: hepsi İSTEMCİNİN gönderdiği değerden
		// doğar, hiçbiri sunucu hatası değildir.
		{ad: "metin sütuna sığmadı", kod: "22001", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{ad: "değer hedef tipe çevrilemedi", kod: "22P02", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{ad: "jsonb'ye NUL kaçışı kondu", kod: "22P05", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{ad: "kodlamada karşılığı olmayan bayt", kod: "22021", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{ad: "sıfıra bölme", kod: "22012", tur: errors.KindInvalid, hata: CodeConstraintViolation},

		// 23xxx bütünlük kısıtları.
		{
			ad: "check ihlali", kod: "23514", kisit: "auth_user_email_check",
			tur: errors.KindInvalid, hata: CodeConstraintViolation,
		},
		{ad: "foreign key ihlali", kod: "23503", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{ad: "not null ihlali", kod: "23502", tur: errors.KindInvalid, hata: CodeConstraintViolation},
		{
			ad: "benzersizlik ihlali", kod: "23505", kisit: IndexUserEmail,
			tur: errors.KindConflict, hata: CodeDuplicate,
		},

		// Kalan her şey BİZİM hatamızdır ve istemciye 500 döner.
		{ad: "sözdizimi hatası", kod: "42601", tur: errors.KindInternal, hata: CodeQueryFailed},
		{ad: "bağlantı sayısı tükendi", kod: "53300", tur: errors.KindInternal, hata: CodeQueryFailed},
	}

	for _, tc := range tests {
		t.Run(tc.ad, func(t *testing.T) {
			t.Parallel()

			err := wrapDB(&pgconn.PgError{Code: tc.kod, ConstraintName: tc.kisit},
				"kayıt yazılamadı: %s", "apikey_1")

			assert.Equal(t, tc.tur, errors.KindOf(err), "SQLSTATE %s yanlış türe eşlendi", tc.kod)
			assert.Equal(t, tc.hata, errors.CodeOf(err))
			assert.Contains(t, err.Error(), "apikey_1", "biçimlendirilmiş mesaj korunmalı")
		})
	}
}

// TestWrapDBKisitAdiniYalnizcaVarsaEkler mesajın yarım bir "(kısıt: )" ekiyle
// bitmediğini kanıtlar.
//
// Veri istisnalarında kısıt adı boştur; ek koşulsuz yazılsaydı hatayı okuyan
// kişi olmayan bir kısıtı aramaya çıkardı.
func TestWrapDBKisitAdiniYalnizcaVarsaEkler(t *testing.T) {
	t.Parallel()

	kisitli := wrapDB(&pgconn.PgError{Code: "23505", ConstraintName: IndexTokenHash}, "yazılamadı")
	assert.Contains(t, kisitli.Error(), "(kısıt: "+IndexTokenHash+")")

	kisitsiz := wrapDB(&pgconn.PgError{Code: "22001"}, "yazılamadı")
	assert.NotContains(t, kisitsiz.Error(), "kısıt")
}

// TestKimlikCakismasiEPostaCakismasiSayilmaz kullanıcı başına sağlayıcı başına
// tek kimlik kuralının ihlalinin AYRI bir hata olarak bildirildiğini kanıtlar.
//
// İkisi tek koda indirilseydi çağıran "bu e-posta kullanımda" der ve kullanıcı
// serbest bir e-posta aramaya çıkardı; oysa çakışan şey e-posta değil,
// kullanıcının o sağlayıcıdaki kimliğidir.
func TestKimlikCakismasiEPostaCakismasiSayilmaz(t *testing.T) {
	t.Parallel()

	err := classifyUserWrite(
		&pgconn.PgError{Code: "23505", ConstraintName: IndexIdentityUserProvider},
		"kisi@ornek.test", "kimlik kaydı oluşturulamadı")

	assert.Equal(t, errors.KindConflict, errors.KindOf(err))
	assert.Equal(t, CodeDuplicate, errors.CodeOf(err))
	assert.NotContains(t, err.Error(), "kisi@ornek.test", "hata e-posta çakışması gibi görünmemeli")

	ePosta := classifyUserWrite(
		&pgconn.PgError{Code: "23505", ConstraintName: IndexIdentityProvider},
		"kisi@ornek.test", "kimlik kaydı oluşturulamadı")
	assert.Equal(t, CodeEmailTaken, errors.CodeOf(ePosta))
}

// TestWrapDBNilHatayiYutar nil girdinin nil dönmesini kanıtlar; çağıranlar bu
// sözleşmeye güvenip her yolda wrapDB'den geçiyor.
func TestWrapDBNilHatayiYutar(t *testing.T) {
	t.Parallel()

	assert.NoError(t, wrapDB(nil, "önemsiz"))
}
