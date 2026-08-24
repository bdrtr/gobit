package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
)

// listProviders kayıtlı ödeme sağlayıcılarının kimliklerini döner.
//
// Hem admin hem store yüzeyine bağlıdır: vitrinin ödeme adımı hangi yolların
// açık olduğunu bilmek zorundadır ve bu bilgi gizli değildir.
func (h *Handler) listProviders(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	writeList(ctx, w, h.svc.ProviderIDs(ctx))
}

// createCollectionRequest POST /admin/v1/payment-collections gövdesidir.
type createCollectionRequest struct {
	Reference string `json:"reference"`
	// Amount minor unit TAM SAYIDIR; işaretçi olması "gönderilmedi" ile
	// "sıfır gönderildi" ayrımını korur ve ikisi de reddedilir ama farklı
	// mesajla.
	Amount       *int64         `json:"amount"`
	CurrencyCode string         `json:"currency_code"`
	Metadata     map[string]any `json:"metadata"`
}

// createCollection yeni bir ödeme koleksiyonu oluşturur.
func (h *Handler) createCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createCollectionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	if body.Amount == nil {
		corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest, "amount zorunludur"))
		return
	}

	col, err := h.svc.CreatePaymentCollection(ctx, service.CreateCollectionInput{
		Reference:    body.Reference,
		Amount:       *body.Amount,
		CurrencyCode: body.CurrencyCode,
		Metadata:     body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toCollectionDTO(col)})
}

// listCollections koleksiyonları sayfalayarak döner.
func (h *Handler) listCollections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	page, err := parsePage(r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	in := service.ListCollectionsInput{Page: page}
	if raw := r.URL.Query().Get("reference"); raw != "" {
		in.Reference = &raw
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		in.Status = &raw
	}

	collections, count, err := h.svc.ListPaymentCollections(ctx, in)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]collectionDTO, 0, len(collections))
	for i := range collections {
		data = append(data, toCollectionDTO(collections[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, listEnvelope{
		Data:   data,
		Count:  count,
		Offset: page.Offset,
		Limit:  page.Limit,
	})
}

// getCollection koleksiyonu kimliğiyle döner.
func (h *Handler) getCollection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	col, err := h.svc.GetPaymentCollection(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toCollectionDTO(col)})
}

// createSessionRequest ADMIN yüzeyinin ödeme oturumu açma gövdesidir.
type createSessionRequest struct {
	ProviderID string `json:"provider_id"`
	// Amount verilmezse koleksiyonun KALAN tutarının tamamı için oturum
	// açılır. Ödemeyi birden çok oturuma bölmek yönetim işidir; mağaza
	// yüzeyinde bu alan YOKTUR (bkz. [createStoreSessionRequest]).
	Amount int64 `json:"amount"`
	// IdempotencyKey zorunludur: aynı anahtarla ikinci istek YENİ oturum açmaz.
	IdempotencyKey string `json:"idempotency_key"`
	// Data sağlayıcıya iletilen serbest veridir.
	Data json.RawMessage `json:"data"`
}

// createStoreSessionRequest MAĞAZA yüzeyinin ödeme oturumu açma gövdesidir.
//
// Tutar alanı BİLİNÇLİ OLARAK yoktur: mağaza ucundan açılan oturum her zaman
// koleksiyonun kalan tutarının tamamını kapar. Müşterinin kendi tarayıcısından
// oturumun tutarını belirleyebilmesi, 50.000'lik bir sipariş için 1 birimlik
// oturum açıp ödemeyi atlatması demekti. Alan gövdede gönderilirse istek
// reddedilir (bkz. [decodeBody] — tanınmayan alanlar hata verir).
type createStoreSessionRequest struct {
	ProviderID string `json:"provider_id"`
	// IdempotencyKey zorunludur: aynı anahtarla ikinci istek YENİ oturum açmaz.
	IdempotencyKey string `json:"idempotency_key"`
	// Data sağlayıcıya iletilen serbest veridir; sağlayıcının DAVRANIŞINI
	// yönlendiren anahtarlar burada kabul edilmez.
	Data json.RawMessage `json:"data"`
}

