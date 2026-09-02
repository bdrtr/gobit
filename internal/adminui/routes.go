package adminui

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Routes panelin yollarını router'a bağlar.
//
// Yollar TAM YOLLA kaydedilir; bir ön ek MOUNT EDİLMEZ. Kural modüllerinkiyle
// aynıdır ve aynı sebebe dayanır: mount eden ilk taraf o alt ağacın tamamını
// sahiplenir ve aynı öneki kullanan başka bir tarafla çakışır.
//
// # Koruma BURADA takılmaz
//
// Panelin kimlik halkası bileşim kökünde, koruma yığınının içine konur — bu
// metotta değil. Router, route kaydından sonra halka eklenmesini panikle
// reddediyor ve sağlık uçları router kurulurken kaydediliyor; yani halkayı
// buradan takmak MÜMKÜN değil. Ayrım ADR 0011'de yazılıdır.
func (u *UI) Routes(r chi.Router) {
	r.Get(URLPrefix, u.giris)
}

// giris panelin giriş sayfasıdır.
//
// BUGÜN bir yer tutucudur ve HİÇBİR VERİ taşımaz: oturum ve koruma halkası
// henüz kurulmadığı için bu ağaç kimliksizdir ve veri taşıyan bir sayfa
// yayımlamak, korumadan önce içerik yayımlamak olurdu. Sayfanın bu turdaki
// işi, yazma yolunun uçtan uca çalıştığını göstermektir: gömülü şablon →
// ayrıştırma → tampon → çekirdeğin HTML yazıcısı.
func (u *UI) giris(w http.ResponseWriter, r *http.Request) {
	u.sablonlar.yaz(w, r, http.StatusOK, "duzen.gohtml", map[string]any{
		"Baslik": "Yönetim",
		"Icerik": "Panel kuruluyor.",
	})
}
