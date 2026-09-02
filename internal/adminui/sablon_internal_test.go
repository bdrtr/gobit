package adminui

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/errors"
)

// TestSablonlarAcilistaAyristirilir gömülü şablonların yüklenebildiğini ve
// beklenen adların hepsinin kümede olduğunu kanıtlar.
func TestSablonlarAcilistaAyristirilir(t *testing.T) {
	t.Parallel()

	set, err := sablonlariYukle()
	require.NoError(t, err, "gömülü şablonlar ayrıştırılabilmeli")
	require.NotNil(t, set)

	for _, ad := range beklenenSablonlar {
		assert.NotNil(t, set.sablonlar.Lookup(ad), "%s ayrıştırılmış kümede olmalı", ad)
	}
}

// TestBeklenenSablonEksikseAcilisDuser var olmayan bir şablon adının AÇILIŞTA
// yakalandığını kanıtlar.
//
// Şablon adı bir DİZEDİR: yazım hatası derlenir, lint görmez ve yalnızca o
// sayfa açıldığında — kullanıcının karşısında — patlar. Denetim hatayı açılışa
// çeker, yani arıza dağıtımda görünür.
func TestBeklenenSablonEksikseAcilisDuser(t *testing.T) {
	onceki := beklenenSablonlar
	t.Cleanup(func() { beklenenSablonlar = onceki })

	beklenenSablonlar = append(slices.Clone(onceki), "hicbir-zaman-yazilmadi.gohtml")

	_, err := sablonlariYukle()
	require.Error(t, err, "beklenen ama ayrıştırılmamış bir şablon adı hata vermeli")
	assert.Equal(t, CodeSablonBozuk, errors.CodeOf(err))
	assert.Contains(t, err.Error(), "hicbir-zaman-yazilmadi.gohtml",
		"hata hangi adın çözülemediğini SÖYLEMELİ; söylemezse teşhis kaynağa inmeyi gerektirir")
}

// TestAyristirilanFazlaSablonAcilisDuser listenin TERS yönde bayatlamasını
// yakalar: ayrıştırılan ama hiçbir yerde çağrılmayan bir şablon ölü dosyadır.
//
// Tek yönlü bir denetim (yalnızca "beklenen var mı") dosyayı silmeyi unutan
// turu görmezdi ve ölü şablon ikiliye gömülmeye devam ederdi.
func TestAyristirilanFazlaSablonAcilisDuser(t *testing.T) {
	onceki := beklenenSablonlar
	t.Cleanup(func() { beklenenSablonlar = onceki })

	beklenenSablonlar = slices.Clone(onceki[:len(onceki)-1])

	_, err := sablonlariYukle()
	require.Error(t, err, "listede olmayan bir şablon ayrıştırıldığında hata vermeli")
	assert.Equal(t, CodeSablonBozuk, errors.CodeOf(err))
	assert.Contains(t, err.Error(), onceki[len(onceki)-1],
		"hata hangi şablonun listede olmadığını söylemeli")
}

// TestSayfaKullaniciVerisiniKACIRIR şablon motorunun otomatik kaçışının
// gerçekten çalıştığını kanıtlar.
//
// Panel bir YÖNETİCİ oturumunda çalışır; oradaki bir XSS, saldırgana yönetim
// yetkisi verir. İddia iki yönlüdür ve ikincisi kolayca atlanır: çıktıda ham
// etiket BULUNMAMALI, ama aynı zamanda "ZgotmplZ" da bulunmamalı. Şablon motoru
// çözemediği bir bağlamda hata VERMEZ, sessizce o damgayı basar — yani kaçışın
// çalıştığı sanılırken veri kaybolur ve hiçbir test düşmez.
func TestSayfaKullaniciVerisiniKACIRIR(t *testing.T) {
	t.Parallel()

	set, err := sablonlariYukle()
	require.NoError(t, err)

	const kotucul = `<script>alert('yonetici')</script>`

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	set.yaz(rec, req, http.StatusOK, "duzen.gohtml", map[string]any{
		"Baslik": kotucul,
		"Icerik": kotucul,
	})

	require.Equal(t, http.StatusOK, rec.Code)
	govde := rec.Body.String()

	assert.NotContains(t, govde, "<script>",
		"kullanıcı verisi ham etiket olarak basılMAMALI")
	assert.Contains(t, govde, "&lt;script&gt;",
		"veri kaçırılarak basılmalı; kaybolmamalı")
	assert.NotContains(t, govde, "ZgotmplZ",
		"motor bağlamı çözememiş: kaçış çalışmış GİBİ görünür ama veri silinir")
}

// TestSayfaBozukSablonaHataDoner şablon üretimi patladığında YARIM sayfa
// yazılmadığını kanıtlar.
//
// Şablon doğrudan yazıcıya akıtılsaydı, ortada doğan bir hata 200 durum kodlu
// yarım bir gövde bırakırdı: başlık gönderildikten sonra ne panik yakalayıcı ne
// de hata yazıcısı bir şey yapabilir ve arıza istemcide sessizleşir. Tampon,
// hatayı hâlâ 500'e çevrilebilir bir yerde tutar.
func TestSayfaBozukSablonaHataDoner(t *testing.T) {
	t.Parallel()

	set, err := sablonlariYukle()
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, URLPrefix, http.NoBody)
	// Var olmayan bir şablon adı: ExecuteTemplate hata döner.
	set.yaz(rec, req, http.StatusOK, "boyle-bir-sablon-yok.gohtml", nil)

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"üretim patladığında durum kodu 200 KALMAMALI")
	assert.NotContains(t, rec.Body.String(), "<html",
		"yarım bir sayfa yazılmamalı")
	assert.True(t, strings.Contains(rec.Body.String(), `"error"`),
		"gövde çekirdeğin hata zarfı olmalı: %s", rec.Body.String())
}