// storeBlockedDataKeys mağaza yüzeyinin sağlayıcıya İLETMEDİĞİ veri
// anahtarlarıdır.
//
// Manuel sağlayıcının test kancaları (ret enjeksiyonu, kısmi yetkilendirme)
// oturumun serbest verisinden okunur ve oturumla birlikte saklanır. Müşteriye
// açık uçtan geçirilirlerse müşteri kendi ödemesinin sonucunu yazabilir:
// 50.000'lik bir koleksiyonda 1 birim bloke ettirip siparişi ödenmiş
// gösterebilirdi. Kancalar yönetim yüzeyinde kalır; orayı çağıran zaten
// tahsilatı da tetikleyebilir.
//
// Liste sessizce SÜZÜLMEZ, istek REDDEDİLİR: yutulan bir alan, istemcinin
// gönderdiğini sandığı ama uygulanmayan bir ayardır (aynı gerekçe için bkz.
// [decodeBody]).
var storeBlockedDataKeys = []string{
	manual.DataKeyOutcome,
	manual.DataKeyDeclineReason,
	manual.DataKeyAuthorizedAmount,
}

// createSession ADMIN yüzeyinden koleksiyon için ödeme oturumu açar.
func (h *Handler) createSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createSessionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data, err := decodeSessionData(body.Data)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	ses, err := h.svc.CreateSession(ctx, chi.URLParam(r, "id"), body.ProviderID, service.CreateSessionInput{
		Amount:         body.Amount,
		IdempotencyKey: body.IdempotencyKey,
		Data:           data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toSessionDTO(ses)})
}

// createStoreSession MAĞAZA yüzeyinden koleksiyon için ödeme oturumu açar.
//
// Admin ucundan iki farkı vardır ve ikisi de müşterinin ödeme akışını kendi
// lehine yönlendirmesini engeller: tutar İSTEMCİDEN alınmaz (her zaman kalanın
// tamamı) ve sağlayıcının davranış anahtarları reddedilir.
func (h *Handler) createStoreSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createStoreSessionRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data, err := decodeSessionData(body.Data)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	for _, key := range storeBlockedDataKeys {
		if _, ok := data[key]; ok {
			corehttp.WriteError(ctx, w, coreerrors.Invalid(codeInvalidRequest,
				"%s alanı mağaza yüzeyinde kabul edilmiyor: %q", "data", key))
			return
		}
	}

	ses, err := h.svc.CreateSession(ctx, chi.URLParam(r, "id"), body.ProviderID, service.CreateSessionInput{
		// Tutar verilmez: koleksiyonun KALAN tutarının tamamı için oturum açılır.
		IdempotencyKey: body.IdempotencyKey,
		Data:           data,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toSessionDTO(ses)})
}

// listSessions koleksiyonun oturumlarını döner.
func (h *Handler) listSessions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	sessions, err := h.svc.ListPaymentSessions(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]sessionDTO, 0, len(sessions))
	for i := range sessions {
		data = append(data, toSessionDTO(sessions[i]))
	}
	writeList(ctx, w, data)
}

// getSession oturumu kimliğiyle döner.
func (h *Handler) getSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ses, err := h.svc.GetPaymentSession(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toSessionDTO(ses)})
}

// authorizeSession oturumu yetkilendirir.
//
// Sağlayıcı reddederse servis errors.Conflict döner ve yanıt 409 olur: ret bir
// sunucu hatası değildir ama istenen geçiş de gerçekleşmemiştir.
func (h *Handler) authorizeSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ses, err := h.svc.AuthorizePayment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toSessionDTO(ses)})
}

// amountRequest yalnızca tutar taşıyan gövdelerin şeklidir.
type amountRequest struct {
	// Amount sıfır ya da hiç verilmezse "tamamı" anlamına gelir.
	Amount int64 `json:"amount"`
}

