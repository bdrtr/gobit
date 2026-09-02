package checkout

import (
	"context"
	"slices"
	"time"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/workflow"
)

// Adımlar arası taşınan verinin anahtarları.
//
// Anahtarlar telafinin TEK bilgi kaynağıdır: bir Compensate, kendi Invoke'unun
// ne ürettiğini buradan öğrenir (bkz. [workflow.StepContext].Shared). Önek,
// aynı haritayı ileride başka bir bileşen kullanırsa çakışmayı önler.
const (
	sharedReservations = "checkout.reservations"
	sharedOrderID      = "checkout.order_id"
	sharedCollectionID = "checkout.collection_id"
	sharedSessionID    = "checkout.session_id"
	sharedPaymentID    = "checkout.payment_id"
	// sharedCaptureAttempted tahsilat çağrısının BAŞLATILDIĞINI bildirir ve
	// pivot korumasının gerçek tetiğidir (bkz. [Workflows.skipAfterCapture]).
	//
	// İşaret çağrıdan ÖNCE konur, çünkü "para gitti mi" sorusunun cevabı
	// tahsilat kimliğinde DEĞİLDİR: sağlayıcı parayı çekip yanıtı kaybettiğinde
	// Capture hata döner ve geriye hiçbir kimlik kalmaz. Kimliğe bağlı bir
	// koruma o durumda kapanır ve saga ödenmiş siparişi geri alırdı.
	//
	// İşaret YALNIZCA koleksiyon hiçbir tahsilat olmadığını KANITLADIĞINDA
	// silinir (bkz. capturePaymentStep.settle).
	sharedCaptureAttempted = "checkout.capture_attempted"
)

// reservationRef bir satır için alınmış rezervasyonun izidir.
type reservationRef struct {
	// LineItemID rezervasyonun açıldığı sepet satırıdır.
	LineItemID string `json:"line_item_id"`
	// ReservationID stok modülünün ürettiği rezervasyon kimliğidir.
	ReservationID string `json:"reservation_id"`
	// LocationID stoğun AYRILDIĞI lokasyondur.
	//
	// Geri bırakmak için gerekmez — telafi yalnızca rezervasyon kimliğini
	// kullanır — ama kayda YAZILIR: lokasyon satır başına seçilebildiğinden
	// (bkz. [CompleteCartInput.LocationID]) bir siparişin satırları farklı
	// depolardan ayrılmış olabilir. Elle müdahale eden operatörün "hangi depo"
	// sorusunu yürütme kaydından yanıtlayabilmesi gerekir; alan olmasaydı cevap
	// yalnızca stok modülüne tek tek sorularak bulunurdu.
	LocationID string `json:"location_id"`
}

// sharedRefs paylaşılan haritadan rezervasyon izlerini okur.
//
// Anahtar hiç yazılmamışsa boş dilim döner: bu, adımın henüz hiçbir rezervasyon
// almadığı normal durumdur. Anahtar DOLU ama tipi beklenmedikse hata döner;
// sessizce boş dönmek, telafinin geri alacağı işi bulamadan "başardım"
// demesine yol açardı.
func sharedRefs(sc *workflow.StepContext) ([]reservationRef, error) {
	raw, exists := sc.Shared[sharedReservations]
	if !exists {
		return nil, nil
	}
	refs, ok := raw.([]reservationRef)
	if !ok {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"%q anahtarı beklenmedik tipte: %T", sharedReservations, raw)
	}
	return refs, nil
}

// sharedText paylaşılan haritadan bir kimlik okur.
//
// Anahtar yoksa boş dize döner; tipi beklenmedikse hata döner (bkz.
// [sharedRefs]).
func sharedText(sc *workflow.StepContext, key string) (string, error) {
	raw, exists := sc.Shared[key]
	if !exists {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", errors.Internal(CodeSharedStateInvalid,
			"%q anahtarı beklenmedik tipte: %T", key, raw)
	}
	return value, nil
}

// sharedFlag paylaşılan haritadan bir işaret okur.
//
// Anahtar yoksa false döner; tipi beklenmedikse hata döner (bkz.
// [sharedRefs]).
func sharedFlag(sc *workflow.StepContext, key string) (bool, error) {
	raw, exists := sc.Shared[key]
	if !exists {
		return false, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return false, errors.Internal(CodeSharedStateInvalid,
			"%q anahtarı beklenmedik tipte: %T", key, raw)
	}
	return value, nil
}

// skipAfterCapture telafinin PIVOT'tan sonra çalışıp çalışmayacağını söyler.
//
// Tahsilat DENENDİYSE telafi zinciri İLERİ GİTMEZ: sipariş iptal edilmez, stok
// geri bırakılmaz, blokaj serbest bırakılmaz. Sebep tek cümledir — para
// çekilmişken siparişi geri almak, müşteriyi hem parasından hem siparişinden
// etmek olurdu; oysa saga'nın telafi etmeye çalıştığı şey tam da bunun
// tersidir.
//
// Ölçü "tahsilat BAŞARILI oldu mu" değil "tahsilat DENENDİ mi"dir
// (bkz. [sharedCaptureAttempted]). Başarıya bakan bir koruma, ödeme
// sağlayıcısının parayı çekip yanıtı kaybettiği durumda — yani korumanın en
// çok gerektiği durumda — kapanırdı. İşaret yalnızca koleksiyon hiçbir
// tahsilat olmadığını KANITLADIĞINDA silinir, dolayısıyla "geri al" kararı
// kanıta dayanır, kimliğin varlığına değil.
//
// Kararın sessiz kalmadığını iki şey garanti eder: her atlama ERROR olarak
// loglanır ve yürütmenin durumu her hâlükârda compensation_failed olur —
// tahsilat adımı başarılıysa kendi Compensate'i "geri alınamaz" hatası döner,
// başarısızsa hatası [workflow.ErrUncompensated] taşır.
func (w *Workflows) skipAfterCapture(ctx context.Context, sc *workflow.StepContext, step, cartID string) (bool, error) {
	paymentID, err := sharedText(sc, sharedPaymentID)
	if err != nil {
		return false, err
	}
	attempted, err := sharedFlag(sc, sharedCaptureAttempted)
	if err != nil {
		return false, err
	}
	if paymentID == "" && !attempted {
		return false, nil
	}

	w.log.ErrorContext(ctx, "telafi atlandı: tahsilat denenmiş, ödenmiş olabilecek sipariş geri alınmaz",
		"step", step, "cart_id", cartID, "payment_id", paymentID, "capture_attempted", attempted)
	return true, nil
}

// cleanupContext bir adımın KENDİ temizliği için iptalden etkilenmeyen, süreli
// bir bağlam üretir.
//
// Motor telafi zincirini context.WithoutCancel ile çalıştırır, ama Invoke'un
// içindeki kendi temizliği adımın bağlamıyla kalır; oysa temizliğin en çok
// gerektiği durumlardan biri tam da bağlamın ölmesidir. Saga çağıranın
// iptalinden ayrılmış olsa bile (bkz. [sagaContext]) kendi süre bütçesi
// tükenebilir ve tükendiği an, yarıda kalmış bir yan etkinin durduğu andır.
// Bütçe telafi bütçesiyle aynıdır: ikisi de aynı işi (yan etkiyi geri alma)
// yapar.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), CompensationTimeout)
}

