package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// --- müşteriler ---------------------------------------------------------------

// adminCreateCustomer KAYITLI bir müşteri hesabı oluşturur
// (POST /admin/v1/customers).
//
// Yönetim ucu daima HESAP açar; misafir kaydı vitrin akışının parçasıdır ve
// POST /store/v1/customers ile yapılır. Ayrım gövdedeki bir bayrağa
// bırakılsaydı, yönetim isteği sessizce benzersizlik kuralının dışına düşerdi.
func (h *Handler) adminCreateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req customerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	created, err := h.svc.CreateCustomer(ctx, toCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCustomerDTO(created))
}

// adminListCustomers müşterileri süzerek ve sayfalayarak listeler
// (GET /admin/v1/customers).
//
// Süzgeçler: email, has_account, group_id. "email" süzgeci MİSAFİRLERİ DE
// getirir; aynı e-postayla birden çok misafir kaydı olabildiği için sonuç
// birden fazla satır içerebilir (bkz. models.Customer).
func (h *Handler) adminListCustomers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	hasAccount, err := boolParam(r, "has_account")
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	after, err := afterParam(r, service.CustomerListing, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListCustomers(ctx, service.ListCustomersInput{
		Email:      stringParam(r, "email"),
		HasAccount: hasAccount,
		GroupID:    stringParam(r, "group_id"),
		Limit:      limit,
		Offset:     offset,
		After:      after,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toCustomerDTO)
}

// adminGetCustomer tek bir müşteriyi döner (GET /admin/v1/customers/{id}).
func (h *Handler) adminGetCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, err := h.svc.GetCustomer(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// adminUpdateCustomer müşterinin verilen alanlarını günceller
// (PUT /admin/v1/customers/{id}).
func (h *Handler) adminUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateCustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateCustomer(ctx, pathParam(r, paramID), toUpdateCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(updated))
}

// adminDeleteCustomer müşteriyi ve adreslerini yumuşak siler
// (DELETE /admin/v1/customers/{id}).
func (h *Handler) adminDeleteCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteCustomer(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminConvertGuest misafir kaydını kayıtlı hesaba çevirir
// (POST /admin/v1/customers/{id}/convert-to-account).
//
// E-posta zaten kayıtlı bir hesaba aitse ya da kayıt zaten hesapsa 409 döner;
// sınıflandırma servisten gelir, handler status seçmez.
func (h *Handler) adminConvertGuest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := pathParam(r, paramID)

	if err := h.svc.ConvertGuestToAccount(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	// Dönüşüm sonrası kaydın GÜNCEL hâli döner: istemcinin has_account alanını
	// görmek için ikinci bir istek yapması gerekmez.
	customer, err := h.svc.GetCustomer(ctx, id)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// --- gruplar ------------------------------------------------------------------

// adminCreateGroup yeni bir müşteri grubu oluşturur
// (POST /admin/v1/customer-groups).
func (h *Handler) adminCreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req groupRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	group, err := h.svc.CreateGroup(ctx, service.GroupInput{Name: req.Name, Metadata: req.Metadata})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toGroupDTO(group))
}

// adminListGroups grupları sayfalayarak listeler
// (GET /admin/v1/customer-groups).
func (h *Handler) adminListGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit, offset, err := pageParams(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	page, err := h.svc.ListGroups(ctx, limit, offset)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writePage(w, r, page, toGroupDTO)
}

// adminGetGroup tek bir grubu döner (GET /admin/v1/customer-groups/{id}).
func (h *Handler) adminGetGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	group, err := h.svc.GetGroup(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminUpdateGroup grubun verilen alanlarını günceller
// (PUT /admin/v1/customer-groups/{id}).
//
// Aynı adda başka bir canlı grup varsa 409 döner; sınıflandırma servisten
// gelir, handler status seçmez.
func (h *Handler) adminUpdateGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateGroupRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	group, err := h.svc.UpdateGroup(ctx, pathParam(r, paramID),
		service.UpdateGroupInput{Name: req.Name, Metadata: req.Metadata})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toGroupDTO(group))
}

