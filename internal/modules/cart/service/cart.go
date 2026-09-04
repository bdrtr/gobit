package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/cart/models"
)

// CreateCartInput yeni bir sepetin alanlarıdır.
type CreateCartInput struct {
	// RegionID sepetin bölgesidir; ZORUNLUDUR. Bölgenin gerçekten var olduğu
	// BURADA doğrulanmaz: region modülünü tanıyan taraf workflow'dur
	// (ADR 0001/0006).
	RegionID string
	// CustomerID sepetin sahibidir; OPSİYONELDİR. Boş bırakılırsa sepet
	// MİSAFİRE aittir.
	CustomerID string
	// Email sepetin iletişim adresidir; opsiyoneldir.
	Email string
	// CurrencyCode ISO 4217 kodudur; ZORUNLUDUR.
	//
	// Para birimi bölgenin verisidir ve bölgeden KOPYALANIR. Kopyanın sepette
	// durmasının sebebi tarihseldir: bölge para birimini sonradan
	// değiştirirse, açık sepetlerin tutarları sessizce başka bir para biriminde
	// okunmamalıdır.
	//
	// Kopyalayan taraf HER ZAMAN SUNUCUDUR ve bu servisin DIŞINDADIR:
	// create_cart workflow'u ülke kodundan hem bölgeyi hem para birimini
	// çözer ve vitrin ucu da o akıştan geçer. Servis bu soruyu kendisi
	// soramaz — region modülünü çağırmaz (ADR 0006) — ve bu yüzden yalnızca
	// kodun BİÇİMİNİ doğrular. Alan bir zamanlar vitrin gövdesinden, yani
	// İSTEMCİDEN geliyordu; kaldırılma gerekçesi api.createCartRequest
	// godoc'unda yazılıdır.
	CurrencyCode string
	// Metadata çağıranın serbest ek verisidir.
	Metadata map[string]any
}

// CreateCart yeni bir sepet oluşturur.
//
// Sepetin bölgesi ve (varsa) müşterisi KENDİ SÜTUNLARINDA durur
// (carts.region_id / carts.customer_id) ve ikisi de sepet satırıyla AYNI
// INSERT'te yazılır. Bu yüzden yarım kalmış bir sepet yoktur: satır ya bölgesi
// ve sahibiyle birlikte doğar ya da hiç doğmaz.
func (s *Service) CreateCart(ctx context.Context, in CreateCartInput) (models.Cart, error) {
	if err := requireID("region_id", in.RegionID); err != nil {
		return models.Cart{}, err
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return models.Cart{}, err
		}
	}
	currency, err := normalizeCurrency(in.CurrencyCode)
	if err != nil {
		return models.Cart{}, err
	}
	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Cart{}, err
	}

	cart, err := s.store.CreateCart(ctx, models.Cart{
		ID:           models.NewCartID(),
		RegionID:     in.RegionID,
		CustomerID:   in.CustomerID,
		Email:        email,
		CurrencyCode: currency,
		Metadata:     in.Metadata,
	})
	if err != nil {
		return models.Cart{}, err
	}
	return cart, nil
}