// retryCleanup adımın KENDİ temizliğini telafi politikasıyla yeniden dener.
//
// Adım içi temizlik (yarıda kalan ayırmanın geri bırakılması, yarıda kalan
// yetkilendirmenin blokajı) motorun telafisiyle AYNI işi yapar; tek fark
// hatanın hangi yolda yakalandığıdır. Politikanın da aynı olması bu yüzden
// zorunludur: aksi hâlde geçici bir arıza, yalnızca hangi yolda yakalandığına
// göre elle müdahale üretir ya da üretmezdi. Gerekçe [compensationRetry] ile
// aynıdır — başarısız bir telafinin bedeli elle müdahaledir.
//
// Kalıcı hatalar (errors.Conflict, errors.Invalid) ve ölü bağlam
// [workflow.DefaultRetryable] tarafından zaten elenir; onları denemek yalnızca
// gecikme üretirdi.
func retryCleanup(ctx context.Context, attempt func() error) error {
	policy := compensationRetry()
	backoff := policy.Backoff

	for i := 1; ; i++ {
		err := attempt()
		if err == nil {
			return nil
		}
		if i >= policy.MaxAttempts || !workflow.DefaultRetryable(err) {
			return err
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return err
		case <-timer.C:
		}

		backoff = time.Duration(float64(backoff) * policy.Multiplier)
		if backoff > policy.MaxBackoff {
			backoff = policy.MaxBackoff
		}
	}
}

// releaseAll verilen rezervasyonları geri bırakır ve BIRAKILAMAYANLARI döner.
//
// Zincir ilk hatada DURMAZ: bir rezervasyonun bırakılamaması, diğerlerinin
// asılı kalması için gerekçe değildir. Hatalar errors.Join ile birleştirilir.
func (w *Workflows) releaseAll(ctx context.Context, refs []reservationRef) ([]reservationRef, error) {
	var (
		remaining []reservationRef
		failures  []error
	)
	for i := range refs {
		if err := w.inventory.ReleaseReservation(ctx, refs[i].ReservationID); err != nil {
			remaining = append(remaining, refs[i])
			failures = append(failures, errors.Wrap(err, errors.KindOf(err), CodeReservationLeaked,
				"%s rezervasyonu geri bırakılamadı (satır %s)", refs[i].ReservationID, refs[i].LineItemID))
			continue
		}
		w.log.DebugContext(ctx, "rezervasyon geri bırakıldı",
			"reservation_id", refs[i].ReservationID, "line_item_id", refs[i].LineItemID)
	}
	return remaining, errors.Join(failures...)
}

// reserveInventoryStep sepetin her satırı için stok ayırır.
type reserveInventoryStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// reserveOutput stok adımının yürütme kaydına yazılan çıktısıdır.
type reserveOutput struct {
	// Reservations alınan rezervasyonlardır.
	Reservations []reservationRef `json:"reservations"`
}

// Name adımın adını döner.
func (s *reserveInventoryStep) Name() string { return StepReserveInventory }

// Invoke satır başına stok ayırır ve kimlikleri paylaşılan haritaya yazar.
//
// Kimlikler her başarılı ayırmadan SONRA yazılır, hepsi bittikten sonra değil:
// telafi (ve motorun en iyi çaba telafisi) elindeki tek bilgi kaynağı olarak
// o haritayı okur ve yarıda kalmış bir adımın izini orada bulamazsa ayrılmış
// stok asılı kalırdı.
//
// # Yarıda kalan adım KENDİ temizliğini yapar
//
// Bir satır patlarsa o ana kadar alınmış rezervasyonlar BURADA geri bırakılır.
// Sebep motorun sözleşmesidir: tek denemede patlayan adım telafi EDİLMEZ, bu
// yüzden "ya tümüyle başarılı ol ya da arkanda iş bırakma" borcu adıma aittir
// (bkz. core/workflow paket yorumu). Temizlik de patlarsa hata
// [workflow.ErrUncompensated] ile sarılır: motor gözcüyü görünce yürütmeyi
// "geri alındı" değil compensation_failed olarak yazar ve elle müdahale
// istenir.
//
// # BOŞ rezervasyon kimliği başarı sayılmaz
//
// Stok modülü hata dönmeden boş bir kimlik döndürürse ayırma YAPILMIŞ ama izi
// elimizde YOK demektir: ne bu adım ne de telafi onu geri bırakabilir. Sessizce
// kabul etmek, listede görünmeyen bir rezervasyonu sonsuza kadar asılı
// bırakırdı; bu yüzden durum [workflow.ErrUncompensated] ile bildirilir ve o
// ana kadar alınmış rezervasyonlar yine geri bırakılır.
//
// # Lokasyon satır BAŞINA belirlenir
//
// Çağıran bir lokasyon bildirmediyse her satırın deposu ayrı ayrı belirlenir
// (bkz. [reserveInventoryStep.locationFor]) ve bir siparişin satırları farklı
// depolardan ayrılabilir. Adaylar ve tercih sırası ayırmanın hemen ÖNÜNDE
// çözülür, hazırlıkta değil: adaylar kilitsiz okunan bir olgudur ve okuma ile
// ayırma arasındaki her milisaniye, listeye giren bir deponun Reserve'e
// gelindiğinde tükenmiş olma ihtimalidir. Yarış tümüyle kapanmaz — kapatan tek
// şey Reserve'ün kendi kilididir — ama pencere gereksiz yere büyütülmez;
// sıralama bu yüzden satır başına BİR kez sorulur.
//
// Sıralama da bir satırın patlayabileceği yeni bir noktadır ve tıpkı ayırma gibi
// [reserveInventoryStep.unwind]'a düşer: önceki satırların rezervasyonları
// geri bırakılır. Çok depolu bir sepette bu durum daha kolay oluşur — ilk satır
// bir depodan ayrılmışken ikinci satır hiçbir depoda bulunamayabilir.
func (s *reserveInventoryStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	refs := make([]reservationRef, 0, len(s.plan.Lines))

	for i := range s.plan.Lines {
		line := s.plan.Lines[i]

		locationID, reservationID, err := s.reserveLine(ctx, line)
		if err != nil {
			return nil, s.unwind(ctx, sc, refs, line, locationID, err)
		}
		if reservationID == "" {
			return nil, s.unwind(ctx, sc, refs, line, locationID, errors.Join(
				errors.Internal(CodeEmptyIdentifier,
					"stok modülü %s satırı için BOŞ rezervasyon kimliği döndürdü; ayrılan stok geri bırakılamaz",
					line.LineItemID),
				workflow.ErrUncompensated))
		}

		refs = append(refs, reservationRef{
			LineItemID:    line.LineItemID,
			ReservationID: reservationID,
			LocationID:    locationID,
		})
		sc.Shared[sharedReservations] = refs
	}

	s.w.log.DebugContext(ctx, "stok ayrıldı",
		"cart_id", s.plan.CartID, "lines", len(refs))
	return reserveOutput{Reservations: refs}, nil
}

