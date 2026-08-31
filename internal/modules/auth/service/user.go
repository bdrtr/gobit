package service

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/internal/modules/auth/models"
)

// CreateUserInput bir yönetim kullanıcısının yazma girdisidir.
//
// DÜZ PAROLA BU YAPIDA YOKTUR ve bilinçlidir: girdi yapıları log'a, hata
// bağlamına ve testlerin çıktısına "%+v" ile düşer. Parola
// [Service.CreateUser]'a AYRI bir parametre olarak verilir, hiçbir yapıya
// konmaz ve hash'lendikten sonra bırakılır.
type CreateUserInput struct {
	// Email kullanıcının e-postasıdır; zorunludur, küçük harfe normalize
	// edilerek saklanır ve giriş kimliği olarak da kullanılır.
	Email string
	// FirstName kullanıcının adıdır; boş bırakılabilir.
	FirstName string
	// LastName kullanıcının soyadıdır; boş bırakılabilir.
	LastName string
	// AvatarURL profil görselinin adresidir; boş bırakılabilir.
	AvatarURL string
	// Scopes kullanıcının yetkileridir.
	//
	// nil verilirse [models.ScopeAdmin] uygulanır: yönetim kullanıcısının
	// varsayılanı tam yetkidir. BOŞ ama nil olmayan dilim ise gerçek bir
	// istektir ve yetkisiz bir kullanıcı üretir — giriş yapabilir ve kendi
	// kimliğini (GET /admin/v1/auth/me) okuyabilir, başka hiçbir yönetim
	// ucuna erişemez. İki durumu ayırmak, "yetki alanını unuttum" ile
	// "yetkisiz olsun" arasındaki farkı korur.
	//
	// Çağıranın kendisinde olmayan bir yetki verilemez
	// (bkz. [requireGrantableScopes]).
	Scopes []string
	// Metadata serbest yapısal bağlamdır; boş bırakılabilir.
	Metadata map[string]any
}

// CreateUser yeni bir yönetim kullanıcısı oluşturur.
//
// password boş DEĞİLSE kullanıcı ve giriş kimliği TEK İŞLEMDE yazılır: parolası
// atanamamış bir yönetim kullanıcısı hiç giriş yapamaz ve bunu ancak ilk giriş
// denemesinde fark edersiniz. password boşsa yalnızca kullanıcı yazılır ve
// parola sonradan [Service.SetPassword] ile atanır.
//
// Düz parola bu çağrının içinde yaşar: doğrulanır, hash'lenir ve bir daha
// kullanılmaz. Ne loglanır ne de hata mesajında geçer.
//
// Yeni kullanıcı, ÇAĞIRANIN KENDİSİNDE OLMAYAN bir yetkiyi alamaz
// (bkz. [requireGrantableScopes]): alabilseydi dar yetkili bir yönetici
// kendine tam yetkili bir kullanıcı yaratıp onunla giriş yapardı.
//
// E-posta zaten kullanılıyorsa errors.Conflict döner.
func (s *Service) CreateUser(
	ctx context.Context,
	in CreateUserInput,
	password string,
) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.User{}, err
	}
	if err := validateUserFields(in.FirstName, in.LastName, in.AvatarURL); err != nil {
		return models.User{}, err
	}
	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return models.User{}, err
	}
	if scopes == nil {
		scopes = []string{models.ScopeAdmin}
	}
	// Denetim varsayılan uygulandıktan SONRA yapılır: yetki alanını hiç
	// doldurmayan bir istek tam yetkili bir kullanıcı doğurur ve "vermedim"
	// demek, vermemiş olmaya yetmez.
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.User{}, err
	}

	var identity *models.AuthIdentity
	now := s.clock()
	if password != "" {
		if err := validatePassword(password); err != nil {
			return models.User{}, err
		}
		hash, hashErr := s.hashPassword(password)
		if hashErr != nil {
			return models.User{}, hashErr
		}
		identity = &models.AuthIdentity{
			ID:               models.NewAuthIdentityID(now),
			Provider:         models.ProviderEmailPass,
			ProviderIdentity: email,
			PasswordHash:     hash,
			CreatedAt:        now,
		}
	}

	created, err := s.repo.CreateUser(ctx, models.User{
		ID:        models.NewUserID(now),
		Email:     email,
		FirstName: in.FirstName,
		LastName:  in.LastName,
		AvatarURL: in.AvatarURL,
		Scopes:    scopes,
		Metadata:  in.Metadata,
		CreatedAt: now,
	}, identity)
	if err != nil {
		return models.User{}, err
	}

	// E-posta hassas veridir ve loglanmaz (plan Bölüm 8); kimlik ve parola
	// durumu bir çağrının izini sürmeye yeter.
	s.log.InfoContext(ctx, "yönetim kullanıcısı oluşturuldu",
		slog.String("user_id", created.ID),
		slog.Bool("parola_atandi", identity != nil),
	)
	return created, nil
}

