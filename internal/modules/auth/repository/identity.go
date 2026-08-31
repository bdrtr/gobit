package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// GetIdentity kullanıcının bir sağlayıcıdaki giriş kimliğini döner; yoksa
// errors.NotFound.
//
// Dönen kayıt password_hash İÇERİR. Değer bu paketin dışına çıkar ama yalnızca
// bcrypt karşılaştırmasına gider; hiçbir log satırına, hata mesajına ya da API
// yanıtına konmaz.
func (r *Repo) GetIdentity(ctx context.Context, userID, provider string) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	row, err := r.q.GetIdentityOfUser(ctx, authdb.GetIdentityOfUserParams{
		UserID:   userID,
		Provider: provider,
	})
	if err != nil {
		return models.AuthIdentity{}, notFoundOr(err, CodeIdentityNotFound,
			"%s kullanıcısının %q kimliği bulunamadı", userID, provider)
	}
	return toIdentity(row)
}

// SetPasswordHash kullanıcının parola hash'ini yazar; kimlik yoksa OLUŞTURUR.
//
// Neden tek metot: "parola ata" isteği, kullanıcının daha önce parolası olup
// olmamasına göre farklı davranmamalıdır. İki ayrı metot olsaydı çağıran her
// seferinde önce varlık sorgusu yapar ve o sorgu ile yazma arasındaki boşlukta
// iki eşzamanlı istek iki kimlik satırı üretmeye çalışırdı; ikincisi
// benzersizlik indeksine takılır ve istemciye anlamsız bir çakışma dönerdi.
//
// Hash'in kendisi ne loglanır ne de hata mesajına konur.
//
// Yazma, kaydın updated_at değerini now'a taşır. Bu sütun bu tabloda basit bir
// denetim alanı DEĞİLDİR: servis, ondan önce üretilmiş oturum jetonlarını
// reddeder (bkz. queries/identities.sql dosya başı ve service/session.go,
// sessionAnchor). Yani bu çağrı kullanıcının açık oturumlarını da kapatır;
// oturumları parolaya dokunmadan kapatmak için [Repo.RevokeSessions] vardır.
func (r *Repo) SetPasswordHash(
	ctx context.Context,
	userID, provider, providerIdentity, hash string,
	now time.Time,
) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	var identity models.AuthIdentity
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		existing, err := q.GetIdentityOfUser(ctx, authdb.GetIdentityOfUserParams{
			UserID:   userID,
			Provider: provider,
		})
		switch {
		case err == nil:
			row, upErr := q.UpdatePasswordHash(ctx, authdb.UpdatePasswordHashParams{
				ID:           existing.ID,
				PasswordHash: hash,
				UpdatedAt:    fromTime(now),
			})
			if upErr != nil {
				return notFoundOr(upErr, CodeIdentityNotFound,
					"kimlik kaydı bulunamadı: %s", existing.ID)
			}
			identity, upErr = toIdentity(row)
			return upErr

		case errors.Is(err, pgx.ErrNoRows):
			meta, metaErr := fromMetadata(nil)
			if metaErr != nil {
				return metaErr
			}
			row, insErr := q.InsertIdentity(ctx, authdb.InsertIdentityParams{
				ID:               models.NewAuthIdentityID(now),
				UserID:           userID,
				Provider:         provider,
				ProviderIdentity: providerIdentity,
				PasswordHash:     hash,
				Metadata:         meta,
				CreatedAt:        fromTime(now),
			})
			if insErr != nil {
				return classifyUserWrite(insErr, providerIdentity, "kimlik kaydı oluşturulamadı")
			}
			identity, insErr = toIdentity(row)
			return insErr

		default:
			return wrapDB(err, "kimlik kaydı okunamadı")
		}
	})
	if txErr != nil {
		return models.AuthIdentity{}, txErr
	}
	return identity, nil
}