// locationFor satırın stoğunun ayrılabileceği ADAY lokasyonları döner.
//
// Tek bir lokasyon değil LİSTE döner ve bunun sebebi somut: adaylar kilitsiz
// okunur, ayırma kilit altında yapılır ve aradaki pencerede seçilen depo
// tükenebilir. Tek lokasyon dönseydi çağıranın geri düşecek yeri kalmaz,
// sipariş başka depoda stok dururken düşerdi (bkz. [reserveInventoryStep.reserveLine]).
//
// # Çağıran bildirdiyse SEÇİM YOKTUR
//
// [CompleteCartInput.LocationID] doluysa tek elemanlı bir liste döner ve
// hiçbir modüle sorulmaz. Bildirilen lokasyon bir tercih değil TALİMATTIR;
// onu "aday" sayıp kargo modülüne onaylatmak, çağıranın kararını sessizce
// değiştirebilirdi.
//
// # Boşsa adaylar STOK modülünden gelir
//
// Hangi depolarda yeterli adet olduğu bir OLGUDUR ve stok modülünün işidir.
// Hangisinden gönderileceği bir KARARDIR ve kargo modülüne aittir
// (bkz. [reserveInventoryStep.sirala]). Bölünme bilinçlidir: iki yarıyı tek
// yüzeyde toplamak, stok sorgusunu kargo politikasına ya da kargo politikasını
// stok şemasına bağımlı kılardı.
//
// # Aday yoksa kararı BU paket verir
//
// Stok modülü boş liste döner, hata değil (bkz. [Inventory.LocationsWithStock]);
// "sipariş verilemez" sonucunu çıkaran bu adımdır ve sınıf, Reserve'ün yetersiz
// stokta döndüğüyle AYNIDIR (errors.Conflict, [CodeReservationFailed]). Boş
// listeyi kargo modülüne sormak da aynı sınıfta bir hata üretirdi ama yanlış
// modülü işaret ederdi: eksik olan gönderilecek bir depo değil, ayrılacak
// STOKTUR ve operatörün mesajda görmesi gereken kalem ile adettir.
func (s *reserveInventoryStep) locationFor(ctx context.Context, line planLine) ([]string, error) {
	if s.plan.LocationID != "" {
		return []string{s.plan.LocationID}, nil
	}

	candidates, err := s.w.inventory.LocationsWithStock(ctx, line.InventoryItemID, line.Quantity)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, errors.Conflict(CodeReservationFailed,
			"%s kaleminden %d adet ayrılabilecek lokasyon yok", line.InventoryItemID, line.Quantity)
	}

	return candidates, nil
}

// reserveLine satırın stoğunu ayırır ve kullanılan lokasyonu döner.
//
// # Neden tek adayla yetinmiyoruz
//
// Aday listesi KİLİTSİZ okunur, ayırma ise kilit altında yapılır. Aradaki
// pencerede sıranın başındaki deponun stoğu tükenmiş olabilir ve o zaman
// Reserve errors.Conflict döner. Tek adayla yetinen bir uygulama siparişin
// TAMAMINI düşürürdü — üstelik BAŞKA bir depoda yeterli stok dururken.
//
// Bu yalnızca teorik bir yarış değildir: sıra deterministiktir, yani eşzamanlı
// gelen her sipariş AYNI depoyu dener ve hepsi aynı satırda çarpışır.
// Deterministik sıra çakışmayı azaltmaz, yoğunlaştırır.
//
// # Sıra BİR KEZ sorulur
//
// Kargo modülü satır başına tek kez çağrılır ve tercih sırasını verir; geri
// düşme o listede bir sonrakine geçmektir. Her tükenişte yeniden sormak aynı
// cevabı üretirdi (sıra deterministiktir) ama her defasında politika
// kayıtlarını yeniden okurdu: N adaylı bir satır için bir sorgu yerine N sorgu
// ve her biri, adayların KİLİTSİZ okunmasıyla ayırmanın KİLİTLİ yapılması
// arasındaki yarış penceresini uzatan bir gidiş-dönüş.
//
// # Neden bu, adımı yeniden denemek DEĞİLDİR
//
// Motorun adım tekrarı bilinçli olarak kapalıdır (bkz. [Workflows.CompleteCart]):
// Reserve iki kez çağrılırsa iki rezervasyon üretir. Burada öyle bir risk
// yoktur — geri düşülen çağrı BAŞARISIZ olmuş, yani hiçbir rezervasyon
// bırakmamıştır. Denenen şey aynı iş değil, aynı işin BAŞKA bir deposudur.
//
// # Yalnızca ÇAKIŞMADA geri düşülür
//
// errors.Conflict, [Inventory.Reserve] sözleşmesinde "yeterli stok yok"
// demektir ve başka bir depoda cevabı farklı olabilir. Diğer hata sınıfları
// (erişilemeyen veritabanı, geçersiz girdi) her depoda AYNI cevabı verir;
// onlarda ısrar etmek arızayı gizleyip gecikmeyi aday sayısıyla çarpardı.
//
// Çağıran bir lokasyon BİLDİRDİYSE sıra tektir ve geri düşülecek yer yoktur:
// bildirilen lokasyon bir tercih değil talimattır.
//
// # Döngü SONLANIR
//
// Sıra sonlu bir dilimdir ve her tur bir eleman ilerler; sonlanma, kargo
// modülünün ne döndüğünden bağımsız olarak dilimin uzunluğuyla sınırlıdır.
// Bu, sıranın bir kez sorulmasının ikinci kazancıdır: eskiden sonlanma,
// seçilen adayın listeden düşürülebilmesine — yani modülün aday kümesinin
// DIŞINA çıkmamasına — bağlıydı.
func (s *reserveInventoryStep) reserveLine(
	ctx context.Context, line planLine,
) (locationID, reservationID string, err error) {
	adaylar, err := s.locationFor(ctx, line)
	if err != nil {
		return "", "", err
	}

	sirali, err := s.sirala(ctx, line, adaylar)
	if err != nil {
		return "", "", err
	}

	var sonHata error

	for i, secilen := range sirali {
		reservationID, err := s.w.inventory.Reserve(ctx,
			line.InventoryItemID, secilen, line.Quantity, line.LineItemID)

		switch {
		case err == nil:
			return secilen, reservationID, nil
		case !errors.IsConflict(err):
			return secilen, "", err
		}

		sonHata = err

		s.w.log.DebugContext(ctx, "depo tükenmiş, sıradaki adaya geçiliyor",
			"cart_id", s.plan.CartID, "line_item_id", line.LineItemID,
			"location_id", secilen, "sira_uzunlugu", len(sirali), "sira_indeksi", i)
	}

	return "", "", sonHata
}

// sirala adayları kargo modülüne TERCİH SIRASINA dizdirir.
//
// Çağıran lokasyon bildirdiyse sıra yoktur ve hiçbir modüle sorulmaz:
// bildirilen lokasyon bir tercih değil TALİMATTIR; onu "aday" sayıp kargo
// modülüne onaylatmak, çağıranın kararını sessizce değiştirebilirdi.
//
// Aksi hâlde soru İKİYE bölünür: hangi depolarda yeterli stok olduğu bir
// OLGUDUR (stok modülü, çağrılmış olarak elimizde), hangisinden gönderileceği
// bir KARARDIR (kargo modülü). Sırayı bu paketin kurması en kötüsü olurdu —
// sepet akışının depo politikası hakkında söyleyecek bir sözü yoktur.
//
// Karara giren tek bağlam siparişin BÖLGESİDİR ve plandan gelir. Kargo modülü
// deponun o bölgeye hizmet edip etmediğini kendi kaydından bilir; bu paketin
// taşıdığı şey politika değil, politikanın SORUSUDUR. Bölgeden fazlası (örn.
// teslimat adresi) bilinçli olarak geçirilmez: yürütme kaydı kalıcı bir
// defterdir ve plan Bölüm 8 hassas verinin oraya yazılmamasını ister.
//
// # Cevap üç yerden denetlenir
//
// Kargo modülü boş bir sıra, aday olmayan bir kimlik ya da aynı adayı iki kez
// döndürürse hata errors.Internal'dır. Üçü de sözleşmenin ihlalidir ve
// denetlenmeselerdi arıza sebebinden bir modül uzakta çıkardı: aday olmayan bir
// depoya ayırma denenir, yinelenen bir aday aynı depoya iki kez gidilmesine yol
// açardı.
func (s *reserveInventoryStep) sirala(
	ctx context.Context, line planLine, adaylar []string,
) ([]string, error) {
	if s.plan.LocationID != "" {
		return []string{s.plan.LocationID}, nil
	}

	sirali, err := s.w.fulfillment.RankLocations(ctx, s.plan.RegionID, adaylar)
	if err != nil {
		return nil, err
	}
	if len(sirali) == 0 {
		return nil, errors.Internal(CodeReservationFailed,
			"kargo modülü %d aday arasından BOŞ bir sıra döndürdü (kalem %s)",
			len(adaylar), line.InventoryItemID)
	}

	gorulen := make(map[string]struct{}, len(sirali))
	for _, secilen := range sirali {
		if !slices.Contains(adaylar, secilen) {
			return nil, errors.Internal(CodeReservationFailed,
				"kargo modülü aday olmayan bir lokasyon sıraladı: %s (kalem %s)",
				secilen, line.InventoryItemID)
		}
		if _, dup := gorulen[secilen]; dup {
			return nil, errors.Internal(CodeReservationFailed,
				"kargo modülü aynı lokasyonu iki kez sıraladı: %s (kalem %s)",
				secilen, line.InventoryItemID)
		}
		gorulen[secilen] = struct{}{}
	}

	return sirali, nil
}