// GetUser kimliğe göre kullanıcı döner; yoksa errors.NotFound.
func (s *Service) GetUser(ctx context.Context, id string) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	if err := requireID(id, models.UserIDPrefix, "kullanıcı kimliği"); err != nil {
		return models.User{}, err
	}
	return s.repo.GetUser(ctx, id)
}

// GetUserByEmail e-postaya göre kullanıcı döner; yoksa errors.NotFound.
//
// Bu metot YÖNETİM yüzeyi içindir. Giriş akışı onu KULLANMAZ: giriş, "yok" ile
// "parola yanlış" arasındaki farkı dışarı sızdırmamak için kendi dalını
// yürütür (bkz. [Service.Login]).
func (s *Service) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return models.User{}, err
	}
	return s.repo.GetUserByEmail(ctx, normalized)
}

// ListUsersInput kullanıcı listelemesinin girdisidir.
type ListUsersInput struct {
	// Email verilirse yalnızca bu e-postaya sahip kullanıcı döner.
	Email *string
	// Scope verilirse yalnızca bu yetkiye sahip kullanıcılar döner.
	Scope *string
	// Limit sayfa boyudur; 0 ise [DefaultLimit] uygulanır.
	Limit int64
	// Offset atlanacak kayıt sayısıdır.
	Offset int64
}

// ListUsers süzgeçlenmiş ve sayfalanmış kullanıcı listesini döner.
func (s *Service) ListUsers(ctx context.Context, in ListUsersInput) (Page[models.User], error) {
	if err := s.ready(); err != nil {
		return Page[models.User]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.User]{}, err
	}

	var filter models.UserFilter
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return Page[models.User]{}, emailErr
		}
		filter.Email = &normalized
	}
	if in.Scope != nil {
		scopes, scopeErr := normalizeScopes([]string{*in.Scope})
		if scopeErr != nil {
			return Page[models.User]{}, scopeErr
		}
		filter.Scope = &scopes[0]
	}

	items, total, err := s.repo.ListUsers(ctx, filter, limit, offset)
	if err != nil {
		return Page[models.User]{}, err
	}
	return Page[models.User]{Items: items, Count: total, Limit: limit, Offset: offset}, nil
}

// UpdateUserInput bir kullanıcının kısmi güncelleme girdisidir.
//
// nil alan "dokunma", dolu alan "bu değeri yaz" demektir. Parola BURADA
// DEĞİŞTİRİLEMEZ: parola ayrı bir işlemdir ([Service.SetPassword]) ve profil
// güncellemesiyle aynı gövdeye konsaydı, adını değiştiren bir isteğin yanlışlıkla
// parolayı da sıfırlaması mümkün olurdu.
type UpdateUserInput struct {
	// Email yeni e-postadır; giriş kimliği de bununla birlikte güncellenir.
	Email *string
	// FirstName yeni addır.
	FirstName *string
	// LastName yeni soyaddır.
	LastName *string
	// AvatarURL yeni avatar adresidir.
	AvatarURL *string
	// Scopes yeni yetki listesidir; nil ise dokunulmaz, boş dilim tüm
	// yetkileri KALDIRIR.
	//
	// Çağıranın kendisinde olmayan bir yetki verilemez
	// (bkz. [requireGrantableScopes]). Yetki KALDIRMAK serbesttir: dar bir
	// listeye inmek yükseltme değildir.
	Scopes []string
	// Metadata yeni metadata haritasıdır; sütunun tamamını değiştirir.
	Metadata map[string]any
}

