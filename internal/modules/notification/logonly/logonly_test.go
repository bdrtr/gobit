package logonly_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
)

// testBildirimi gerçek bir sipariş onayının şeklini taşır.
func testBildirimi() coreprovider.Notification {
	return coreprovider.Notification{
		Channel:  coreprovider.ChannelEmail,
		To:       "musteri@example.com",
		Template: "order.placed",
		Data: map[string]string{
			"order_id":   "order_01H",
			"display_id": "1042",
			"total":      "6100",
		},
	}
}

// yakalayan çıktısını tampona yazan bir logger üretir.
func yakalayan() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	return slog.New(handler), buf
}

// TestSendAliciAdresiniLOGLAMAZ planın "hassas veri loglanmaz" kuralını
// (Bölüm 8) doğrular.
//
// Log toplayıcısı yönetim yüzeyinden çok daha geniş bir kitleye açıktır; bir
// adresin oraya düşmesi, teslim günlüğünde bilinçli olarak tutulmayan veriyi
// arka kapıdan kalıcı hâle getirirdi. Bu yüzden yalnızca "To" değil, yükün
// DEĞERLERİ de yazılmaz — şablon verisi tanımı gereği serbesttir ve yarın bir
// müşteri adı taşıyabilir.
func TestSendAliciAdresiniLOGLAMAZ(t *testing.T) {
	log, buf := yakalayan()
	prov := logonly.New(log)

	require.NoError(t, prov.Send(context.Background(), testBildirimi()))

	cikti := buf.String()
	require.NotEmpty(t, cikti, "sağlayıcı en azından bir satır yazmalı")
	assert.NotContains(t, cikti, "musteri@example.com", "alıcı adresi LOGLANMAMALI")
	assert.NotContains(t, cikti, "@", "hiçbir adres parçası loga düşmemeli")
	assert.NotContains(t, cikti, "6100", "yük DEĞERLERİ loglanmamalı")

	assert.Contains(t, cikti, "order.placed", "şablon loglanmalı")
	assert.Contains(t, cikti, coreprovider.ChannelEmail, "kanal loglanmalı")
	assert.Contains(t, cikti, "order_id", "veri ANAHTARLARI teşhis için loglanır")
}

// TestSendGondermediginiSOYLER log satırının gönderim yapılmadığını açıkça
// bildirdiğini doğrular.
//
// "Bildirim gitti" sanmak, sipariş onayının müşteriye ulaştığını sanmaktır;
// sessiz bir yer tutucu tam da bu yanılgıyı üretirdi. Seviye de WARN'dır:
// gerçek bir sağlayıcı seçilmemiş bir kurulum, sessiz bir kurulum olmamalıdır.
func TestSendGondermediginiSOYLER(t *testing.T) {
	log, buf := yakalayan()

	require.NoError(t, logonly.New(log).Send(context.Background(), testBildirimi()))

	cikti := buf.String()
	assert.Contains(t, cikti, "GÖNDERİLMEDİ", "log satırı gönderim yapılmadığını söylemeli")
	assert.Contains(t, cikti, `"level":"WARN"`, "seviye WARN olmalı")
}

// TestIDGondermediginiSoyleyenBirAddir sağlayıcının KİMLİĞİNİN de davranışı
// anlattığını doğrular.
//
// Kimlik yapılandırmaya yazılan değerdir (NOTIFICATION_PROVIDER=log); "smtp"
// ya da "default" gibi bir ad, kurulumun sahibine gönderim yapıldığını
// düşündürürdü.
func TestIDGondermediginiSoyleyenBirAddir(t *testing.T) {
	assert.Equal(t, "log", logonly.ID)
	assert.Equal(t, logonly.ID, logonly.New(nil).ID())
}

// TestSendHataDONMEZ göndermemenin bir ARIZA olarak raporlanmadığını doğrular.
//
// Hata dönseydi teslim günlüğü, gerçek bir sağlayıcı arızası varmış gibi
// kırmızıya boyanır ve yapılandırma eksiği ile gerçek arıza ayırt edilemezdi.
func TestSendHataDONMEZ(t *testing.T) {
	assert.NoError(t, logonly.New(nil).Send(context.Background(), testBildirimi()))
}