// unwind yarıda kalan ayırmanın kendi temizliğini yapar ve nihai hatayı üretir.
//
// Temizlik motorun telafisiyle AYNI politikayla yeniden denenir
// (bkz. [retryCleanup]) ve her denemede yalnızca KALAN rezervasyonlara
// dokunulur: bırakılmış bir rezervasyonu yeniden bırakmak gereksizdir ve
// listeyi budamak, hangi kimliğin gerçekten asılı kaldığını görünür tutar.
//
// locationID satırın deposudur ve İKİ durumda boştur: sıra hiç kurulamadığında
// (aday yok ya da hepsi elendi) ve sıradaki depoların HEPSİ denenip
// tükendiğinde. Mesaj ikisinde de "seçilemedi" yazar ve bu, mesajın SÖYLEMEDİĞİ
// şeydir — hangisinin yaşandığı KODDAN okunur (aday yoksa bu paketin kodu,
// eleme boşalttıysa kargo modülünün kodu, tükendiyse stok modülünün kodu).
// Planın lokasyonunu yazmak yanlış olurdu: alan opsiyoneldir ve satır başına
// depo belirlenen bir akışta boş bir alanı mesaja koymak, operatöre "boş
// lokasyona ayırmayı denedik" dedirtirdi.
//
// # Alt hatanın KODU korunur
//
// Sarmalama sınıfı (Kind) zaten alt hatadan devralıyordu; kod da devralınır ve
// [CodeReservationFailed] yalnızca kodsuz bir hata için YEDEKTİR. Kalıp motorun
// kendi sarmalamasından alınmıştır (bkz. [github.com/bdrtr/gobit/internal/core/workflow.CodeStepFailed])
// ve orada gerekçesi ölçülmüş bir bedelle yazılıdır: taşıma katmanı gövdeye tek
// bir makine okunur alan (kod) yazar ve o alan tek bir değere düzleşirse
// istemci farklı arızaları ayırt edemez.
//
// Bedel burada daha da somuttur. Bu adımda üç ayrı dünya aynı sınıfta (409)
// patlar: hiçbir depoda yeterli stok yoktur, seçilen depo yarışta tükenmiştir,
// ya da hiçbir aday siparişin bölgesine HİZMET ETMEZ. Üçüncüsü bir stok sorunu
// DEĞİL, işletmecinin yazdığı bir kargo politikasının sonucudur ve düzeltmesi
// başka bir yerdedir. Kod ezilseydi dolu raflarla "stok ayrılamadı" raporlanır,
// operatör de bakması gereken yeri bulamazdı — mesaj zinciri sebebi taşır ama
// taşıma katmanı yalnızca en dıştaki mesajı yayımlar.
func (s *reserveInventoryStep) unwind(
	ctx context.Context,
	sc *workflow.StepContext,
	refs []reservationRef,
	line planLine,
	locationID string,
	cause error,
) error {
	location := locationID
	if location == "" {
		location = "seçilemedi"
	}
	code := errors.CodeOf(cause)
	if code == "" {
		code = CodeReservationFailed
	}
	failure := errors.Wrap(cause, errors.KindOf(cause), code,
		"%s satırı için stok ayrılamadı (kalem %s, lokasyon %s, adet %d)",
		line.LineItemID, line.InventoryItemID, location, line.Quantity)
	if len(refs) == 0 {
		return failure
	}

	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	remaining := refs
	releaseErr := retryCleanup(cctx, func() error {
		var err error
		remaining, err = s.w.releaseAll(cctx, remaining)
		return err
	})
	sc.Shared[sharedReservations] = remaining
	if releaseErr == nil {
		return failure
	}

	s.w.log.ErrorContext(ctx, "yarıda kalan stok ayırması geri bırakılamadı; elle müdahale gerekir",
		"cart_id", s.plan.CartID, "leaked", len(remaining), "error", releaseErr)

	return errors.Wrap(errors.Join(failure, releaseErr, workflow.ErrUncompensated),
		errors.KindInternal, CodeReservationLeaked,
		"%s sepetinde %d rezervasyon asılı kaldı", s.plan.CartID, len(remaining))
}

// Compensate ayrılan tüm stoğu geri bırakır; İDEMPOTENTTİR.
//
// Zaten bırakılmış bir rezervasyon ikinci çağrıda hata vermez, dolayısıyla
// telafi yeniden denenebilir. Bırakılamayanlar paylaşılan haritada KALIR:
// telafi yeniden denenirse yalnızca onlar denenir ve motorun kaydında hangi
// rezervasyonun asılı olduğu görünür.
//
// Tahsilat yapılmışsa stok GERİ BIRAKILMAZ (bkz. [Workflows.skipAfterCapture]):
// ödenmiş sipariş ayakta kalır ve onun malı hâlâ ayrılmış olmalıdır; bırakmak,
// aynı stoğu ikinci kez satmak olurdu.
func (s *reserveInventoryStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepReserveInventory, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	refs, err := sharedRefs(sc)
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}

	remaining, releaseErr := s.w.releaseAll(ctx, refs)
	sc.Shared[sharedReservations] = remaining
	if releaseErr != nil {
		return errors.Wrap(releaseErr, errors.KindOf(releaseErr), CodeReservationLeaked,
			"%s sepetinin %d rezervasyonu geri bırakılamadı", s.plan.CartID, len(remaining))
	}

	s.w.log.InfoContext(ctx, "telafi: stok rezervasyonları geri bırakıldı",
		"cart_id", s.plan.CartID, "reservations", len(refs))
	return nil
}

// createOrderStep sepetin görüntüsünden sipariş açar.
type createOrderStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// createOrderOutput sipariş adımının yürütme kaydına yazılan çıktısıdır.
type createOrderOutput struct {
	// OrderID açılan siparişin kimliğidir.
	OrderID string `json:"order_id"`
}

// Name adımın adını döner.
func (s *createOrderStep) Name() string { return StepCreateOrder }