// UpdateUser kullanıcının verilen alanlarını günceller.
//
// E-posta değişiyorsa giriş kimliği de aynı işlemde güncellenir; aksi hâlde
// kullanıcı yeni adresiyle giriş yapamazdı. E-posta başka bir kullanıcıdaysa
// errors.Conflict döner.
//
// Yetki listesi ÇAĞIRANIN KENDİSİNİ AŞAMAZ (bkz. [requireGrantableScopes]):
// aşabilseydi dar yetkili bir kimlik, kendi kaydını güncelleyip admin olurdu.
func (s *Service) UpdateUser(ctx context.Context, id string, in UpdateUserInput) (models.User, error) {
	if err := s.ready(); err != nil {
		return models.User{}, err
	}
	if err := requireID(id, models.UserIDPrefix, "kullanıcı kimliği"); err != nil {
		return models.User{}, err
	}

	scopes, err := normalizeScopes(in.Scopes)
	if err != nil {
		return models.User{}, err
	}
	if err := requireGrantableScopes(ctx, scopes); err != nil {
		return models.User{}, err
	}

	patch := models.UserPatch{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		AvatarURL: in.AvatarURL,
		Scopes:    scopes,
		Metadata:  in.Metadata,
	}
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return models.User{}, emailErr
		}
		patch.Email = &normalized
	}
	if err := validateUserPatch(patch); err != nil {
		return models.User{}, err
	}

	return s.repo.UpdateUser(ctx, id, patch, s.clock())
}

// DeleteUser kullanıcıyı ve giriş kimliklerini soft delete ile siler.
//
// Kimlikler de silinir; canlı kalsalardı kullanıcı SİLİNDİKTEN SONRA DA giriş
// yapabilirdi. Daha önce üretilmiş bir oturum jetonu ise imzası geçerli olduğu
// hâlde kabul EDİLMEZ: kimlik doğrulama her istekte kullanıcının hâlâ var
// olduğunu sorar (bkz. interop.go).
func (s *Service) DeleteUser(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.UserIDPrefix, "kullanıcı kimliği"); err != nil {
		return err
	}
	if err := s.repo.DeleteUser(ctx, id, s.clock()); err != nil {
		return err
	}

	s.log.InfoContext(ctx, "yönetim kullanıcısı silindi", slog.String("user_id", id))
	return nil
}

// validateUserFields kullanıcı metin alanlarının uzunluk sınırlarını doğrular.
func validateUserFields(firstName, lastName, avatarURL string) error {
	if err := checkLen("ad", firstName, models.MaxNameLen); err != nil {
		return err
	}
	if err := checkLen("soyad", lastName, models.MaxNameLen); err != nil {
		return err
	}
	return checkLen("avatar adresi", avatarURL, models.MaxURLLen)
}

// validateUserPatch kısmi güncellemedeki alanları doğrular.
//
// nil alanlar atlanır: "dokunma" ile "boş yaz" ayrımı korunur ve verilmeyen
// bir alan için uzunluk hatası üretilmez.
func validateUserPatch(patch models.UserPatch) error {
	if patch.FirstName != nil {
		if err := checkLen("ad", *patch.FirstName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.LastName != nil {
		if err := checkLen("soyad", *patch.LastName, models.MaxNameLen); err != nil {
			return err
		}
	}
	if patch.AvatarURL != nil {
		if err := checkLen("avatar adresi", *patch.AvatarURL, models.MaxURLLen); err != nil {
			return err
		}
	}
	return nil
}