// GetCart sepeti satırları, adresleri ve kargo yöntemleriyle birlikte döner.
//
// Çocuklar ÜÇ sabit sorguyla getirilir; satır ya da kayıt sayısı ne olursa olsun
// sorgu sayısı değişmez (N+1 yoktur). Sepet yoksa ya da yumuşak silinmişse
// errors.NotFound döner.
//
// # Neden salt-okunur işlem
//
// Dört sorgu TEK ANLIK GÖRÜNTÜ üzerinde koşar ([Store.WithReadTx]). Kilit
// alınmaz; sağlanan tek şey, dördünün de sepetin AYNI hâlini görmesidir.
// İşlemsiz hâlinde her sorgu havuzdan başka bir bağlantı ve başka bir anlık
// görüntü alabiliyordu: araya giren bir [Service.AddLineItem] ya da
// [Service.SetTotals], sepet başlığı YENİ toplamları taşırken satır listesinin
// ESKİ hâlini döndürebiliyor ve müşteriye "toplam 3000 ama tek satır 1000" gibi
// kendi içinde tutarsız bir sepet gösterilebiliyordu. Yazma yolları kilitli
// olduğu için veri bozulmuyordu; YIRTILAN yalnızca okuma görünümüydü, ama
// müşterinin ödeme sayfasında gördüğü şey odur.
func (s *Service) GetCart(ctx context.Context, cartID string) (models.CartDetail, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.CartDetail{}, err
	}

	var detail models.CartDetail
	err := s.store.WithReadTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.GetCart(ctx, cartID)
		if err != nil {
			return err
		}

		items, err := s.store.ListLineItems(ctx, cartID)
		if err != nil {
			return err
		}
		addresses, err := s.store.ListCartAddresses(ctx, cartID)
		if err != nil {
			return err
		}
		methods, err := s.store.ListShippingMethods(ctx, cartID)
		if err != nil {
			return err
		}

		detail = models.CartDetail{Cart: cart, Items: items, ShippingMethods: methods}
		for i := range addresses {
			switch addresses[i].Type {
			case models.AddressShipping:
				detail.ShippingAddress = &addresses[i]
			case models.AddressBilling:
				detail.BillingAddress = &addresses[i]
			}
		}
		return nil
	})
	if err != nil {
		return models.CartDetail{}, err
	}
	return detail, nil
}

// UpdateCartInput sepetin iletişim ve sahiplik alanlarının güncellemesidir.
//
// İki alanın biçimi bilinçli olarak FARKLIDIR ve fark sözleşmeyi anlatır:
// e-posta düzeltilebilir ve temizlenebilir, sahiplik ise yalnızca KURULABİLİR.
type UpdateCartInput struct {
	// Email nil ise e-postaya DOKUNULMAZ; boş dize verilirse temizlenir.
	//
	// İşaretçi olmasının sebebi budur: "alanı gönderme" ile "alanı boşalt" ayrı
	// niyetlerdir ve ikisini tek boş dizeye indirgemek, gövdesinde e-posta
	// taşımayan her isteğin sepetin e-postasını sessizce silmesi olurdu.
	Email *string
	// CustomerID boş ise müşteriye DOKUNULMAZ; doluysa MİSAFİR sepeti o
	// müşteriye devredilir.
	//
	// İşaretçi DEĞİLDİR, çünkü "boşalt" geçerli bir niyet değildir: sahipliği
	// geri almak, sepeti kimin açtığını kaybetmek olurdu. Dolu bir sepeti
	// BAŞKA bir müşteriye devretmek de reddedilir (errors.Conflict); iki farklı
	// müşterinin aynı sepeti sahiplenmesi, sipariş kime yazılacağı sorusunu
	// yanıtsız bırakırdı.
	CustomerID string
}