// RevokeSessions kullanıcının BÜTÜN sağlayıcılarındaki oturum çapasını now
// anına taşır ve AÇIK OTURUMLARININ TAMAMINI düşürür.
//
// Yazılan tek sütun updated_at'tir. O sütun bu tabloda bir denetim alanı
// DEĞİLDİR: servis, ondan önce üretilmiş oturum jetonlarını reddeder
// (bkz. queries/identities.sql dosya başı ve service/session.go,
// sessionAnchor). Çıkışın tamamı bu yazmadır; düşürülecek bir "oturum kaydı"
// yoktur, düşen şey jetonların geçerliliğidir.
//
// # Sağlayıcı PARAMETRE DEĞİLDİR
//
// Tablo sağlayıcı başına satır tutar ((user_id, provider) benzersizliği) ve
// çağıranın "hangi sağlayıcıdan çıkılıyor" diye bir seçimi yoktur: satırların
// hepsi ilerletilir ve ilerletilenler dönülür. Tek sağlayıcı seçilebilseydi,
// ileride OAuth eklendiği gün çıkış o sağlayıcının jetonlarını düşürmez ve
// bunu sessizce yapardı. Bugün canlı sağlayıcı bir tane olduğu için dönen
// dilim tek elemanlıdır; değişen şey davranış değil, ikinci satırın eklendiği
// günkü davranıştır.
//
// Parola ve kilit sayaçları KORUNUR: çıkış yapmak parolayı değiştirmez ve
// sayaç sıfırlansaydı çıkış ucu, giriş kilidini temizlemenin yolu olurdu
// (gerekçe queries/identities.sql).
//
// Kullanıcının hiç canlı kimliği yoksa errors.NotFound döner; boş dilimle
// başarılı dönmek, hiçbir şey düşürmeyen bir çıkışı başarı gibi göstermek
// olurdu.
func (r *Repo) RevokeSessions(
	ctx context.Context,
	userID string,
	now time.Time,
) ([]models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}

	rows, err := r.q.RevokeSessions(ctx, authdb.RevokeSessionsParams{
		UserID:    userID,
		UpdatedAt: fromTime(now),
	})
	if err != nil {
		return nil, wrapDB(err, "%s kullanıcısının oturumları kapatılamadı", userID)
	}
	if len(rows) == 0 {
		// Çok satırlı UPDATE pgx.ErrNoRows ÜRETMEZ, boş dilim döner; "satır
		// yok" durumu bu yüzden elle sınanır. notFoundOr'a bırakılsaydı
		// kimliksiz kullanıcının çıkışı sessizce başarılı olurdu.
		return nil, errors.NotFound(CodeIdentityNotFound,
			"%s kullanıcısının hiç giriş kimliği yok", userID)
	}
	return toIdentities(rows)
}

// SessionAnchor kullanıcının EN YENİ oturum çapasını döner; hiç kimliği yoksa
// errors.NotFound.
//
// Dönen değer TEK BİR sağlayıcınınki değildir: kullanıcının bütün kimlikleri
// arasından en ileri olanıdır (gerekçe queries/identities.sql,
// GetSessionAnchor). Çıkış hepsini birden ilerlettiği için bu iki uç aynı
// kuralı uygular; ayrışsalardı çıkışın yazdığı çapa okunmaz olurdu.
//
// Kimlik satırı DÖNMEZ, yalnızca zaman damgası döner: çağıranın ihtiyacı olan
// tek şey budur ve satırın tamamını vermek, password_hash'i hiç gerekmeyen bir
// yolda repository sınırının dışına taşırdı.
func (r *Repo) SessionAnchor(ctx context.Context, userID string) (time.Time, error) {
	if err := r.ready(); err != nil {
		return time.Time{}, err
	}

	anchor, err := r.q.GetSessionAnchor(ctx, userID)
	if err != nil {
		return time.Time{}, notFoundOr(err, CodeIdentityNotFound,
			"%s kullanıcısının hiç giriş kimliği yok", userID)
	}
	return toTime(anchor), nil
}

// RegisterLoginFailure başarısız bir giriş denemesini ATOMİK olarak sayar ve
// eşiğe ulaşıldığında kimliği lockUntil anına kadar kilitler.
//
// Artırmanın SQL'de yapılması bir tercih değil, zorunluluktur: sayı burada
// okunup geri yazılsaydı aynı anda gelen yüzlerce deneme hepsi aynı değeri
// okur ve kilit hiç devreye girmezdi (bkz. queries/identities.sql).
//
// Sayaç yazılırken updated_at'e DOKUNULMAZ: o sütun oturum iptalinin çapasıdır
// ve ilerleseydi tek bir başarısız deneme kurbanın bütün oturumlarını
// düşürürdü (gerekçe queries/identities.sql dosya başında).
func (r *Repo) RegisterLoginFailure(
	ctx context.Context,
	identityID string,
	threshold int,
	lockUntil, now time.Time,
) (models.AuthIdentity, error) {
	if err := r.ready(); err != nil {
		return models.AuthIdentity{}, err
	}

	row, err := r.q.RegisterLoginFailure(ctx, authdb.RegisterLoginFailureParams{
		ID:          identityID,
		Threshold:   toInt32(int64(threshold)),
		LockedUntil: fromTime(lockUntil),
		Now:         fromTime(now),
	})
	if err != nil {
		return models.AuthIdentity{}, notFoundOr(err, CodeIdentityNotFound,
			"kimlik kaydı bulunamadı: %s", identityID)
	}
	return toIdentity(row)
}

// RegisterLoginSuccess başarılı girişte deneme sayacını ve kilidi temizler,
// son giriş anını yazar.
//
// updated_at burada da ilerlemez: yeni bir giriş, kullanıcının diğer
// cihazlardaki oturumlarını kapatmamalıdır (gerekçe queries/identities.sql
// dosya başında).
func (r *Repo) RegisterLoginSuccess(ctx context.Context, identityID string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	if err := r.q.RegisterLoginSuccess(ctx, authdb.RegisterLoginSuccessParams{
		ID:          identityID,
		LastLoginAt: fromTime(now),
	}); err != nil {
		return wrapDB(err, "giriş kaydı güncellenemedi")
	}
	return nil
}