// Invoke siparişi açar ve kimliğini paylaşılan haritaya yazar.
//
// Görüntüye idempotency anahtarı olarak YÜRÜTME kimliği konur: aynı yürütmede
// tekrarlanan bir çağrı yeni sipariş açmaz. "order.placed" olayını order
// modülü kendi yayımlar; bu adım hiçbir olay yayımlamaz (bkz. paket yorumu).
//
// # BOŞ sipariş kimliği başarı sayılmaz
//
// Sipariş modülü hata dönmeden boş bir kimlik döndürürse sipariş AÇILMIŞ ama
// izi elimizde YOK demektir. Kimliği sessizce kabul etmek iki yalan üretirdi:
// telafi "sipariş hiç açılmadı" sanıp no-op yapar (YETİM bir sipariş ayakta
// kalır) ve sonuç, order_id'si boş bir "başarılı" sipariş bildirirdi. Durum
// [workflow.ErrUncompensated] ile bildirilir; stok yine geri bırakılır, ama
// yürütme "geri alındı" değil compensation_failed yazılır.
func (s *createOrderStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	payload, err := s.plan.orderSnapshotJSON(sc.ExecutionID)
	if err != nil {
		return nil, err
	}

	orderID, err := s.w.orders.PlaceOrderJSON(ctx, payload)
	if err != nil {
		return nil, err
	}
	if orderID == "" {
		return nil, errors.Wrap(errors.Join(
			errors.Internal(CodeEmptyIdentifier,
				"sipariş modülü BOŞ sipariş kimliği döndürdü; açılmış olabilecek sipariş iptal edilemez"),
			workflow.ErrUncompensated),
			errors.KindInternal, CodeEmptyIdentifier,
			"%s sepetinde kimliksiz bir sipariş asılı kalmış olabilir; ELLE MÜDAHALE gerekir", s.plan.CartID)
	}
	sc.Shared[sharedOrderID] = orderID

	s.w.log.InfoContext(ctx, "sipariş açıldı",
		"cart_id", s.plan.CartID, "order_id", orderID, "amount", s.plan.Amount)
	return createOrderOutput{OrderID: orderID}, nil
}

// Compensate siparişi iptal eder; İDEMPOTENTTİR.
//
// Sipariş hiç açılmadıysa (kimlik yoksa) çağrı no-op'tur: telafi, olmayan bir
// kaydı aramaz.
//
// Tahsilat yapılmışsa sipariş İPTAL EDİLMEZ (bkz.
// [Workflows.skipAfterCapture]): parası çekilmiş bir siparişi iptal etmek,
// müşteriyi hem parasından hem siparişinden etmek olurdu. Sipariş ayakta kalır
// ve elle müdahale sinyali yürütmenin durumundan okunur.
func (s *createOrderStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepCreateOrder, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	orderID, err := sharedText(sc, sharedOrderID)
	if err != nil {
		return err
	}
	if orderID == "" {
		return nil
	}

	if cancelErr := s.w.orders.CancelOrder(ctx, orderID, cancelReason(sc)); cancelErr != nil {
		return cancelErr
	}

	s.w.log.InfoContext(ctx, "telafi: sipariş iptal edildi",
		"cart_id", s.plan.CartID, "order_id", orderID)
	return nil
}

// cancelReason siparişin iptal gerekçesini üretir.
//
// Gerekçe yürütme kimliğini taşır: iptal edilmiş bir siparişe bakan kişi, onu
// hangi akışın ve hangi yürütmenin geri aldığını kayıtta bulabilmelidir.
func cancelReason(sc *workflow.StepContext) string {
	return "complete_cart telafisi (yürütme: " + sc.ExecutionID + ")"
}

// authorizePaymentStep ödeme koleksiyonunu açar, oturum açar ve yetkilendirir.
type authorizePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// authorizeOutput yetkilendirme adımının yürütme kaydına yazılan çıktısıdır.
type authorizeOutput struct {
	// CollectionID ödeme koleksiyonunun kimliğidir.
	CollectionID string `json:"collection_id"`
	// SessionID ödeme oturumunun kimliğidir.
	SessionID string `json:"session_id"`
	// Status oturumun sağlayıcıdan dönen durumudur.
	Status string `json:"status"`
	// Authorized fiilen bloke edilen tutardır (minor unit).
	Authorized int64 `json:"authorized"`
}

// Name adımın adını döner.
func (s *authorizePaymentStep) Name() string { return StepAuthorizePayment }

// Invoke koleksiyonu açar, oturumu açar ve tutarı bloke ettirir.
//
// # TAM ÖDEME KURALI
//
// Bloke edilen tutar toplanması gereken tutarı karşılamıyorsa adım BAŞARISIZ
// olur: authorized < plan.Amount. Kural sağlayıcının DURUM dizesine değil
// SAYIYA bakar, çünkü kısmi yetkilendirmede durum yine "authorized" olur ve
// yalnızca duruma bakan bir saga ödenmemiş bir siparişi onaylardı — bu
// projedeki en ciddi bulgu buydu.
//
// # Yarıda kalan adım
//
// Yetkilendirme patlarsa ya da eksik kalırsa oturum BURADA iptal edilir; aksi
// hâlde kısmen bloke edilmiş bir tutar müşterinin kartında asılı kalırdı ve
// motor tek denemede patlayan adımı telafi etmez. İptal de patlarsa hata
// [workflow.ErrUncompensated] ile sarılır.
//
// Oturum açılamazsa geriye yalnızca BOŞ bir koleksiyon kalır ve o
// temizlenmez: koleksiyon para tutmaz, yalnızca "şu kadar toplanacaktı"
// diyen bir defter satırıdır ve ödeme modülünün yüzeyinde silme yoktur.
//
// # BOŞ kimlikler burada durdurulur
//
// Koleksiyon ya da oturum kimliği boş gelirse adım hemen düşer. Bu, ödeme
// yolundaki kimliklerin EN UCUZ kırılma noktasıdır: henüz yetkilendirme
// yapılmadığı için müşterinin kartında bloke bir tutar yoktur ve tek bedel geri
// alınan bir rezervasyondur. Boş kimlikle devam etmek, tahsilat adımının
// "oturumu bulamadım" demesine ya da telafinin sessizce no-op'a düşmesine yol
// açardı.
func (s *authorizePaymentStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	collectionID, err := s.w.payments.CreateCollection(ctx,
		s.plan.CartID, s.plan.CurrencyCode, s.plan.Amount)
	if err != nil {
		return nil, err
	}
	if collectionID == "" {
		return nil, errors.Internal(CodeEmptyIdentifier,
			"ödeme modülü BOŞ koleksiyon kimliği döndürdü: %s", s.plan.CartID)
	}
	sc.Shared[sharedCollectionID] = collectionID

	sessionID, err := s.w.payments.OpenSessionWithData(ctx,
		collectionID, s.plan.PaymentProviderID, sc.ExecutionID, s.plan.PaymentData)
	if err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.Internal(CodeEmptyIdentifier,
			"ödeme modülü BOŞ oturum kimliği döndürdü: %s (koleksiyon %s)",
			s.plan.CartID, collectionID)
	}
	sc.Shared[sharedSessionID] = sessionID

	status, authorized, err := s.w.payments.Authorize(ctx, sessionID)
	if err != nil {
		return nil, s.releaseHold(ctx, sessionID, err)
	}
	if authorized < s.plan.Amount {
		return nil, s.releaseHold(ctx, sessionID, errors.Conflict(CodePaymentUnderauthorized,
			"bloke edilen tutar toplanması gerekeni karşılamıyor: %d < %d (oturum %s, durum %q)",
			authorized, s.plan.Amount, sessionID, status))
	}

	s.w.log.InfoContext(ctx, "ödeme yetkilendirildi",
		"cart_id", s.plan.CartID, "collection_id", collectionID, "session_id", sessionID,
		"authorized", authorized, "amount", s.plan.Amount)

	return authorizeOutput{
		CollectionID: collectionID,
		SessionID:    sessionID,
		Status:       status,
		Authorized:   authorized,
	}, nil
}