// UpdateCart sepetin e-postasını ve/veya müşterisini günceller.
//
// Gerçek akış bunu gerektirir: müşteri sepeti MİSAFİR olarak açar, e-postasını
// ödeme adımında girer ve/veya araya giriş yapar. Bu yol olmadan sepetin baştan
// kurulması gerekirdi ve satırlar kaybolurdu; üstelik complete_cart siparişin
// iletişim adresini sepetten okuduğu için eksiklik oraya taşınırdı.
//
// # Neden toplamları bayatlatır
//
// Çağrı [Service.mutate] çerçevesinde koşar, yani sepetin şekil sayacını
// ARTIRIR ve toplamları bayat hâle getirir. Sebep sahiplik değişimidir: fiyat
// müşteri grubuna, vergi ise muafiyete göre değişebilir ve sepetin sahibi
// değiştikten sonra eski hesap artık o sepetin hesabı değildir. Hangisinin
// gerçekten değiştiğini bilen taraf pricing/tax'tır, cart değil (ADR 0006); bu
// yüzden karar temkinli verilir — bir tur fazladan hesaplamak, yanlış tutarla
// sipariş yazmaktan ucuzdur.
//
// Tamamlanmış sepete yazılamaz: errors.Conflict döner.
func (s *Service) UpdateCart(ctx context.Context, cartID string, in UpdateCartInput) (models.Cart, error) {
	var email *string
	if in.Email != nil {
		normalized, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.Cart{}, err
		}
		email = &normalized
	}
	if in.CustomerID != "" {
		if err := requireID("customer_id", in.CustomerID); err != nil {
			return models.Cart{}, err
		}
	}
	if email == nil && in.CustomerID == "" {
		return models.Cart{}, errors.Invalid(CodeInvalidInput,
			"no field was given to update: email ya da customer_id gerekli")
	}

	updated, err := s.mutate(ctx, cartID, func(ctx context.Context, cart models.Cart) error {
		contact := models.CartContact{Email: cart.Email, CustomerID: cart.CustomerID}
		if email != nil {
			contact.Email = *email
		}
		if in.CustomerID != "" {
			if cart.CustomerID != "" && cart.CustomerID != in.CustomerID {
				return errors.Conflict(CodeCustomerMismatch,
					"sepet başka bir müşteriye ait: %s (istenen: %s)", cart.CustomerID, in.CustomerID)
			}
			contact.CustomerID = in.CustomerID
		}

		_, err := s.store.UpdateCartContact(ctx, cart.ID, contact)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return updated, nil
}

// ListCartsInput sepet listelemesinin girdisidir.
type ListCartsInput struct {
	// CustomerID verilirse yalnızca o müşterinin sepetleri döner.
	CustomerID *string
	// RegionID verilirse yalnızca o bölgenin sepetleri döner.
	RegionID *string
	// Completed verilirse sepetler tamamlanmışlığa göre süzülür.
	Completed *bool
	// Page sayfalama parametreleridir.
	Page Page
}

// ListCarts sepetleri sayfalayarak döner (çocukları YÜKLEMEDEN).
//
// İkinci dönüş değeri filtreye uyan TÜM satırların sayısıdır. Satırlar burada
// yüklenmez: sayfa başına onlarca sepetin çocuklarını getirmek listeyi ağır ve
// N+1'e açık hâle getirirdi. Çocuklar yalnızca [Service.GetCart] ile gelir.
func (s *Service) ListCarts(ctx context.Context, in ListCartsInput) ([]models.Cart, int64, error) {
	page, err := in.Page.normalize()
	if err != nil {
		return nil, 0, err
	}

	filter := models.CartFilter{
		Completed: in.Completed,
		Limit:     page.Limit,
		Offset:    page.Offset,
	}
	if in.CustomerID != nil {
		if err := requireID("customer_id", *in.CustomerID); err != nil {
			return nil, 0, err
		}
		filter.CustomerID = in.CustomerID
	}
	if in.RegionID != nil {
		if err := requireID("region_id", *in.RegionID); err != nil {
			return nil, 0, err
		}
		filter.RegionID = in.RegionID
	}
	return s.store.ListCarts(ctx, filter)
}

// ListCartsByIDs verilen kimliklerin sepetlerini TEK sorguda döner.
// Bulunamayan kimlik için kayıt dönmez; bu bir hata değildir.
func (s *Service) ListCartsByIDs(ctx context.Context, ids []string) ([]models.Cart, error) {
	if len(ids) == 0 {
		return []models.Cart{}, nil
	}
	return s.store.CartsByIDs(ctx, ids)
}

// DeleteCart sepeti ve çocuklarını yumuşak siler.
//
// TAMAMLANMIŞ sepet silinemez (errors.Conflict): siparişin dayandığı kayıt
// odur ve silinmesi geçmişi yok etmek olurdu.
func (s *Service) DeleteCart(ctx context.Context, cartID string) error {
	if err := requireID("cart_id", cartID); err != nil {
		return err
	}

	return s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		if err := s.store.SoftDeleteLineItemsByCart(ctx, cartID); err != nil {
			return err
		}
		if err := s.store.SoftDeleteCartAddressesByCart(ctx, cartID); err != nil {
			return err
		}
		if err := s.store.SoftDeleteShippingMethodsByCart(ctx, cartID); err != nil {
			return err
		}
		return s.store.SoftDeleteCart(ctx, cartID)
	})
}

