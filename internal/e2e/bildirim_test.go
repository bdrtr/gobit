//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	notificationmod "github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	notificationsvc "github.com/bdrtr/gobit/internal/modules/notification/service"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// Bu dosya SİPARİŞ -> OLAY -> BİLDİRİM zincirini uçtan uca kanıtlar: sepet
// siparişe çevrilir, order modülü "order.placed" yayımlar, notification modülü
// olayı alır, siparişin iletişim bilgisini GERÇEK order modülünden okur,
// sağlayıcıya gider ve denemeyi teslim günlüğüne yazar.
//
// Kanıtlanan dört iddia:
//
//  1. Tamamlanan bir sipariş günlüğe TEK kayıt düşürür ve kaydın referansı o
//     siparişin kimliğidir.
//  2. Sağlayıcıya giden bildirimin ALICISI siparişin e-postasıdır — adres
//     olayda YOKTUR, yani abone onu kayıttan okumuştur.
//  3. Aynı sipariş için ikinci bir olay ikinci bir bildirim ÜRETMEZ.
//  4. Günlük yönetim ucundan okunur ve kimliksiz istek 401 alır.
//
// # Neden bu test modülün kendi entegrasyon testine EK
//
// Bildirim modülü order'ı import EDEMEZ (Prensip 2.4); kendi testinde sipariş
// yüzeyi taklittir ve taklit, elle yazılmış bir JSON şemasıdır. İki tarafın
// şeması ayrışırsa (order bir alanı yeniden adlandırırsa) o test yeşil kalır ve
// üretimde her sipariş bildirimi düşerdi. Burada iki taraf da GERÇEKTİR;
// ayrışma yalnızca burada görülebilir.
//
// # Sağlayıcı neden kutudan çıkan "log" DEĞİL
//
// İkinci iddia — "alıcı siparişin e-postasıdır" — adresi GÖREN bir yer
// gerektirir. Adres ise hiçbir yerde saklanmaz ve bu bilinçlidir: teslim
// günlüğünde sütunu yoktur, "log" sağlayıcısı da onu loglamaz. Geriye tek bir
// yer kalır — sağlayıcının kendisi. Bu yüzden zemin gönderimi bir CASUSA
// ([bildirimSaglayiciCasusu]) yaptırır.
//
// Casus bir SAHTE MODÜL DEĞİLDİR: zincirin tamamı (abonelik, sipariş okuması,
// kayıt açma, sağlayıcı çözümü, sonuç yazımı) üretim kodudur ve casus tam
// olarak bir eklenti sağlayıcısının durduğu yerde durur. Kutudan çıkan "log"
// sağlayıcısı da kayıtta KALIR; ikisi de aşağıdaki
// TestBildirimSaglayiciKaydiVarsayilaniKorur ile doğrulanır.
//
// Bir e-posta hiçbir yere GİTMEZ; casus yalnızca kendisine verileni tutar.

// teslimGunluguYolu teslim günlüğü listesinin yönetim ucudur.
//
// Yol ELLE yazılır: notification/api paketinde unexported'tır ve dışa açmanın
// tek sebebi bu test olurdu. Asıl kanıtlanan da yolun kendisidir — istemcinin
// bildiği adres budur.
const teslimGunluguYolu = "/admin/v1/notifications"

// Bildirim senaryosunun fikstür sabitleri.
const (
	bildirimBirimFiyat  int64 = 30_000
	bildirimAdet        int64 = 1
	bildirimStok        int64 = 5
	bildirimBeklemeSure       = 5 * time.Second
	bildirimAralik            = 25 * time.Millisecond
)

// bildirimCasusuID casus sağlayıcının kimliğidir (NOTIFICATION_PROVIDER
// karşılığı).
//
// Ad kutudan çıkan hiçbir sağlayıcıyla çakışmaz ve "e2e" önekiyle nereden
// geldiğini söyler: teslim günlüğünde provider_id olarak görünür ve bir
// üretim kurulumunda görülmesi kurulumun yanlış olduğunun kanıtı olur.
const bildirimCasusuID = "e2e-casus"