// releaseHold yarıda kalan yetkilendirmenin blokajını serbest bırakır.
//
// İptal motorun telafisiyle AYNI politikayla yeniden denenir
// (bkz. [retryCleanup]): müşterinin kartındaki blokajı geçici bir arıza
// yüzünden asılı bırakmak, aynı arıza telafi zincirinde yakalansaydı
// olmayacak bir sonuçtur.
func (s *authorizePaymentStep) releaseHold(ctx context.Context, sessionID string, cause error) error {
	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	if err := retryCleanup(cctx, func() error {
		return s.w.payments.Cancel(cctx, sessionID)
	}); err != nil {
		s.w.log.ErrorContext(ctx, "yarıda kalan ödeme oturumu iptal edilemedi; elle müdahale gerekir",
			"cart_id", s.plan.CartID, "session_id", sessionID, "error", err)

		return errors.Wrap(errors.Join(cause, err, workflow.ErrUncompensated),
			errors.KindInternal, CodePaymentUnderauthorized,
			"%s oturumunun blokajı serbest bırakılamadı", sessionID)
	}
	return cause
}

// Compensate ödeme oturumunu iptal eder ve blokajı serbest bırakır.
//
// # Tahsilattan sonra NO-OP
//
// Tahsilat blokajı KAPATIR (bkz. payment modülünde CapturePayment): çekilen
// kısım tahsilata dönüşür, çekilmeyen kısım serbest bırakılır. Yani tahsilat
// gerçekleştiyse serbest bırakılacak blokaj YOKTUR ve iptal denemek yalnızca
// errors.Conflict üretirdi. Çekilmiş paranın telafi edilmediğini bildirmek
// tahsilat adımının işidir (bkz. [capturePaymentStep.Compensate]); aynı durumu
// iki adımın birden raporlaması gürültüden başka bir şey üretmezdi.
func (s *authorizePaymentStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	skip, err := s.w.skipAfterCapture(ctx, sc, StepAuthorizePayment, s.plan.CartID)
	if err != nil {
		return err
	}
	if skip {
		return nil
	}

	sessionID, err := sharedText(sc, sharedSessionID)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return nil
	}

	if cancelErr := s.w.payments.Cancel(ctx, sessionID); cancelErr != nil {
		return cancelErr
	}

	s.w.log.InfoContext(ctx, "telafi: ödeme oturumu iptal edildi",
		"cart_id", s.plan.CartID, "session_id", sessionID)
	return nil
}

// capturePaymentStep bloke edilen tutarı tahsil eder.
type capturePaymentStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// captureOutput tahsilat adımının yürütme kaydına yazılan çıktısıdır.
type captureOutput struct {
	// PaymentID oluşan tahsilatın kimliğidir.
	PaymentID string `json:"payment_id"`
	// Captured koleksiyonun tahsil edilmiş toplamıdır (minor unit).
	Captured int64 `json:"captured"`
}

// Name adımın adını döner.
func (s *capturePaymentStep) Name() string { return StepCapturePayment }

// Invoke tutarı tahsil eder ve tahsilatı koleksiyondan DOĞRULAR.
//
// Tahsil edilecek tutar AÇIKÇA verilir (plan.Amount), sıfır değil: sıfır "bloke
// tutarın tamamını çek" demektir ve sağlayıcı istenenden fazlasını bloke
// ettiyse müşteriden fazla tahsil edilirdi.
//
// Tahsilattan sonra koleksiyon yeniden okunur ve captured >= amount doğrulanır.
// Doğrulama yetkilendirmedeki kuralın ikizidir: durum dizesi türetilmiş bir
// özettir ve eksik bir tahsilatı tam gösterecek biçimde değişebilir.
//
// # Doğrulama patlarsa
//
// Para ÇEKİLMİŞTİR ve motor tek denemede patlayan adımı telafi etmez; bu yüzden
// hata [workflow.ErrUncompensated] ile sarılır ve yürütme compensation_failed
// yazılır. nil dönmek ödenmemiş bir siparişi onaylamak, sade bir hata dönmek
// ise çekilmiş parayı sessizce "geri alındı" saymak olurdu.
//
// # BELİRSİZ TAHSİLAT: Capture'ın hata dönmesi "para gitmedi" DEMEK DEĞİLDİR
//
// En pahalı arıza, sağlayıcının parayı çekip yanıtı kaybetmesidir (ağ zaman
// aşımı). Bu durumda Capture hata döner, geriye hiçbir tahsilat kimliği kalmaz
// ve kimliğe bakan bir pivot koruması KAPANIR: saga siparişi iptal eder, stoğu
// bırakır ve müşteri hem parasından hem siparişinden olur. Paket yorumunun
// "asla olmamalı" dediği şey tam olarak budur.
//
// Bu yüzden hata yolu SORUŞTURULUR (bkz. [capturePaymentStep.settle]):
// koleksiyon yeniden okunur ve geri alma yalnızca koleksiyon hiçbir tahsilat
// olmadığını KANITLADIĞINDA yapılır. Kanıt yoksa (okuma da patladıysa) ya da
// tahsilat görünüyorsa saga İLERİ tarafta kalır — sipariş ayakta, stok ayrılmış,
// yürütme compensation_failed — ve düzeltme elle yapılır.
//
// Karar asimetriktir çünkü bedeller asimetriktir: yanlışlıkla geri almanın
// bedeli çekilmiş ama karşılığı olmayan paradır ve onarımı iade akışı,
// muhasebe ve müşteri temasıdır; yanlışlıkla geri ALMAMANIN bedeli ise bekleyen
// bir sipariş, ayrılmış stok ve kartta kalan bir blokajdır — hepsi görünür,
// hepsi geri alınabilir. Şüphe durumunda ucuz olan hata seçilir.
func (s *capturePaymentStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	sessionID, err := sharedText(sc, sharedSessionID)
	if err != nil {
		return nil, err
	}
	collectionID, err := sharedText(sc, sharedCollectionID)
	if err != nil {
		return nil, err
	}
	if sessionID == "" || collectionID == "" {
		return nil, errors.Internal(CodeSharedStateInvalid,
			"tahsilat adımı ödeme oturumunu bulamadı: %s", s.plan.CartID)
	}

	// İşaret çağrıdan ÖNCE konur: bundan sonra oluşacak HER arıza (hata, panik,
	// süre aşımı) "para gitmiş olabilir" anlamına gelir ve pivot koruması
	// devrededir. İşaret yalnızca kanıtlanmış sıfır tahsilatta silinir.
	sc.Shared[sharedCaptureAttempted] = true

	paymentID, err := s.w.payments.Capture(ctx, sessionID, s.plan.Amount)
	if err != nil {
		return nil, s.settle(ctx, sc, collectionID, err)
	}
	if paymentID == "" {
		// Para çekilmiştir ama izi yoktur: iade akışı bile onu bulamaz.
		return nil, s.dangling(errors.Internal(CodeEmptyIdentifier,
			"ödeme modülü BOŞ tahsilat kimliği döndürdü (oturum %s, koleksiyon %s)",
			sessionID, collectionID))
	}
	sc.Shared[sharedPaymentID] = paymentID

	_, amount, _, captured, _, err := s.w.payments.Collection(ctx, collectionID)
	if err != nil {
		return nil, s.dangling(errors.Wrap(err, errors.KindOf(err), CodePaymentUndercaptured,
			"tahsilat doğrulanamadı: koleksiyon %s okunamadı", collectionID))
	}
	// Doğrulama YEREL olarak bilinen tutara demirlenir, ödeme modülünün kendi
	// bildirdiğine DEĞİL.
	//
	// captured < amount karşılaştırması, doğruladığı sistemin kendi raporladığı
	// referansı kullanırdı ve soru "koleksiyon kendi içinde tutarlı mı"ya
	// inerdi. Koleksiyon "0 toplanacaktı, 0 toplandı" dediğinde 3000 birimlik
	// sipariş SIFIR tahsilatla başarılı yazılıyordu. authorize adımının kuralı
	// (authorized < s.plan.Amount) zaten yerel tutara demirli; bu onun ikizidir.
	if captured < s.plan.Amount {
		return nil, s.dangling(errors.Conflict(CodePaymentUndercaptured,
			"tahsil edilen tutar toplanması gerekeni karşılamıyor: %d < %d (koleksiyon %s)",
			captured, s.plan.Amount, collectionID))
	}

	// Koleksiyonun tutarı planla ayrışmışsa bu ayrı bir arızadır: ödeme
	// koleksiyonu saga'nın açtığından farklı bir tutarla açılmış demektir.
	if amount != s.plan.Amount {
		return nil, s.dangling(errors.Internal(CodePaymentUndercaptured,
			"ödeme koleksiyonunun tutarı planla ayrışmış: koleksiyon %d, plan %d (koleksiyon %s)",
			amount, s.plan.Amount, collectionID))
	}

	s.w.log.InfoContext(ctx, "ödeme tahsil edildi",
		"cart_id", s.plan.CartID, "payment_id", paymentID,
		"captured", captured, "amount", amount)

	return captureOutput{PaymentID: paymentID, Captured: captured}, nil
}

