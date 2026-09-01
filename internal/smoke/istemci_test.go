//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	corehttp "github.com/bdrtr/gobit/internal/core/http"
	authmodels "github.com/bdrtr/gobit/internal/modules/auth/models"
)

// Bu dosya, GERÇEK SÜREÇTE hiç koşulmamış iki yüzeyin (GraphQL vitrini ve B2B)
// senaryolarının paylaştığı HTTP yardımcılarıdır.
//
// surec_test.go'daki [surec.iste] bilinçli olarak dardır: gövdesiz istek kurar
// ve tek kimlik biçimi olarak Bearer bilir. Buradaki yüzeyler ise JSON gövde
// gönderir ve vitrin tarafında kimlik Authorization'da DEĞİL, publishable
// anahtar başlığındadır. Yardımcılar bu yüzden eklendi; kalıp aynıdır ve istek
// yine [surec.gonder] üzerinden gider, yani ikinci bir HTTP istemcisi ya da
// ikinci bir koşum altyapısı YOKTUR.

// jsonIste gövdeli bir JSON isteği kurar ve gönderir.
//
// govde nil ise istek gövdesiz gider; aynı yardımcının hem POST hem GET
// kurabilmesi, çağıranın iki fonksiyon arasında seçim yapmasını gereksiz kılar.
//
// Kimlik başlığı çağırandan gelir ve bu bilinçlidir: yönetim yüzeyi
// Authorization, vitrin yüzeyi publishable anahtar başlığı ister. İkisini tek
// bir "jeton" parametresine sıkıştırmak, çağrı yerinde hangi yüzeye gidildiğini
// okunamaz hâle getirirdi — ki bu depoda korumanın tamamı o ayrıma dayanır.
func (s *surec) jsonIste(
	metot, yol string,
	govde any,
	basliklar map[string]string,
) (kod int, yanit string) {
	s.t.Helper()

	var ham io.Reader = http.NoBody
	if govde != nil {
		kodlanmis, err := json.Marshal(govde)
		require.NoError(s.t, err, "istek gövdesi kodlanamadı: %s %s", metot, yol)
		ham = bytes.NewReader(kodlanmis)
	}

	istek, err := http.NewRequestWithContext(s.t.Context(), metot, s.adres+yol, ham)
	require.NoError(s.t, err, "istek kurulamadı: %s %s", metot, yol)

	if govde != nil {
		istek.Header.Set("Content-Type", "application/json")
	}
	for ad, deger := range basliklar {
		istek.Header.Set(ad, deger)
	}

	return s.gonder(istek)
}

// yonetimIste yönetim yüzeyine jetonlu bir JSON isteği yapar.
func (s *surec) yonetimIste(metot, yol, jeton string, govde any) (kod int, yanit string) {
	s.t.Helper()

	return s.jsonIste(metot, yol, govde, map[string]string{"Authorization": "Bearer " + jeton})
}

// vitrinIste vitrin yüzeyine publishable anahtarla bir JSON isteği yapar.
//
// anahtar boşsa başlık HİÇ gönderilmez; "kimliksiz istek reddediliyor mu"
// iddiası, boş bir başlık göndermekle değil başlığı hiç göndermemekle sınanır
// — üretimde eksik yapılandırmanın ürettiği durum budur.
func (s *surec) vitrinIste(metot, yol, anahtar string, govde any) (kod int, yanit string) {
	s.t.Helper()

	basliklar := map[string]string{}
	if anahtar != "" {
		basliklar[corehttp.PublishableKeyHeader] = anahtar
	}

	return s.jsonIste(metot, yol, govde, basliklar)
}

// zarfVerisi tekil yanıt zarfının data alanını hedef tipe çözer.
//
// Zarf ({"data": …}) plan Bölüm 8'in sözleşmesidir ve senaryolar onu ağdan
// geçtiği hâliyle okur; modüllerin DTO tipleri import EDİLMEZ. Sebep
// acilis_test.go'daki jetonAl ile aynıdır: paylaşılan bir Go tipi, alan adı
// değişse bile testi yeşil bırakırdı — oysa değişen şey tam olarak istemcinin
// gördüğü sözleşmedir.
func zarfVerisi[T any](t *testing.T, yanit string) T {
	t.Helper()

	var zarf struct {
		Data T `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(yanit), &zarf),
		"yanıt zarfı çözülemedi; gövde: %s", yanit)

	return zarf.Data
}

// satisKanaliAc yeni bir satış kanalı açar ve kimliğini döner.
//
// Kanal, vitrin yüzeyinin ÇALIŞMA KOŞULUDUR: publishable anahtar bir kanalı
// temsil eder ve katalog süzgeci o kanala dayanır. Kanalsız bir anahtarla
// açılan vitrin boş görünür, yani kurulum adımının atlanması senaryoyu
// "yeşil ama hiçbir şey kanıtlamayan" bir teste çevirirdi.
func satisKanaliAc(t *testing.T, s *surec, jeton, ad string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/sales-channels", jeton,
		map[string]any{"name": ad})
	require.Equal(t, http.StatusCreated, kod, "satış kanalı açılamadı; gövde: %s", govde)

	kanal := zarfVerisi[struct {
		ID string `json:"id"`
	}](t, govde)
	require.NotEmpty(t, kanal.ID, "satış kanalı kimlik dönmeli; gövde: %s", govde)

	return kanal.ID
}

// vitrinAnahtariAl kanala bağlı bir publishable anahtar üretir ve DÜZ metnini
// döner.
//
// Düz anahtar yalnızca bu yanıtta görünür (bkz. auth api adminCreateAPIKey);
// senaryonun onu saklamaktan başka yolu yoktur.
func vitrinAnahtariAl(t *testing.T, s *surec, jeton, kanalID string) string {
	t.Helper()

	kod, govde := s.yonetimIste(http.MethodPost, "/admin/v1/api-keys", jeton, map[string]any{
		"type":              string(authmodels.APIKeyPublishable),
		"title":             "smoke vitrin anahtarı",
		"sales_channel_ids": []string{kanalID},
	})
	require.Equal(t, http.StatusCreated, kod, "publishable anahtar üretilemedi; gövde: %s", govde)

	anahtar := zarfVerisi[struct {
		Key string `json:"key"`
	}](t, govde)
	require.NotEmpty(t, anahtar.Key, "yanıt düz anahtarı taşımalı; gövde: %s", govde)

	return anahtar.Key
}

// yonetimZeminiKur tohumlanmış yöneticiyle giriş yapar ve vitrin kimliğini
// hazırlar.
//
// Üç adım (giriş, kanal, anahtar) tek yerde durur çünkü ikisi de YENİ yüzeyin
// ön koşuludur ve senaryolar arasında ayrışmaları, iki testin farklı
// kurulumları sınaması demek olurdu.
func yonetimZeminiKur(t *testing.T, s *surec, kanalAdi string) (jeton, kanalID, vitrinAnahtari string) {
	t.Helper()

	jeton = jetonAl(t, s, tohumEposta, tohumParola)
	kanalID = satisKanaliAc(t, s, jeton, kanalAdi)
	vitrinAnahtari = vitrinAnahtariAl(t, s, jeton, kanalID)

	return jeton, kanalID, vitrinAnahtari
}
