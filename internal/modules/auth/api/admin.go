package api

import (
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/auth/models"
	"github.com/bdrtr/gobit/internal/modules/auth/service"
)

// --- kimlik -------------------------------------------------------------------

// adminLogin e-posta ve parolayla oturum jetonu üretir
// (POST /admin/v1/auth/login).
//
// # BU UÇ KORUMASIZDIR
//
// Kimlik doğrulaması yapılacak istektir; kimliği daha yeni kuracaktır.
// Yönetim yüzeyine corehttp.RequireAdmin bağlanırken [LoginPath] DIŞARIDA
// BIRAKILMALIDIR — korunursa kimse giriş yapamaz ve sistem kilitlenir.
//
// Başarısız her deneme AYNI hatayı ve kabaca aynı süreyi üretir; ayrım
// yapılsaydı yanıtın kendisi "bu e-posta kayıtlı" bilgisini verirdi
// (bkz. service.Service.Login).
func (h *Handler) adminLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req loginRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Düz parola yalnızca burada açık dizeye çevrilir; dönüşümün göze batması
	// bilinçlidir (bkz. [secret]).
	token, expiresAt, err := h.svc.Login(ctx, req.Email, string(req.Password))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusOK, itemEnvelope{Data: loginResponse{
		Token:     token,
		ExpiresAt: expiresAt,
		TokenType: "Bearer",
	}})
}

// adminWhoami doğrulanmış çağıranın kimliğini döner (GET /admin/v1/auth/me).
//
// Kimlik ÇEKİRDEKTEN gelir: corehttp.RequireAdmin onu context'e koyar. Bu uç
// bu yüzden korumanın gerçekten bağlı olduğunun da kanıtıdır — koruma yoksa
// kimlik de olmaz ve 401 döner.
//
// # BU UÇ YETKİ İSTEMEZ
//
// Kimlik yeterlidir: uç, çağıranın ZATEN sahip olduğu yetkileri geri okur.
// Yetki isteseydi, yetkisiz bir kullanıcı neye yetkili olmadığını da
// öğrenemez ve 403'ün nedenini kendi kimliğine bakarak göremezdi.
func (h *Handler) adminWhoami(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	principal, ok := corehttp.PrincipalFromContext(ctx)
	if !ok {
		corehttp.WriteError(ctx, w, coreerrors.Unauthorized(
			corehttp.CodeUnauthenticated, "kimlik doğrulama gerekli"))
		return
	}

	writeItem(w, r, http.StatusOK, principalResponse{
		ID:              principal.ID,
		Kind:            principal.Kind,
		Scopes:          orEmpty(principal.Scopes),
		SalesChannelIDs: principal.SalesChannelIDs,
	})
}

// --- kullanıcılar -------------------------------------------------------------

// adminCreateUser yeni bir yönetim kullanıcısı oluşturur
// (POST /admin/v1/users).
//
// Gövdedeki parola boş bırakılabilir; o durumda kullanıcı parolasız oluşturulur
// ve giriş yapabilmesi için önce POST /admin/v1/users/{id}/password çağrılır.
//
// Uç [ScopeWrite] ister; ayrıca istenen yetkiler çağıranınkileri AŞAMAZ
// (bkz. service.CreateUser).
func (h *Handler) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createUserRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateUser(ctx, toCreateUserInput(req), string(req.Password))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toUserDTO(created))
}

// adminListUsers kullanıcıları süzerek ve sayfalayarak listeler
// (GET /admin/v1/users).
//
// Süzgeçler: email, scope.
func (h *Handler) adminListUsers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListUsers(ctx, service.ListUsersInput{
		Email:  stringParam(r, "email"),
		Scope:  stringParam(r, "scope"),
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toUserDTO)
}

// adminGetUser tek bir kullanıcıyı döner (GET /admin/v1/users/{id}).
func (h *Handler) adminGetUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	user, err := h.svc.GetUser(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toUserDTO(user))
}

// adminUpdateUser kullanıcının verilen alanlarını günceller
// (PUT /admin/v1/users/{id}).
//
// Uç [ScopeWrite] ister; gövdedeki yetki listesi çağıranınkileri AŞAMAZ
// (bkz. service.UpdateUser).
func (h *Handler) adminUpdateUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateUserRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateUser(ctx, pathParam(r, paramID), toUpdateUserInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toUserDTO(updated))
}