// dangling tahsilat SONRASI oluşan bir hatayı asılı yan etki olarak işaretler.
func (s *capturePaymentStep) dangling(cause error) error {
	return errors.Wrap(errors.Join(cause, workflow.ErrUncompensated),
		errors.KindInternal, CodePaymentUndercaptured,
		"%s sepetinde tahsilat yapıldı ama doğrulanamadı; ELLE MÜDAHALE gerekir", s.plan.CartID)
}

// settle BAŞARISIZ bir tahsilat çağrısının ardından paranın gerçekten gidip
// gitmediğini soruşturur ve saga'nın geri alınıp alınmayacağına karar verir.
//
// Üç sonuç vardır ve yalnızca birincisi geri almaya izin verir:
//
//  1. Koleksiyon HİÇBİR tahsilat olmadığını söylüyor (captured == 0). Kanıt
//     budur: "tahsilat denendi" işareti silinir, hata olduğu gibi döner ve motor
//     zinciri TERS SIRADA geri alır — blokaj serbest bırakılır, sipariş iptal
//     edilir, stok bırakılır. Bu, sağlayıcının isteği hiç almadığı ya da açıkça
//     reddettiği normal arızadır.
//  2. Koleksiyon tahsilat GÖRÜYOR (captured > 0). Para gitmiştir; yanıt
//     kaybolmuştur. Geri alma YAPILMAZ.
//  3. Koleksiyon OKUNAMIYOR. Kanıt yoktur ve kanıtsız geri alma, ödenmiş bir
//     siparişi yok etme riskidir. Geri alma YAPILMAZ.
//
// İkinci ve üçüncü durumda hata [workflow.ErrUncompensated] taşır: yürütme
// compensation_failed yazılır ve bu, izlemenin öncelikle sayması gereken ELLE
// MÜDAHALE sinyalidir. Akışın kendiliğinden İLERİ gitmesi (tahsilatı başarılı
// sayıp sepeti kapatması) bilinçli olarak YAPILMAZ: elimizde tahsilat kimliği
// yoktur, dolayısıyla ne siparişe ne muhasebeye yazılacak bir iz vardır ve
// "başarılı" demek, doğrulanamayan bir ödemeyi doğrulanmış göstermek olurdu.
// Kaybolmuş yanıtın mutabakatı (kimliği sağlayıcıdan bulup siparişi ileri
// taşımak) ayrı bir akıştır ve plan Faz 7+'ya aittir.
//
// Koleksiyon okuması çağıranın bağlamıyla DEĞİL, temizlik bağlamıyla yapılır
// (bkz. [cleanupContext]): belirsizliğin en tipik sebebi zaten bağlamın
// ölmesidir ve ölü bir bağlamla sorulan soru cevapsız kalırdı.
func (s *capturePaymentStep) settle(
	ctx context.Context,
	sc *workflow.StepContext,
	collectionID string,
	cause error,
) error {
	cctx, cancel := cleanupContext(ctx)
	defer cancel()

	_, _, _, captured, _, readErr := s.w.payments.Collection(cctx, collectionID)
	switch {
	case readErr != nil:
		s.w.log.ErrorContext(ctx, "tahsilat belirsiz: koleksiyon okunamadı, geri alma YAPILMIYOR; elle müdahale gerekir",
			"cart_id", s.plan.CartID, "collection_id", collectionID,
			"error", cause, "read_error", readErr)

		return errors.Wrap(errors.Join(cause, readErr, workflow.ErrUncompensated),
			errors.KindInternal, CodeCaptureAmbiguous,
			"%s sepetinde tahsilatın sonucu BİLİNMİYOR (koleksiyon %s okunamadı); "+
				"ödenmiş olabilecek sipariş geri alınmaz, ELLE MÜDAHALE gerekir",
			s.plan.CartID, collectionID)

	case captured > 0:
		s.w.log.ErrorContext(ctx, "tahsilat belirsiz: para çekilmiş ama çağrı hata döndü, geri alma YAPILMIYOR",
			"cart_id", s.plan.CartID, "collection_id", collectionID,
			"captured", captured, "amount", s.plan.Amount, "error", cause)

		return errors.Wrap(errors.Join(cause, workflow.ErrUncompensated),
			errors.KindInternal, CodeCaptureAmbiguous,
			"%s sepetinde tahsilat çağrısı hata döndü ama koleksiyonda %d birim tahsil edilmiş görünüyor "+
				"(koleksiyon %s); ödenmiş sipariş geri alınmaz, ELLE MÜDAHALE gerekir",
			s.plan.CartID, captured, collectionID)

	default:
		// KANIT: hiçbir para hareketi yok. Pivot koruması kaldırılır ve saga
		// olağan biçimde geri alınır.
		delete(sc.Shared, sharedCaptureAttempted)
		s.w.log.WarnContext(ctx, "tahsilat yapılamadı; koleksiyon hiçbir hareket bildirmiyor, saga geri alınıyor",
			"cart_id", s.plan.CartID, "collection_id", collectionID, "error", cause)
		return cause
	}
}

