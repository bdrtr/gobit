package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/repository/authdb"
)

// CreateUser yeni bir yönetim kullanıcısını, varsa giriş kimliğiyle BİRLİKTE
// yazar.
//
// İkisi tek işlemdedir: kimliksiz kalan bir kullanıcı hiç giriş yapamaz ve
// bunu ancak ilk giriş denemesinde fark edersiniz; kullanıcısız kalan bir
// kimlik ise sahipsizdir. identity nil ise yalnızca kullanıcı yazılır — parola
// sonradan [Repo.SetPassword] ile atanır.
//
// E-posta zaten kullanılıyorsa errors.Conflict döner; kural veritabanındaki
// kısmi benzersiz indekstedir (bkz. [IndexUserEmail]) ve uygulama tarafında
// tekrarlanmaz.
func (r *Repo) CreateUser(
	ctx context.Context,
	u models.User,
	identity *models.AuthIdentity,
) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	userMeta, err := fromMetadata(u.Metadata)
	if err != nil {
		return models.User{}, err
	}

	var created models.User
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		row, insErr := q.InsertUser(ctx, authdb.InsertUserParams{
			ID:        u.ID,
			Email:     u.Email,
			FirstName: u.FirstName,
			LastName:  u.LastName,
			AvatarUrl: u.AvatarURL,
			Scopes:    u.Scopes,
			Metadata:  userMeta,
			CreatedAt: fromTime(u.CreatedAt),
		})
		if insErr != nil {
			return classifyUserWrite(insErr, u.Email, "kullanıcı oluşturulamadı")
		}

		created, insErr = toUser(row)
		if insErr != nil {
			return insErr
		}

		if identity == nil {
			return nil
		}

		identityMeta, metaErr := fromMetadata(identity.Metadata)
		if metaErr != nil {
			return metaErr
		}
		if _, idErr := q.InsertIdentity(ctx, authdb.InsertIdentityParams{
			ID:               identity.ID,
			UserID:           created.ID,
			Provider:         identity.Provider,
			ProviderIdentity: identity.ProviderIdentity,
			PasswordHash:     identity.PasswordHash,
			Metadata:         identityMeta,
			CreatedAt:        fromTime(identity.CreatedAt),
		}); idErr != nil {
			return classifyUserWrite(idErr, identity.ProviderIdentity, "kimlik kaydı oluşturulamadı")
		}
		return nil
	})
	if txErr != nil {
		return models.User{}, txErr
	}
	return created, nil
}

// GetUser kimliğe göre kullanıcı döner; yoksa errors.NotFound.
func (r *Repo) GetUser(ctx context.Context, id string) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	row, err := r.q.GetUser(ctx, id)
	if err != nil {
		return models.User{}, notFoundOr(err, CodeUserNotFound, "kullanıcı bulunamadı: %s", id)
	}
	return toUser(row)
}

// GetUserByEmail e-postaya göre CANLI kullanıcıyı döner; yoksa errors.NotFound.
//
// Giriş akışının ilk adımıdır. Hata mesajı e-postayı İÇERİR ama bu mesaj
// istemciye gitmez: giriş yolu "bulunamadı" ile "parola yanlış" arasındaki
// farkı dışarı sızdırmaz (bkz. service, Login).
func (r *Repo) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	row, err := r.q.GetUserByEmail(ctx, email)
	if err != nil {
		return models.User{}, notFoundOr(err, CodeUserNotFound,
			"%q e-postasıyla kullanıcı bulunamadı", email)
	}
	return toUser(row)
}

// ListUsers süzgeçlenmiş ve sayfalanmış kullanıcı listesini, filtreye uyan
// TOPLAM kayıt sayısıyla birlikte döner.
func (r *Repo) ListUsers(
	ctx context.Context,
	filter models.UserFilter,
	limit, offset int64,
) ([]models.User, int64, error) {
	if err := r.ready(); err != nil {
		return nil, 0, err
	}

	rows, err := r.q.ListUsers(ctx, authdb.ListUsersParams{
		Email: filter.Email,
		Scope: filter.Scope,
		Lim:   toInt32(limit),
		Off:   toInt32(offset),
	})
	if err != nil {
		return nil, 0, wrapDB(err, "kullanıcı listesi alınamadı")
	}

	total, err := r.q.CountUsers(ctx, authdb.CountUsersParams{
		Email: filter.Email,
		Scope: filter.Scope,
	})
	if err != nil {
		return nil, 0, wrapDB(err, "kullanıcı sayısı alınamadı")
	}

	users, err := toUsers(rows)
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUsersByIDs verilen kimliklere karşılık gelen kullanıcıları TEK sorguda
// döner. Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (r *Repo) GetUsersByIDs(ctx context.Context, ids []string) ([]models.User, error) {
	if err := r.ready(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []models.User{}, nil
	}

	rows, err := r.q.ListUsersByIDs(ctx, ids)
	if err != nil {
		return nil, wrapDB(err, "kullanıcılar alınamadı")
	}
	return toUsers(rows)
}