// captureSession bloke edilmiş tutarı tahsil eder.
func (h *Handler) captureSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	body, err := decodeOptionalAmount(w, r)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	payment, err := h.svc.CapturePayment(ctx, chi.URLParam(r, "id"), body.Amount)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toPaymentDTO(payment)})
}

// cancelSession oturumu iptal eder (saga telafisi).
//
// İDEMPOTENTTİR: zaten iptal edilmiş bir oturum için de 204 döner.
func (h *Handler) cancelSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.CancelPayment(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// cancelStoreSession müşterinin kendi açtığı ödeme oturumunu bırakmasıdır.
//
// Yönetim yüzeyindeki [Handler.cancelSession] ile AYNI servis çağrısını yapar;
// ayrı bir uç olmasının sebebi yetkilendirmedir (Faz 8'de store ve admin
// yüzeyleri farklı korunacak), davranış farkı değildir.
//
// Var olma sebebi rezervasyondur: açık bir oturum koleksiyonun kalan tutarını
// kapatır ve çift tahsilatı bu engeller. Bırakma yolu olmadan müşteri ödeme
// YÖNTEMİNİ değiştiremez ve bir yönetici müdahalesine kadar kilitli kalırdı.
//
// İDEMPOTENTTİR: zaten iptal edilmiş bir oturum için de 204 döner.
func (h *Handler) cancelStoreSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.svc.CancelPayment(ctx, chi.URLParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listPayments koleksiyonun tahsilatlarını döner.
func (h *Handler) listPayments(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payments, err := h.svc.ListPayments(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]paymentDTO, 0, len(payments))
	for i := range payments {
		data = append(data, toPaymentDTO(payments[i]))
	}
	writeList(ctx, w, data)
}

// getPayment tahsilatı kimliğiyle döner.
func (h *Handler) getPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	payment, err := h.svc.GetPayment(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: toPaymentDTO(payment)})
}

// refundRequest POST /admin/v1/payments/{id}/refunds gövdesidir.
type refundRequest struct {
	// Amount sıfır ya da hiç verilmezse kalan tutarın tamamı iade edilir.
	Amount int64  `json:"amount"`
	Reason string `json:"reason"`
}

// refundPayment tahsilatı iade eder.
func (h *Handler) refundPayment(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body refundRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	refund, err := h.svc.RefundPayment(ctx, chi.URLParam(r, "id"), body.Amount, body.Reason)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated, singleEnvelope{Data: toRefundDTO(refund)})
}

// listRefunds tahsilatın iadelerini döner.
func (h *Handler) listRefunds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	refunds, err := h.svc.ListRefunds(ctx, chi.URLParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	data := make([]refundDTO, 0, len(refunds))
	for i := range refunds {
		data = append(data, toRefundDTO(refunds[i]))
	}
	writeList(ctx, w, data)
}

// decodeOptionalAmount gövdesi İSTEĞE BAĞLI olan tutar isteklerini çözer.
//
// Tahsilat ve iptal uçlarında gövde göndermemek geçerlidir ve "tamamı"
// anlamına gelir; boş gövdeyi hata saymak, en yaygın çağrıyı gereksiz bir JSON
// nesnesi yazmaya zorlardı.
func decodeOptionalAmount(w http.ResponseWriter, r *http.Request) (amountRequest, error) {
	var body amountRequest
	if r.ContentLength == 0 {
		return body, nil
	}
	if err := decodeBody(w, r, &body); err != nil {
		return amountRequest{}, err
	}
	return body, nil
}

// decodeSessionData sağlayıcıya iletilecek ham veriyi haritaya çevirir.
//
// Sayılar json.Number olarak çözülür: haritadan geçen bir tam sayı float64'e
// dönerse yeniden kodlanırken üstel gösterime kayabilir ve para hiçbir aşamada
// kayan noktaya uğramamalıdır (plan Bölüm 8).
func decodeSessionData(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"data alanı JSON nesnesi olmalı")
	}
	return out, nil
}