// Compensate tahsilatın GERİ ALINMADIĞINI bildirir.
//
// Tahsilat saga'nın PIVOT adımıdır: para çekildikten sonra otomatik geri dönüş
// yoktur. İade, tahsilatın telafisi değil AYRI bir akıştır (plan Faz 7+) ve
// müşteriye, siparişe, muhasebeye ayrı ayrı dokunur; onu sessizce bir telafi
// adımına gizlemek, saga'nın "geri alındı" dediği yerde gerçekte para hareketi
// yaratması demek olurdu.
//
// Bu yüzden tahsilat gerçekleşmişse hata döner: motor yürütmeyi
// compensation_failed yazar ve bu, izlemenin öncelikle sayması gereken ELLE
// MÜDAHALE sinyalidir. nil dönmek, yürütmeyi "iş yapıldı ve GERİ ALINDI" diye
// kaydeden bir yalan olurdu.
//
// Hata errors.Conflict'tir ve bu sınıf yeniden DENENMEZ (bkz.
// workflow.DefaultRetryable): kalıcı bir durumu üç kez denemek yalnızca
// gecikme üretirdi.
//
// Tahsilat hiç DENENMEDİYSE çağrı no-op'tur: geri alınacak bir şey yoktur ve
// blokajı yetkilendirme adımının telafisi bırakır. Denendiği hâlde kimlik
// yoksa (sonucu bilinmeyen tahsilat, bkz. [capturePaymentStep.settle]) telafi
// yine "geri alınamadı" der — sonucu bilinmeyen bir para hareketini "geri
// alındı" saymak, bilinen bir tahsilatı öyle saymaktan daha az yalan değildir.
func (s *capturePaymentStep) Compensate(ctx context.Context, sc *workflow.StepContext) error {
	paymentID, err := sharedText(sc, sharedPaymentID)
	if err != nil {
		return err
	}
	attempted, err := sharedFlag(sc, sharedCaptureAttempted)
	if err != nil {
		return err
	}
	if paymentID == "" && !attempted {
		return nil
	}

	if paymentID == "" {
		s.w.log.ErrorContext(ctx, "telafi: sonucu bilinmeyen tahsilat geri alınamaz; mutabakat gerekir",
			"cart_id", s.plan.CartID, "amount", s.plan.Amount)

		return errors.Conflict(CodeCaptureAmbiguous,
			"%s sepetinde tahsilatın sonucu bilinmiyor; bu akışta geri alınamaz ve ELLE mutabakat gerekir",
			s.plan.CartID)
	}

	s.w.log.ErrorContext(ctx, "telafi: tahsil edilmiş tutar geri alınamaz; iade akışı gerekir",
		"cart_id", s.plan.CartID, "payment_id", paymentID, "amount", s.plan.Amount)

	return errors.Conflict(CodeCaptureIrreversible,
		"%s tahsilatı (%d %s) bu akışta geri alınamaz; iade AYRI bir akıştır ve ELLE başlatılmalıdır",
		paymentID, s.plan.Amount, s.plan.CurrencyCode)
}

// clearCartStep sepeti kapatır ve rezervasyonları kesinleştirir.
type clearCartStep struct {
	w    *Workflows
	plan *checkoutPlan
}

// Name adımın adını döner.
func (s *clearCartStep) Name() string { return StepClearCart }

// Invoke sepeti tamamlanmış damgalar, rezervasyonları onaylar ve akışın
// sonucunu üretir.
//
// # Modül arızaları hata olarak DÖNMEZ
//
// Adım pivot'un (tahsilat) ARDINDAN çalışır. Hata dönmesi yürütmeyi
// başarısız yazar ve müşteriye, parası çekilmiş ve siparişi açılmış bir akış
// için hata gösterirdi; üstelik telafi zinciri de boşuna çalışırdı (pivot
// koruması onu zaten atlatır, bkz. [Workflows.skipAfterCapture]). Bunun yerine
// arızalar ERROR olarak loglanır ve [CompleteCartResult.Warnings] alanına
// yazılır; sipariş GEÇERLİDİR, ama bir insan bakmalıdır.
//
// Tek hata yolu, adımlar arası verinin bozulmasıdır: o bir programlama
// hatasıdır, dış bir arıza değildir ve sessizce yutulması sonucun eksik
// alanlarla dönmesi demek olurdu.
//
// Kalan tutarsızlık sınırlıdır ve onarılabilir: damgalanamamış bir sepet
// açık görünür (ama aynı sepet için ikinci bir yürütme idempotency anahtarı
// yüzünden başlatılamaz), onaylanmamış bir rezervasyon ise "active" kalır —
// stok yine ayrılmıştır, yalnızca düşülmüş sayılmaz. Hiçbiri iade edilmemiş
// bir tahsilat kadar pahalı değildir.
//
// Adımın çıktısı workflow'un çıktısıdır: aynı anahtarla yapılan ikinci çağrı
// bu gövdeyi yürütme kaydından okuyup döner.
func (s *clearCartStep) Invoke(ctx context.Context, sc *workflow.StepContext) (any, error) {
	result := CompleteCartResult{
		CartID:       s.plan.CartID,
		CurrencyCode: s.plan.CurrencyCode,
		Amount:       s.plan.Amount,
	}

	var err error
	if result.OrderID, err = sharedText(sc, sharedOrderID); err != nil {
		return nil, err
	}
	if result.PaymentCollectionID, err = sharedText(sc, sharedCollectionID); err != nil {
		return nil, err
	}
	if result.PaymentSessionID, err = sharedText(sc, sharedSessionID); err != nil {
		return nil, err
	}
	if result.PaymentID, err = sharedText(sc, sharedPaymentID); err != nil {
		return nil, err
	}
	refs, err := sharedRefs(sc)
	if err != nil {
		return nil, err
	}

	if markErr := s.w.carts.MarkCompleted(ctx, s.plan.CartID); markErr != nil {
		s.w.log.ErrorContext(ctx, "sepet tamamlanmış damgalanamadı; sipariş GEÇERLİ, elle onarım gerekir",
			"cart_id", s.plan.CartID, "order_id", result.OrderID, "error", markErr)
		result.Warnings = append(result.Warnings, "sepet tamamlanmış damgalanamadı: "+markErr.Error())
	} else {
		result.CartCompleted = true
	}

	result.ReservationIDs = make([]string, 0, len(refs))
	confirmed := true
	for i := range refs {
		result.ReservationIDs = append(result.ReservationIDs, refs[i].ReservationID)

		if confirmErr := s.w.inventory.ConfirmReservation(ctx, refs[i].ReservationID); confirmErr != nil {
			confirmed = false
			s.w.log.ErrorContext(ctx, "rezervasyon kesinleştirilemedi; sipariş GEÇERLİ, elle onarım gerekir",
				"cart_id", s.plan.CartID, "order_id", result.OrderID,
				"reservation_id", refs[i].ReservationID, "error", confirmErr)
			result.Warnings = append(result.Warnings,
				"rezervasyon kesinleştirilemedi ("+refs[i].ReservationID+"): "+confirmErr.Error())
		}
	}
	result.ReservationsConfirmed = confirmed

	return result, nil
}

// Compensate hiçbir şey yapmaz.
//
// İki sebebi vardır ve ikisi de yeterlidir: adım saga'nın SONUNCUSUDUR, yani
// ondan sonra patlayacak bir adım yoktur; ve yaptığı işin geri dönüşü de
// yoktur — ConfirmReservation ayrılan stoğu fiilen düşer ve stok "yaratmadan"
// geri alınamaz (bkz. inventory modülünde ReleaseReservation, onaylanmış
// rezervasyon errors.Conflict döner).
//
// Motorun bu telafiyi çağırabileceği TEK durum, adımın kendisinin birden çok
// kez denenmesidir (en iyi çaba telafi); adımlar yeniden denenmediği için o
// yol da kapalıdır. nil dönmek bu yüzden doğrudur, sessiz bir kayıp değildir.
func (s *clearCartStep) Compensate(_ context.Context, _ *workflow.StepContext) error {
	return nil
}
