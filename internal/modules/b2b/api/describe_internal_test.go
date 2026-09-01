package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/core/openapi"
)

// Test DAHİLİ pakettedir çünkü anlatılan gövdeler ([companyRequest],
// [employeeDTO] …) dışa kapalıdır. Dışarıdan sınamanın tek yolu tipleri dışa
// açmak olurdu; belgeyi sınamak uğruna modülün yüzeyini genişletmek, sınanan
// şeyin kendisini bozardı.

// belge Describe'ın çıktısını GERÇEK route ağacına karşı üretip JSON'dan geri
// okunmuş hâlini döner.
//
// Router gerçek olmalıdır: açıklamadaki yol ile route'un yolu ayrışırsa hata
// BURADA görünsün, üretimde /openapi.json'a bakan birinde değil.
func belge(t *testing.T) map[string]any {
	t.Helper()

	doc := openapi.New("test", "v1")
	Describe(doc)

	r := chi.NewRouter()
	New(nil).Routes(r)

	ham, err := doc.Build(r)
	require.NoError(t, err)
	require.Empty(t, doc.UnmatchedDescriptions(),
		"anlatılan her uç bir route ile eşleşmeli; eşleşmeyen kayıt belgeye hiç girmez")

	kodlanmis, err := json.Marshal(ham)
	require.NoError(t, err)

	var cozulmus map[string]any
	require.NoError(t, json.Unmarshal(kodlanmis, &cozulmus))

	yollar, ok := cozulmus["paths"].(map[string]any)
	require.True(t, ok)
	return yollar
}

// TestHerUcAnlatildi belgenin route ağacıyla ÖRTÜŞTÜĞÜNÜ doğrular.
//
// İki yön de gereklidir ve ikisi de ayrı bir sessizliği kapatır:
// anlatılıp route'u olmayan bir uç belgeye hiç girmez (Build onu eler),
// route'u olup anlatılmayan bir uç ise belgede YOLU görünür ama gövdesi
// görünmez — istemci üreteci o metodu argümansız üretir ve neden çalışmadığı
// ancak çalışma zamanında anlaşılır.
func TestHerUcAnlatildi(t *testing.T) {
	yollar := belge(t)

	r := chi.NewRouter()
	New(nil).Routes(r)

	err := chi.Walk(r, func(metot, desen string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		islemler, ok := yollar[desen].(map[string]any)
		require.True(t, ok, "%s belgede yok", desen)

		op, ok := islemler[strings.ToLower(metot)].(map[string]any)
		require.True(t, ok, "%s %s belgede yok", metot, desen)
		assert.NotEmpty(t, op["summary"], "%s %s için özet yazılmamış", metot, desen)
		return nil
	})
	require.NoError(t, err)
}

// TestVitrinYanitiPencereAlaniniTasir vitrin şemasının bir sonraki adıma
// bıraktığı alanları içerdiğini doğrular.
//
// Alanların adları YAYIMLANAN sözleşmedir: harcama limitini uygulayacak olan
// istemci tam olarak bunları okuyacaktır.
func TestVitrinYanitiPencereAlaniniTasir(t *testing.T) {
	yollar := belge(t)

	islemler, ok := yollar["/store/v1/b2b/customers/{customer_id}/employee"].(map[string]any)
	require.True(t, ok)

	op, ok := islemler["get"].(map[string]any)
	require.True(t, ok)
	assert.NotEmpty(t, op["responses"])

	// Şema bileşeni gövdeden türetilir; alan adlarının kaynağı DTO'nun json
	// etiketleridir ve burada onların gerçekten yazıldığı sabitlenir.
	ham, err := json.Marshal(storeEmployeeDTO{})
	require.NoError(t, err)

	var alanlar map[string]any
	require.NoError(t, json.Unmarshal(ham, &alanlar))
	for _, ad := range []string{
		"spending_limit", "spending_limit_reset_period", "spending_window_start",
	} {
		assert.Contains(t, alanlar, ad, "vitrin yanıtında %q alanı olmalı", ad)
	}
	assert.NotContains(t, alanlar, "spending_remaining",
		"kalan hak bu turda hesaplanmıyor; alan uydurulmamalı")
}