// bildirimVeriAnahtariSiparisID şablon verisindeki sipariş kimliği
// anahtarıdır.
//
// Ad elle tekrarlanır çünkü notification modülünün şablon veri anahtarları
// unexported'tır ve öyle kalmalıdır: onları okuyan taraf sağlayıcıdır ve
// sağlayıcı, adları dizeyle okuyan YABANCI koddur (çoğu zaman bir eklenti).
// Casus da tam olarak öyle davranır; sabiti dışa açmak, testin sağlayıcıdan
// daha ayrıcalıklı bir konumdan bakması demek olurdu.
const bildirimVeriAnahtariSiparisID = "order_id"

// bildirimCasusu zemindeki tek bildirim sağlayıcısıdır.
//
// Örnek SÜREÇ ÖMÜRLÜDÜR ve tüm testler onu paylaşır: sağlayıcı kaydı
// abonelikler gibi geri alınamaz, dolayısıyla test başına bir casus kaydetmek
// ikinci testte errors.Conflict verirdi. Testler kendi bildirimlerini SİPARİŞ
// KİMLİĞİYLE süzer.
var bildirimCasusu = &bildirimSaglayiciCasusu{}

// bildirimSaglayiciCasusu kendisine verilen bildirimleri tutan, hiçbir yere
// göndermeyen bir bildirim sağlayıcısıdır.
//
// Eşzamanlı kullanıma güvenlidir: Send bir olay işleyicisinden çağrılır ve
// [eventbus.EventBus]'ın bellek içi backend'i her işleyiciyi kendi
// goroutine'inde çalıştırır.
type bildirimSaglayiciCasusu struct {
	mu           sync.Mutex
	yakalananlar []coreprovider.Notification
}

// Casusun çekirdek sözleşmesini karşıladığı derleme zamanında sabitlenir;
// karşılamasaydı zemin kurulurken değil, burada kırılırdı.
var _ coreprovider.NotificationProvider = (*bildirimSaglayiciCasusu)(nil)

// ID casusun kimliğini döner.
func (c *bildirimSaglayiciCasusu) ID() string { return bildirimCasusuID }

// Send bildirimi kaydeder ve BAŞARILI döner.
//
// Başarı dönmek gerçek bir seçimdir: sözleşmeye göre hata dönmek "gitmedi"
// demek değildir ve kayıt "failed" olurdu — oysa bu testlerin sınadığı yol
// mutlu yoldur. Sağlayıcı hatasının günlüğe nasıl yazıldığı notification
// modülünün kendi entegrasyon testindedir.
//
// Data KOPYALANIR: sözleşme, çağrının haritayı çağrı sonrası değiştirmeyeceğini
// garanti etmez ve saklanan bir referans, testin sonradan değişmiş bir yükü
// okumasına yol açabilirdi.
func (c *bildirimSaglayiciCasusu) Send(_ context.Context, n coreprovider.Notification) error {
	kopya := n
	kopya.Data = maps.Clone(n.Data)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.yakalananlar = append(c.yakalananlar, kopya)
	return nil
}

// bildirimler verilen siparişe ait yakalanmış bildirimleri döner.
//
// Süzme şablon verisindeki sipariş kimliği üzerindendir; ALICI ADRESİ
// üzerinden süzmek, tam da kanıtlanmak istenen şeyi varsaymak olurdu.
func (c *bildirimSaglayiciCasusu) bildirimler(siparisID string) []coreprovider.Notification {
	c.mu.Lock()
	defer c.mu.Unlock()

	var bulunan []coreprovider.Notification
	for i := range c.yakalananlar {
		if c.yakalananlar[i].Data[bildirimVeriAnahtariSiparisID] == siparisID {
			bulunan = append(bulunan, c.yakalananlar[i])
		}
	}
	return bulunan
}