// adminDeleteGroup grubu yumuşak siler
// (DELETE /admin/v1/customer-groups/{id}).
//
// Üyelikler kaldırılmaz ama silinen grup hiçbir okumada görünmez; adı da
// yeniden kullanılabilir hâle gelir (bkz. service.Service.DeleteGroup).
func (h *Handler) adminDeleteGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.DeleteGroup(ctx, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminAddToGroup müşteriyi gruba ekler
// (POST /admin/v1/customer-groups/{id}/customers).
//
// İşlem idempotenttir; zaten üye olan müşteri için de 204 döner.
func (h *Handler) adminAddToGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req groupMemberRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	if err := h.svc.AddToGroup(ctx, req.CustomerID, pathParam(r, paramID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminRemoveFromGroup müşteriyi gruptan çıkarır
// (DELETE /admin/v1/customer-groups/{id}/customers/{customer_id}).
func (h *Handler) adminRemoveFromGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	err := h.svc.RemoveFromGroup(ctx, pathParam(r, paramCustomerID), pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// adminListGroupsOfCustomer müşterinin gruplarını döner
// (GET /admin/v1/customers/{id}/groups).
func (h *Handler) adminListGroupsOfCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	groups, err := h.svc.ListGroupsOf(ctx, pathParam(r, paramID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(groups, toGroupDTO))
}

// --- adresler -----------------------------------------------------------------

// adminListAddresses müşterinin adreslerini döner
// (GET /admin/v1/customers/{id}/addresses).
func (h *Handler) adminListAddresses(w http.ResponseWriter, r *http.Request) {
	h.listAddresses(w, r, pathParam(r, paramID))
}

// adminCreateAddress müşterinin yeni adresini ekler
// (POST /admin/v1/customers/{id}/addresses).
func (h *Handler) adminCreateAddress(w http.ResponseWriter, r *http.Request) {
	h.createAddress(w, r, pathParam(r, paramID))
}

// adminUpdateAddress adresi günceller
// (PUT /admin/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) adminUpdateAddress(w http.ResponseWriter, r *http.Request) {
	h.updateAddress(w, r, pathParam(r, paramID))
}

// adminDeleteAddress adresi yumuşak siler
// (DELETE /admin/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) adminDeleteAddress(w http.ResponseWriter, r *http.Request) {
	h.deleteAddress(w, r, pathParam(r, paramID))
}

// adminSetDefaultShipping adresi varsayılan kargo adresi yapar
// (POST /admin/v1/customers/{id}/addresses/{address_id}/default-shipping).
//
// Uç, vitrindekinin yönetim tarafındaki EŞİDİR. Olmasaydı yönetici mevcut bir
// adresi varsayılan yapmak için onu yeniden oluşturmak zorunda kalırdı —
// güncelleme gövdesinde işaret yoktur ve yeniden oluşturma adresin kimliğini
// değiştirirdi.
func (h *Handler) adminSetDefaultShipping(w http.ResponseWriter, r *http.Request) {
	h.setDefaultShipping(w, r, pathParam(r, paramID))
}

// adminSetDefaultBilling adresi varsayılan fatura adresi yapar
// (POST /admin/v1/customers/{id}/addresses/{address_id}/default-billing).
func (h *Handler) adminSetDefaultBilling(w http.ResponseWriter, r *http.Request) {
	h.setDefaultBilling(w, r, pathParam(r, paramID))
}

// toCustomerInput istek gövdesini servis girdisine çevirir.
func toCustomerInput(req customerRequest) service.CustomerInput {
	return service.CustomerInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     req.Phone,
		Metadata:  req.Metadata,
	}
}

// toUpdateCustomerInput güncelleme gövdesini servis girdisine çevirir.
func toUpdateCustomerInput(req updateCustomerRequest) service.UpdateCustomerInput {
	return service.UpdateCustomerInput{
		Email:     req.Email,
		FirstName: req.FirstName,
		LastName:  req.LastName,
		Phone:     req.Phone,
		Metadata:  req.Metadata,
	}
}