// MarkCompleted sepeti tamamlanmış olarak damgalar.
//
// Faz 6'daki complete_cart saga'sı bunu ÇAĞIRIR; damga atıldıktan sonra sepet
// DEĞİŞTİRİLEMEZ ve aynı sepetten ikinci bir sipariş doğamaz.
//
// # Neden ikinci çağrı Conflict
//
// Tamamlanmış bir sepeti tekrar tamamlamak sessizce başarılı sayılsaydı, aynı
// sepetin iki kez sipariş edildiği bir akış hiçbir yerde hata üretmezdi.
// Yeniden deneme güvenliği (plan Bölüm 2.6) burada değil, workflow motorunun
// idempotency-key'inde çözülür: BAŞARIYLA biten bir adım tekrar çalıştırılmaz.
//
// # Neden satırsız sepet reddedilir
//
// Satırsız bir sepetten doğacak sipariş, hiçbir şeyin satılmadığı bir
// sipariştir. Kural ayrıca ikinci bir deliği kapatır: "toplamlar HİÇ
// hesaplanmadı" durumunu. Bayatlık ölçütü totals_revision ≠ revision'dır ve
// yeni bir sepette ikisi de sıfır olduğu için "hiç hesaplanmadı" ile "sıfırıncı
// şekil için hesaplandı" ayırt edilemez. Ayırt edilmesi de GEREKMEZ: sayaç
// hiçbir yerde azalmaz ve satır eklemek onu mutlaka artırır, dolayısıyla
// revision = totals_revision olan bir sepetin satırı varsa [Service.SetTotals]
// o şekil için GERÇEKTEN koşmuştur. Geriye yalnızca hiç dokunulmamış (ve
// zorunlu olarak satırsız) sepet kalır; onu da bu kapı reddeder. Alternatif,
// şemaya "hesaplandı mı" damgası eklemekti — aynı sonucu veren, ama sürüm
// defterini büyüten bir yol.
//
// # Neden bayat toplam reddedilir
//
// Toplamlar sepetin güncel şekline ait değilse ([models.Cart.TotalsStale]),
// sepet hesaplandıktan SONRA değişmiştir — ödeme sayfası açıkken sepete satır
// eklenen klasik durum. Damga atmak, o anki yanlış tutarı sipariş tutarı hâline
// getirirdi. Bu yüzden çağrı errors.Conflict döner ve calculate_totals'ın
// yeniden çalıştırılmasını ister.
func (s *Service) MarkCompleted(ctx context.Context, cartID string) (models.Cart, error) {
	if err := requireID("cart_id", cartID); err != nil {
		return models.Cart{}, err
	}

	var completed models.Cart
	err := s.store.WithTx(ctx, func(ctx context.Context) error {
		cart, err := s.store.LockCart(ctx, cartID)
		if err != nil {
			return err
		}
		if cart.Completed() {
			return completedError(cart.ID)
		}
		items, err := s.store.ListLineItems(ctx, cart.ID)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return errors.Conflict(CodeCartEmpty,
				"sepet tamamlanamaz: hiç satırı yok (%s)", cart.ID)
		}
		if cart.TotalsStale() {
			return errors.Conflict(CodeTotalsStale,
				"sepet tamamlanamaz: toplamlar güncel değil (sepet şekli %d, toplamlar %d); calculate_totals yeniden çalıştırılmalı",
				cart.Revision, cart.TotalsRevision)
		}
		completed, err = s.store.MarkCartCompleted(ctx, cartID)
		return err
	})
	if err != nil {
		return models.Cart{}, err
	}
	return completed, nil
}
