package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// Bu dosyadaki uçlar müşterinin KENDİ profilini ve adreslerini yönetir.
//
// KORUMA YOKTUR — bkz. paket belgesindeki "UYARI: store uçları Faz 8'e kadar
// KORUMASIZDIR". Müşteri kimliği [storeCustomerID] ile okunur ve Faz 8'de
// oturum belirtecine bağlanacak tek nokta orasıdır.

// storeRegisterGuest misafir müşteri kaydı açar (POST /store/v1/customers).
//
// Aynı e-postayla daha önce misafir kaydı ya da kayıtlı bir hesap bulunması
// engel DEĞİLDİR: misafir kaydı bir kimlik değil, tek seferlik bir alışverişin
// iletişim bilgisidir (gerekçe için bkz. models.Customer). Hesap açma Faz 8'de,
// auth modülüyle birlikte gelecektir; o zamana kadar bir misafir,
// POST /admin/v1/customers/{id}/convert-to-account ile hesaba çevrilir.
func (h *Handler) storeRegisterGuest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req customerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	guest, err := h.svc.RegisterGuest(ctx, toCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toCustomerDTO(guest))
}

// storeGetCustomer müşterinin kendi profilini döner
// (GET /store/v1/customers/{id}).
func (h *Handler) storeGetCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customer, err := h.svc.GetCustomer(ctx, storeCustomerID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(customer))
}

// storeUpdateCustomer müşterinin kendi profilini günceller
// (PUT /store/v1/customers/{id}).
func (h *Handler) storeUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req updateCustomerRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	updated, err := h.svc.UpdateCustomer(ctx, storeCustomerID(r), toUpdateCustomerInput(req))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCustomerDTO(updated))
}

// storeListAddresses müşterinin adreslerini döner
// (GET /store/v1/customers/{id}/addresses).
func (h *Handler) storeListAddresses(w http.ResponseWriter, r *http.Request) {
	h.listAddresses(w, r, storeCustomerID(r))
}

// storeCreateAddress müşterinin yeni adresini ekler
// (POST /store/v1/customers/{id}/addresses).
func (h *Handler) storeCreateAddress(w http.ResponseWriter, r *http.Request) {
	h.createAddress(w, r, storeCustomerID(r))
}

// storeUpdateAddress müşterinin adresini günceller
// (PUT /store/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) storeUpdateAddress(w http.ResponseWriter, r *http.Request) {
	h.updateAddress(w, r, storeCustomerID(r))
}

// storeDeleteAddress müşterinin adresini yumuşak siler
// (DELETE /store/v1/customers/{id}/addresses/{address_id}).
func (h *Handler) storeDeleteAddress(w http.ResponseWriter, r *http.Request) {
	h.deleteAddress(w, r, storeCustomerID(r))
}

// storeSetDefaultShipping adresi varsayılan kargo adresi yapar
// (POST /store/v1/customers/{id}/addresses/{address_id}/default-shipping).
func (h *Handler) storeSetDefaultShipping(w http.ResponseWriter, r *http.Request) {
	h.setDefaultShipping(w, r, storeCustomerID(r))
}

// storeSetDefaultBilling adresi varsayılan fatura adresi yapar
// (POST /store/v1/customers/{id}/addresses/{address_id}/default-billing).
func (h *Handler) storeSetDefaultBilling(w http.ResponseWriter, r *http.Request) {
	h.setDefaultBilling(w, r, storeCustomerID(r))
}

// --- iki ad alanının paylaştığı adresle ilgili gövdeler --------------------------------
//
// Adresle ilgili uçlar admin ve store tarafında AYNI işi yapar; farkları yalnızca
// müşteri kimliğinin nereden geldiğidir. Gövdeler bu yüzden tek yerde durur:
// iki kopya, yalnızca birinde düzeltilen bir doğrulama hatası demek olurdu.

// listAddresses verilen müşterinin adreslerini yazar.
func (h *Handler) listAddresses(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	addresses, err := h.svc.ListAddresses(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItems(w, r, convertAll(addresses, toAddressDTO))
}

// createAddress verilen müşterinin yeni adresini ekler.
func (h *Handler) createAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	var req addressRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	address, err := h.svc.CreateAddress(ctx, customerID, service.AddressInput{
		FirstName:         req.FirstName,
		LastName:          req.LastName,
		Company:           req.Company,
		Address1:          req.Address1,
		Address2:          req.Address2,
		City:              req.City,
		CountryCode:       req.CountryCode,
		PostalCode:        req.PostalCode,
		Phone:             req.Phone,
		IsDefaultShipping: req.IsDefaultShipping,
		IsDefaultBilling:  req.IsDefaultBilling,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toAddressDTO(address))
}

// updateAddress verilen müşterinin adresini günceller.
func (h *Handler) updateAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	var req updateAddressRequest
	if err := decodeBody(w, r, &req); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	address, err := h.svc.UpdateAddress(ctx, customerID, pathParam(r, paramAddressID),
		service.UpdateAddressInput{
			FirstName:   req.FirstName,
			LastName:    req.LastName,
			Company:     req.Company,
			Address1:    req.Address1,
			Address2:    req.Address2,
			City:        req.City,
			CountryCode: req.CountryCode,
			PostalCode:  req.PostalCode,
			Phone:       req.Phone,
		})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}

// deleteAddress verilen müşterinin adresini yumuşak siler.
func (h *Handler) deleteAddress(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	if err := h.svc.DeleteAddress(ctx, customerID, pathParam(r, paramAddressID)); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// setDefaultShipping verilen müşterinin adresini varsayılan kargo adresi yapar.
//
// İşaret güncelleme gövdesinde DEĞİL ayrı bir uçtadır: işaret müşterinin diğer
// adreslerini de ilgilendirir (eskisi temizlenir) ve tek satırlık bir
// güncellemeyle ifade edilemez (bkz. updateAddressRequest).
func (h *Handler) setDefaultShipping(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	address, err := h.svc.SetDefaultShippingAddress(ctx, customerID, pathParam(r, paramAddressID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}

// setDefaultBilling verilen müşterinin adresini varsayılan fatura adresi yapar.
func (h *Handler) setDefaultBilling(w http.ResponseWriter, r *http.Request, customerID string) {
	ctx := r.Context()

	address, err := h.svc.SetDefaultBillingAddress(ctx, customerID, pathParam(r, paramAddressID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toAddressDTO(address))
}