// UpdateUser kullanıcının verilen alanlarını günceller.
//
// E-posta değişiyorsa giriş kimliğinin provider_identity alanı AYNI İŞLEMDE
// güncellenir: ikisi ayrı sütunlarda dursa da aynı şeyi ifade eder ve
// ayrışırlarsa kullanıcı yeni adresiyle giriş YAPAMAZ.
func (r *Repo) UpdateUser(
	ctx context.Context,
	id string,
	patch models.UserPatch,
	now time.Time,
) (models.User, error) {
	if err := r.ready(); err != nil {
		return models.User{}, err
	}

	meta, err := patchMetadata(patch.Metadata)
	if err != nil {
		return models.User{}, err
	}

	var updated models.User
	txErr := r.inTx(ctx, func(q *authdb.Queries) error {
		row, upErr := q.UpdateUser(ctx, authdb.UpdateUserParams{
			Email:     patch.Email,
			FirstName: patch.FirstName,
			LastName:  patch.LastName,
			AvatarUrl: patch.AvatarURL,
			Scopes:    patch.Scopes,
			Metadata:  meta,
			UpdatedAt: fromTime(now),
			ID:        id,
		})
		if upErr != nil {
			if errors.Is(upErr, pgx.ErrNoRows) {
				return errors.NotFound(CodeUserNotFound, "kullanıcı bulunamadı: %s", id)
			}
			return classifyUserWrite(upErr, derefOr(patch.Email), "kullanıcı güncellenemedi")
		}

		updated, upErr = toUser(row)
		if upErr != nil {
			return upErr
		}

		if patch.Email == nil {
			return nil
		}
		if syncErr := q.SyncIdentityProviderIdentity(ctx, authdb.SyncIdentityProviderIdentityParams{
			UserID:           id,
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: *patch.Email,
			UpdatedAt:        fromTime(now),
		}); syncErr != nil {
			return classifyUserWrite(syncErr, *patch.Email, "giriş kimliği güncellenemedi")
		}
		return nil
	})
	if txErr != nil {
		return models.User{}, txErr
	}
	return updated, nil
}

// DeleteUser kullanıcıyı ve giriş kimliklerini soft delete ile siler.
//
// İkisi AYNI işlemdedir ve sıra önemlidir değil, ATOMİKLİK önemlidir: kimliği
// canlı kalan bir kullanıcı, silindikten sonra da giriş yapabilirdi.
func (r *Repo) DeleteUser(ctx context.Context, id string, now time.Time) error {
	if err := r.ready(); err != nil {
		return err
	}

	return r.inTx(ctx, func(q *authdb.Queries) error {
		if _, err := q.SoftDeleteUser(ctx, authdb.SoftDeleteUserParams{
			ID:        id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return notFoundOr(err, CodeUserNotFound, "kullanıcı bulunamadı: %s", id)
		}
		if err := q.SoftDeleteIdentitiesOfUser(ctx, authdb.SoftDeleteIdentitiesOfUserParams{
			UserID:    id,
			DeletedAt: fromTime(now),
		}); err != nil {
			return wrapDB(err, "kullanıcının giriş kimlikleri silinemedi")
		}
		return nil
	})
}

// classifyUserWrite bir yazma hatasını e-posta çakışması bakımından sınıflar.
//
// Hem auth_user hem auth_identity benzersizlik indeksleri aynı gerçeği ifade
// eder: bu e-posta zaten kullanılıyor. İkisini tek koda indirmek çağıranı
// hangi tablonun konuştuğunu bilmek zorunda bırakmaz.
func classifyUserWrite(err error, email, message string) error {
	if err == nil {
		return nil
	}
	switch ConstraintName(err) {
	case IndexUserEmail, IndexIdentityProvider:
		return errors.Wrap(err, errors.KindConflict, CodeEmailTaken,
			"%q e-postası zaten kullanılıyor", email)
	case IndexIdentityUserProvider:
		// Bu çakışma e-posta çakışması DEĞİLDİR ve öyle gösterilmez: e-posta
		// serbest olsa bile kullanıcının o sağlayıcıdaki kimliği zaten
		// vardır. Buraya düşmek, iki eşzamanlı "parola ata" isteğinin aynı
		// kimliği açmaya çalıştığı anlamına gelir; biri yazar, öteki burada
		// durur (bkz. [Repo.SetPasswordHash]).
		return errors.Wrap(err, errors.KindConflict, CodeDuplicate,
			"kullanıcının bu sağlayıcıdaki kimliği zaten var")
	}
	return wrapDB(err, "%s", message)
}

// derefOr işaretçiyi çözer; nil ise boş dize döner.
func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
