package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// Bu dosyadaki uçlar müşterinin KENDİ şirketini ve KENDİ çalışan kaydını
// döner. İkisi de aynı servis çağrısına (MembershipOfCustomer) dayanır: müşteri
// bir şirketin çalışanı değilse ikisi de 404 verir.
//
// Şirket kimliğiyle çağrılan bir uç YOKTUR; gerekçesi paket belgesindedir.
//
// Both resolve the customer through [Handler.storeCustomerID], which refuses
// the request when the installation's bound identity CONTRADICTS the customer
// the path claims (ADR 0057; with none bound it refuses nothing). The call is
// the FIRST thing each handler does, so a refused caller never reaches the
// service — and never learns from a 404 whether the identifier it guessed
// belongs to anybody.

// storeGetCompany müşterinin kendi şirketini döner
// (GET /store/v1/b2b/customers/{customer_id}/company).
func (h *Handler) storeGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	membership, err := h.svc.MembershipOfCustomer(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toCompanyDTO(membership.Company))
}

// storeGetEmployee müşterinin kendi çalışan kaydını döner
// (GET /store/v1/b2b/customers/{customer_id}/employee).
//
// Yanıt harcama limitini, limitin sıfırlanma aralığını ve geçerli pencerenin
// başlangıcını taşır; KALAN hak taşımaz (bkz. storeEmployeeDTO).
func (h *Handler) storeGetEmployee(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	customerID, err := h.storeCustomerID(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	membership, err := h.svc.MembershipOfCustomer(ctx, customerID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreEmployeeDTO(membership))
}
