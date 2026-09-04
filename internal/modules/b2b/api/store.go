package api

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// Bu dosyadaki uçlar müşterinin KENDİ şirketini ve KENDİ çalışan kaydını
// döner. İkisi de aynı servis çağrısına (MembershipOfCustomer) dayanır: müşteri
// bir şirketin çalışanı değilse ikisi de 404 verir.
//
// Şirket kimliğiyle çağrılan bir uç YOKTUR; gerekçesi ve customer idnin
// hangi anlamda doğrulanmadığı paket belgesindedir.

// storeGetCompany müşterinin kendi şirketini döner
// (GET /store/v1/b2b/customers/{customer_id}/company).
func (h *Handler) storeGetCompany(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	membership, err := h.svc.MembershipOfCustomer(ctx, storeCustomerID(r))
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

	membership, err := h.svc.MembershipOfCustomer(ctx, storeCustomerID(r))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toStoreEmployeeDTO(membership))
}