// adminDeleteUser kullanıcıyı ve giriş kimliklerini yumuşak siler
// (DELETE /admin/v1/users/{id}).
func (h *Handler) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteUser(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminSetPassword kullanıcının parolasını belirler
// (POST /admin/v1/users/{id}/password).
//
// Uç ayrıdır: profil güncellemesiyle aynı gövdeye konsaydı, adını değiştiren
// bir isteğin yanlışlıkla parolayı da sıfırlaması mümkün olurdu.
//
// Yanıt GÖVDESİZDİR (204): parolayla ilgili hiçbir şeyi geri yazmanın anlamı
// yoktur.
func (h *Handler) adminSetPassword(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req setPasswordRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.SetPassword(ctx, pathParam(r, paramID), string(req.Password)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- api anahtarları ----------------------------------------------------------

// adminCreateAPIKey yeni bir API anahtarı üretir (POST /admin/v1/api-keys).
//
// DÜZ ANAHTAR YALNIZCA BU YANITTA döner ve bir daha hiçbir uçtan okunamaz.
// created_by alanı gövdeden DEĞİL, doğrulanmış kimlikten doldurulur; aksi
// hâlde denetim kaydını istemci yazardı.
//
// Uç [ScopeWrite] ister; gövdedeki yetki listesi çağıranınkileri AŞAMAZ
// (bkz. service.CreateAPIKey). İkisi birlikte, bu ucun yetki yükseltme
// aracına dönüşmesini engeller.
func (h *Handler) adminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req createAPIKeyRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	key, plaintext, err := h.svc.CreateAPIKey(ctx, service.CreateAPIKeyInput{
		Type:            models.APIKeyType(req.Type),
		Title:           req.Title,
		Scopes:          req.Scopes,
		CreatedBy:       actorID(ctx),
		SalesChannelIDs: req.SalesChannelIDs,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	corehttp.WriteJSON(ctx, w, http.StatusCreated, itemEnvelope{Data: createAPIKeyResponse{
		APIKey: toAPIKeyDTO(key),
		Key:    plaintext,
	}})
}

// adminListAPIKeys anahtarları süzerek ve sayfalayarak listeler
// (GET /admin/v1/api-keys).
//
// Süzgeçler: type, revoked. Yanıtta düz anahtar YOKTUR; yalnızca maskelenmiş
// gösterim vardır.
func (h *Handler) adminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	revoked, err := boolParam(r, "revoked")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	var keyType *models.APIKeyType
	if raw := stringParam(r, "type"); raw != nil {
		value := models.APIKeyType(*raw)
		keyType = &value
	}

	page, err := h.svc.ListAPIKeys(ctx, service.ListAPIKeysInput{
		Type:    keyType,
		Revoked: revoked,
		Limit:   limit,
		Offset:  offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toAPIKeyDTO)
}

// adminGetAPIKey tek bir anahtarı döner (GET /admin/v1/api-keys/{id}).
func (h *Handler) adminGetAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	key, err := h.svc.GetAPIKey(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAPIKeyDTO(key))
}

// adminRevokeAPIKey anahtarı iptal eder
// (POST /admin/v1/api-keys/{id}/revoke).
//
// İptal SİLME DEĞİLDİR: kayıt listede kalır ve ne zaman, kim tarafından
// kapatıldığı görünür. Zaten iptalli bir anahtarda 409 döner.
func (h *Handler) adminRevokeAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	revoked, err := h.svc.RevokeAPIKey(ctx, pathParam(r, paramID), actorID(ctx))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAPIKeyDTO(revoked))
}

// adminDeleteAPIKey anahtarı yumuşak siler
// (DELETE /admin/v1/api-keys/{id}).
//
// Sızıntı sonrası tercih edilmesi gereken işlem iptaldir; silme, yanlışlıkla
// oluşturulmuş bir kaydı temizlemek içindir.
func (h *Handler) adminDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteAPIKey(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminListKeyChannels anahtarın bağlı olduğu kanalları döner
// (GET /admin/v1/api-keys/{id}/sales-channels).
//
// Devre dışı kanallar da listelenir: yönetim yüzeyi bağı olduğu gibi
// göstermelidir.
func (h *Handler) adminListKeyChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channels, err := h.svc.SalesChannelsOfAPIKey(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(channels, toSalesChannelDTO))
}

// adminLinkKeyChannel publishable anahtarı bir satış kanalına bağlar
// (POST /admin/v1/api-keys/{id}/sales-channels).
func (h *Handler) adminLinkKeyChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	keyID := pathParam(r, paramID)

	var req linkChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.LinkSalesChannel(ctx, keyID, req.SalesChannelID); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Bağ kurulduktan sonra GÜNCEL liste döner: istemcinin ikinci bir istek
	// yapmasına gerek kalmaz.
	channels, err := h.svc.SalesChannelsOfAPIKey(ctx, keyID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(channels, toSalesChannelDTO))
}

// adminUnlinkKeyChannel bağı kaldırır
// (DELETE /admin/v1/api-keys/{id}/sales-channels/{sales_channel_id}).
func (h *Handler) adminUnlinkKeyChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.UnlinkSalesChannel(
		ctx, pathParam(r, paramID), pathParam(r, paramChannelID),
	); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// --- satış kanalları ----------------------------------------------------------

// adminCreateSalesChannel yeni bir satış kanalı oluşturur
// (POST /admin/v1/sales-channels).
func (h *Handler) adminCreateSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req salesChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateSalesChannel(ctx, service.SalesChannelInput{
		Name:        req.Name,
		Description: req.Description,
		IsDisabled:  req.IsDisabled,
		Metadata:    req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toSalesChannelDTO(created))
}

// adminListSalesChannels kanalları süzerek ve sayfalayarak listeler
// (GET /admin/v1/sales-channels).
//
// Süzgeçler: name, is_disabled.
func (h *Handler) adminListSalesChannels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	isDisabled, err := boolParam(r, "is_disabled")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListSalesChannels(ctx, service.ListSalesChannelsInput{
		Name:       stringParam(r, "name"),
		IsDisabled: isDisabled,
		Limit:      limit,
		Offset:     offset,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toSalesChannelDTO)
}

// adminGetSalesChannel tek bir kanalı döner
// (GET /admin/v1/sales-channels/{id}).
func (h *Handler) adminGetSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	channel, err := h.svc.GetSalesChannel(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toSalesChannelDTO(channel))
}

// adminUpdateSalesChannel kanalın verilen alanlarını günceller
// (PUT /admin/v1/sales-channels/{id}).
func (h *Handler) adminUpdateSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateSalesChannelRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateSalesChannel(ctx, pathParam(r, paramID), service.UpdateSalesChannelInput{
		Name:        req.Name,
		Description: req.Description,
		IsDisabled:  req.IsDisabled,
		Metadata:    req.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toSalesChannelDTO(updated))
}

// adminDeleteSalesChannel kanalı yumuşak siler ve anahtar bağlarını kaldırır
// (DELETE /admin/v1/sales-channels/{id}).
func (h *Handler) adminDeleteSalesChannel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteSalesChannel(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