// bildirimCasusunuKur casusu notification modülünün sağlayıcı kaydına ekler.
//
// Kayıt container'dan ADLA çözülür ve modüller ayağa kalktıktan SONRA yapılır;
// eklenti sisteminin izlediği yol da budur (coreplugin.Host'un
// RegisterNotificationProvider'ı aynı adı çözüp aynı Register'ı çağırır).
// Zemine test için bir EKLENTİ yazmak yerine kaydı doğrudan çözmek sınanan
// şeyi değiştirmez ama üretimde var olmayan bir eklentiyi kurulumun içine
// sokmaz.
//
// Çağrı Bootstrap'tan önce yapılamaz: "notification.providers" container'a
// modülün Register'ında konur.
func bildirimCasusunuKur() error {
	kayit, err := container.Resolve[*notificationsvc.ProviderRegistry](kap, notificationmod.ProvidersName)
	if err != nil {
		return err
	}
	return kayit.Register(bildirimCasusu)
}

// bildirimKaydi teslim günlüğü ucunun yanıt gövdesidir.
//
// Şema, notification modülünün DTO'sundan bağımsız olarak burada TEKRAR
// tanımlanır: sınanan şey istemcinin gördüğü JSON'dur ve modülün tipini
// kullanmak, alan adı değişse bile testin yeşil kalması demekti.
type bildirimKaydi struct {
	ID         string `json:"id"`
	Template   string `json:"template"`
	Channel    string `json:"channel"`
	Reference  string `json:"reference"`
	ProviderID string `json:"provider_id"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}

// bildirimleriIste yönetim ucundan bir siparişin teslim kayıtlarını okur ve
// sorunu HATA olarak döner.
//
// Ayrım zorunludur: require.Eventually ve require.Never koşulu AYRI BİR
// GOROUTINE'de çalıştırır, oysa t.FailNow yalnızca testin kendi
// goroutine'inden çağrılabilir. Bekleme içinde require kullanan bir yardımcı,
// uç bozulduğunda testi düşürmek yerine ASKIDA bırakırdı — yani asıl arızayı
// zaman aşımının arkasına saklardı.
func bildirimleriIste(t *testing.T, jeton, referans string) ([]bildirimKaydi, error) {
	t.Helper()

	kayit := yonetimIstegi(t, http.MethodGet,
		teslimGunluguYolu+"?reference="+referans, "Bearer "+jeton)
	if kayit.Code != http.StatusOK {
		return nil, fmt.Errorf("teslim günlüğü %d döndü; gövde: %s", kayit.Code, kayit.Body.String())
	}

	var zarf struct {
		Data  []bildirimKaydi `json:"data"`
		Count int64           `json:"count"`
	}
	if err := json.Unmarshal(kayit.Body.Bytes(), &zarf); err != nil {
		return nil, fmt.Errorf("yanıt çözülemedi (%w); gövde: %s", err, kayit.Body.String())
	}
	if zarf.Count != int64(len(zarf.Data)) {
		return nil, fmt.Errorf(
			"zarftaki sayı (%d) ile dönen kayıt sayısı (%d) bu süzgeçte örtüşmeli",
			zarf.Count, len(zarf.Data))
	}

	return zarf.Data, nil
}

// bildirimleriOku teslim kayıtlarını okur; okuyamazsa testi düşürür.
func bildirimleriOku(t *testing.T, jeton, referans string) []bildirimKaydi {
	t.Helper()

	kayitlar, err := bildirimleriIste(t, jeton, referans)
	require.NoError(t, err, "teslim günlüğü okunabilmeli")
	return kayitlar
}

// bildirimBekle siparişin teslim kaydı NİHAİ duruma gelene kadar bekler.
//
// Bekleme ZORUNLUDUR: abone [eventbus.EventBus] üzerinden tetiklenir, Publish
// handler'ları BEKLEMEZ ve bellek içi backend her handler'ı kendi
// goroutine'inde çalıştırır — sipariş yazılmış olsa bile bildirim kaydı henüz
// yazılmamış olabilir.
//
// Nihai duruma ("pending" olmayan) kadar beklemenin ikinci bir faydası vardır:
// sonuç ancak sağlayıcı DÖNDÜKTEN sonra yazılır, dolayısıyla bu çağrı geri
// döndüğünde casusun defterine bakmak yarışa açık değildir.
//
// # Neden require.Eventually DEĞİL
//
// Paketin geri kalanı require.Eventually kullanır; burada yetmez ve sebebi
// tektir: koşul AYRI BİR GOROUTINE'de çalışır. Bunun iki sonucu var —
// (1) koşulun içinde require kullanılamaz, çünkü t.FailNow yalnızca testin
// kendi goroutine'inden çağrılabilir; (2) Eventually'nin mesaj argümanları
// ÇAĞRI ANINDA, yani koşul hiç koşmadan önce değerlendirilir, dolayısıyla
// koşulun gördüğü son hatayı mesaja taşımanın yolu yoktur. EventuallyWithT
// ikisini de çözer: koşulun içindeki assert'ler biriktirilir ve zaman
// aşımında SON turun hataları basılır. Fark teşhiste ortaya çıkar — bozuk bir
// uç "bildirim yazılmadı" değil, döndürdüğü status ile raporlanır.
func bildirimBekle(t *testing.T, jeton, referans string) bildirimKaydi {
	t.Helper()

	var bulunan bildirimKaydi
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		kayitlar, err := bildirimleriIste(t, jeton, referans)
		if !assert.NoError(c, err, "teslim günlüğü okunabilmeli") {
			return
		}
		if !assert.NotEmpty(c, kayitlar, "%s siparişi için teslim kaydı açılmalı", referans) {
			return
		}

		// "pending" ARA durumdur: kayıt açıldı ama sonuç henüz yazılmadı.
		// Onu nihai sanmak, testi yarışa açık hâle getirirdi.
		if !assert.NotEqual(c, "pending", kayitlar[0].Status,
			"kaydın sonucu yazılmalı; 'pending' kalan bir satır gönderimin sonucunun "+
				"yazılamadığını bildirir") {
			return
		}

		bulunan = kayitlar[0]
	}, bildirimBeklemeSure, bildirimAralik,
		"sipariş bildirimi teslim günlüğüne yazılmalı (referans %s)", referans)

	return bulunan
}

// bildirimSiparisi bildirim senaryolarının paylaştığı sipariş fikstürüdür:
// kayıtlı bir müşteriye tek satırlık bir sepet açar ve onu siparişe çevirir.
//
// Her senaryo KENDİ siparişini kurar. Zorunludur: olaylar bellek içi veri
// yolundan geçer ve testler sırayla koşar, dolayısıyla paylaşılan tek bir
// sipariş ilk testte bildirilir ve ikinci test "ikinci bildirim üretilmedi"
// iddiasını kendi kurduğu durumda değil, önceki testin artığında sınardı.
func bildirimSiparisi(
	ctx context.Context,
	t *testing.T,
	baslik string,
) (siparisID, eposta string, toplam int64) {
	t.Helper()

	musteriID, adres := yeniMusteri(ctx, t)
	varyantID, _ := yeniStokluVaryant(ctx, t, baslik, map[string]int64{
		vergiliParaBirimi: bildirimBirimFiyat,
	}, bildirimStok)

	sepetID, toplamlar := sepetHazirla(ctx, t, musteriID, varyantID, bildirimAdet)

	sonuc, err := siparisAkislari.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            sepetID,
		LocationID:        stokLokasyonID,
		PaymentProviderID: manual.ID,
		PaymentData:       odemeDavranisi(t, manual.OutcomeAuthorize),
		Email:             adres,
		ExpectedTotal:     toplamlar.Total,
	})
	require.NoError(t, err, "sepet siparişe çevrilebilmeli")

	return sonuc.OrderID, adres, toplamlar.Total
}

// TestSiparisOnayiBildirimGunluguneYazilir tamamlanan bir siparişin teslim
// günlüğüne TEK kayıt düşürdüğünü ve kaydın o siparişe referansladığını
// doğrular.
//
// Kaydın TEKLİĞİ ayrıca sınanır: ikinci bir kayıt, müşterinin aynı sipariş
// için iki onay alması demektir ve zincirdeki her halka (yayım, abonelik,
// idempotency) bunu tek başına bozabilir.
func TestSiparisOnayiBildirimGunluguneYazilir(t *testing.T) {
	ctx := t.Context()
	jeton := jetonAl(t, yoneticiEposta, yoneticiParola)

	siparisID, eposta, _ := bildirimSiparisi(ctx, t, "E2E Bildirim Ürünü")

	kayit := bildirimBekle(t, jeton, siparisID)

	kayitlar := bildirimleriOku(t, jeton, siparisID)
	require.Len(t, kayitlar, 1,
		"sipariş için TEK teslim kaydı açılmalı; ikincisi müşteriye ikinci bir onay demektir")

	assert.Equal(t, notificationsvc.TemplateOrderPlaced, kayit.Template,
		"şablon, tetikleyen olayın adıyla aynı olmalı")
	assert.Equal(t, siparisID, kayit.Reference,
		"kayıt siparişe REFERANSLA bağlanır; foreign key yoktur (Prensip 2.2)")
	assert.Equal(t, bildirimCasusuID, kayit.ProviderID,
		"gönderimi zeminde seçili sağlayıcı üstlenmeli")
	assert.Equal(t, "sent", kayit.Status,
		"sağlayıcı isteği kabul etti; 'sent' teslimi değil KABULÜ bildirir")
	assert.Empty(t, kayit.Error)

	// Kanal ve durum WIRE değerleridir; dize olarak yazılmaları bilinçlidir.
	// Sabitle karşılaştırmak, JSON'un dışa açık sözleşmesini modülün iç
	// adlandırmasına bağlar ve sabit yeniden adlandırıldığında test yeşil
	// kalarak istemcileri kıran bir değişikliği geçirirdi.
	assert.Equal(t, "email", kayit.Channel)

	// Adres HİÇBİR YERDE saklanmaz: yanıt gövdesi kişisel veri taşımaz
	// (plan Bölüm 8).
	ham, err := json.Marshal(kayit)
	require.NoError(t, err)
	assert.NotContains(t, string(ham), eposta,
		"teslim kaydı alıcı adresini TAŞIMAMALI; adres yalnızca siparişte durur")
}

// TestBildirimAlicisiOlaydanDegilSiparistenOkunur sağlayıcıya giden
// bildirimin alıcısının siparişin e-postası olduğunu doğrular.
//
// # Asıl iddia neden olay yükünde
//
// "Alıcı doğru" demek tek başına zayıftır: abone adresi olay yükünden de
// okumuş olabilirdi ve test ikisini ayırt edemezdi. Bu yüzden aynı testte
// olayın KENDİSİ de denetlenir — yükte "email" alanı yoktur ve hiçbir değer
// adresi taşımaz. İkisi birlikte tek bir sonuç verir: abone adresi ancak
// "order.interop" üzerinden SİPARİŞ KAYDINDAN okumuş olabilir.
//
// Denetim aynı zamanda order modülünün kararının bekçisidir: e-posta olay
// yüküne bilinçli olarak KONMAZ, çünkü olaylar üretimde Redis'e yazılır ve
// orada kalıcıdır. Birisi "kolaylık olsun" diye alanı eklerse burada kırılır.
func TestBildirimAlicisiOlaydanDegilSiparistenOkunur(t *testing.T) {
	ctx := t.Context()
	jeton := jetonAl(t, yoneticiEposta, yoneticiParola)

	siparisID, eposta, toplam := bildirimSiparisi(ctx, t, "E2E Bildirim Alıcı Ürünü")

	// Kaydın nihai duruma gelmesini beklemek casusun defterini de yarışsız
	// hâle getirir: sonuç ancak sağlayıcı döndükten sonra yazılır.
	require.Equal(t, "sent", bildirimBekle(t, jeton, siparisID).Status)

	gonderilenler := bildirimCasusu.bildirimler(siparisID)
	require.Len(t, gonderilenler, 1,
		"sağlayıcıya sipariş başına TEK bildirim gitmeli")
	gonderilen := gonderilenler[0]

	assert.Equal(t, eposta, gonderilen.To,
		"bildirimin alıcısı siparişin e-postası olmalı; başka bir adres, abonenin "+
			"iletişim bilgisini yanlış siparişten okuduğu anlamına gelir")
	assert.Equal(t, coreprovider.ChannelEmail, gonderilen.Channel)
	assert.Equal(t, notificationsvc.TemplateOrderPlaced, gonderilen.Template)

	// Şablon verisi de KAYITTAN gelir; tutar ve satır sayısı siparişin
	// kendisiyle örtüşmeli. Örtüşmemesi, "order.interop" yanıtının şema
	// olarak çözülse bile YANLIŞ siparişi anlattığı anlamına gelirdi.
	assert.Equal(t, siparisID, gonderilen.Data[bildirimVeriAnahtariSiparisID])
	assert.Equal(t, strconv.FormatInt(toplam, 10), gonderilen.Data["total"],
		"şablondaki tutar siparişin toplamı olmalı ve ondalıksız DİZE taşımalı")
	assert.Equal(t, "1", gonderilen.Data["item_count"],
		"fikstür siparişi tek satırlıdır")

	// Ve olay adresi TAŞIMAZ — yani yukarıdaki adres oradan gelemezdi.
	olay := olayDefteri.bekle(t, siparisID)
	assert.Equal(t, siparisID, olayAlani(t, olay, ordersvc.EventFieldOrderID),
		"olayın aboneye verdiği bağ sipariş KİMLİĞİDİR")

	_, epostaAlaniVar := olay.Data["email"]
	assert.False(t, epostaAlaniVar,
		"olay yükünde 'email' alanı OLMAMALI; olaylar üretimde Redis'e yazılır ve "+
			"orada kalıcıdır (plan Bölüm 8: hassas veri taşınmaz)")

	hamOlay, err := json.Marshal(olay.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(hamOlay), eposta,
		"olay yükünün HİÇBİR alanı adresi taşımamalı; taşısaydı bu testin alıcı "+
			"iddiası, adresin kayıttan okunduğunu kanıtlamazdı")
}

// TestAyniSiparisIcinIkinciOlayIkinciBildirimUretmez elle yeniden yayımlanan
// bir olayın müşteriye ikinci bir e-posta göndermediğini doğrular.
//
// Senaryo uydurma değildir: veri yolu bugün yeniden teslim yapmasa da bir
// operatör kaçan olayları yeniden yayımlayabilir ve Redis backend'i EN AZ BİR
// KEZ teslim eder. Koruma teslim günlüğündeki (şablon, referans)
// benzersizliğidir ve burada GERÇEK indeks üzerinde sınanır.
//
// İki ayrı iddia birlikte kurulur: günlükte ikinci KAYIT açılmaz ve casusa
// ikinci GÖNDERİM gitmez. İkincisi asıl olandır — müşterinin gördüğü şey
// kayıt sayısı değil, gelen e-posta sayısıdır.
func TestAyniSiparisIcinIkinciOlayIkinciBildirimUretmez(t *testing.T) {
	ctx := t.Context()
	jeton := jetonAl(t, yoneticiEposta, yoneticiParola)

	siparisID, _, _ := bildirimSiparisi(ctx, t, "E2E Mükerrer Bildirim Ürünü")

	ilk := bildirimBekle(t, jeton, siparisID)

	// Olay ELLE yeniden yayımlanır; yükü siparişin olayıyla aynı şekildedir.
	veriYolu, err := container.Resolve[eventbus.EventBus](kap, svcEventBus)
	require.NoError(t, err, "olay veri yolu çözülebilmeli")

	require.NoError(t, veriYolu.Publish(ctx, eventbus.Event{
		Name: notificationsvc.EventOrderPlaced,
		Data: map[string]any{ordersvc.EventFieldOrderID: siparisID},
	}))

	// İkinci olayın işlenmesi için zaman tanınır. Beklemenin kanıtladığı şey
	// "hiçbir şey olmadı"dır; erken bakmak testi sahte yeşile çevirirdi.
	require.Never(t, func() bool {
		kayitlar, err := bildirimleriIste(t, jeton, siparisID)
		if err != nil {
			// Okuma hatası "ikinci kayıt açıldı" demek DEĞİLDİR; koşul
			// yanlış dönerse Never yeşil kalır ve arıza aşağıdaki okumada
			// testi düşürür.
			return false
		}
		return len(kayitlar) > 1 || len(bildirimCasusu.bildirimler(siparisID)) > 1
	}, time.Second, bildirimAralik,
		"aynı sipariş için İKİNCİ bir teslim kaydı ya da ikinci bir gönderim olmamalı")

	kayitlar := bildirimleriOku(t, jeton, siparisID)
	require.Len(t, kayitlar, 1)
	assert.Equal(t, ilk.ID, kayitlar[0].ID, "ilk kayıt korunmalı")
	assert.Len(t, bildirimCasusu.bildirimler(siparisID), 1,
		"sağlayıcıya TEK gönderim gitmeli; ikincisi müşterinin kutusunda ikinci bir "+
			"sipariş onayı demektir")
}

// TestTeslimGunluguKimliksizOkunamaz teslim günlüğünün korumalı olduğunu
// doğrular.
//
// İki isteğin İKİSİ de gereklidir. 401 tek başına ucun VAR olduğunu söylemez:
// koruma route eşleşmesinden önce çalışır, yani hiç tanımlanmamış bir yol da
// 401 döner (bkz. kimlik_test.go). Aynı adresin geçerli jetonla 200 dönmesi,
// reddedilen şeyin gerçekten bu uç olduğunu kanıtlar.
//
// Günlük kişisel veri taşımaz ama hangi siparişe ne zaman bildirim gittiğini
// gösterir; yani sipariş akışının zaman çizelgesidir ve kimliksiz okunmamalıdır.
func TestTeslimGunluguKimliksizOkunamaz(t *testing.T) {
	kimliksiz := yonetimIstegi(t, http.MethodGet, teslimGunluguYolu, "")
	require.Equal(t, http.StatusUnauthorized, kimliksiz.Code,
		"kimliksiz istek 401 dönmeli; gövde: %s", kimliksiz.Body.String())
	assert.Equal(t, "Bearer", kimliksiz.Header().Get("WWW-Authenticate"),
		"RFC 9110: 401 hangi şemanın beklendiğini bildirmeli")

	jeton := jetonAl(t, yoneticiEposta, yoneticiParola)
	kimlikli := yonetimIstegi(t, http.MethodGet, teslimGunluguYolu, "Bearer "+jeton)
	require.Equal(t, http.StatusOK, kimlikli.Code,
		"aynı adres geçerli jetonla çalışmalı; çalışmıyorsa 401 ucun varlığından değil "+
			"yokluğundan geliyor olurdu; gövde: %s", kimlikli.Body.String())
}

// TestBildirimSaglayiciKaydiVarsayilaniKorur kutudan çıkan sağlayıcının, zemin
// başkasını seçmiş olsa bile kayıtlı kaldığını doğrular.
//
// Test, zeminin sağlayıcıyı casusla değiştirmesinin bedelini karşılar: seçim
// değişti diye modülün varsayılanı kaybolmamalıdır. Kayıt bir LİSTEDİR, seçim
// değil — bir kurulum NOTIFICATION_PROVIDER'ı "log"a çevirdiğinde sağlayıcı
// orada duruyor olmalıdır.
func TestBildirimSaglayiciKaydiVarsayilaniKorur(t *testing.T) {
	kayit, err := container.Resolve[*notificationsvc.ProviderRegistry](kap, notificationmod.ProvidersName)
	require.NoError(t, err,
		"sağlayıcı kaydı container'da %q adıyla bulunmalı; eklentiler onu bu adla çözer",
		notificationmod.ProvidersName)

	assert.Contains(t, kayit.IDs(), logonly.ID,
		"kutudan çıkan %q sağlayıcısı kayıtta kalmalı", logonly.ID)
	assert.Contains(t, kayit.IDs(), bildirimCasusuID,
		"zemine eklenen sağlayıcı da kayıtta olmalı; olmasaydı hiçbir bildirim gönderilemezdi")
}
