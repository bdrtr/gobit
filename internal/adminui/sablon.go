package adminui

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
)

// sablonDosyalari panelin şablonlarıdır ve İKİLİYE GÖMÜLÜDÜR.
//
// Gömülü olması bir kolaylık değil, deponun dağıtım vaadinin gereğidir:
// "binary'yi çalıştır, çalışsın". Diskten okunan şablon, ikilinin yanında
// taşınması gereken ikinci bir artefakt ve yanlış dizinde açıldığında ancak
// ilk istekte görülen bir arıza demektir.
//
//go:embed sablonlar/*.gohtml
var sablonDosyalari embed.FS

// beklenenSablonlar panelin ÇALIŞMA ANINDA adıyla çağıracağı şablonlardır.
//
// Liste elle tutulur ve bu bilinçlidir. Şablon adı bir DİZEDİR: yazım hatası
// derlenir, lint görmez ve yalnızca o sayfa açıldığında patlar — üstelik
// kullanıcının karşısında. [sablonlariYukle] açılışta listedeki her adın
// gerçekten ayrıştırılmış küme içinde olduğunu doğrular, yani hata açılışa
// çekilir.
//
// Listenin BAYATLAMASI da yakalanır: ayrıştırılan ama listede olmayan bir
// şablon, adı hiç çağrılmayan ölü bir dosyadır ve o da hata verir. Tek yönlü
// bir denetim, dosyayı silmeyi unutan turu görmezdi.
var beklenenSablonlar = []string{
	"duzen.gohtml",
	"hata.gohtml",
}

// sablonSeti ayrıştırılmış şablon kümesidir.
type sablonSeti struct {
	sablonlar *template.Template
}

// sablonlariYukle gömülü şablonları ayrıştırır ve adlarını doğrular.
//
// AÇILIŞTA çağrılır ve hata döner; panik ÜRETMEZ. Bileşim kökü hatayı çıkış
// koduna çevirir, yani bozuk bir şablon sunucuyu hiç açtırmaz. Alternatifi —
// template.Must ile panik — aynı sonucu verirdi ama hatayı bileşim kökünün
// hata yoluna değil, runtime'ın panik yoluna sokardı.
func sablonlariYukle() (*sablonSeti, error) {
	kume, err := template.ParseFS(sablonDosyalari, "sablonlar/*.gohtml")
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeSablonBozuk,
			"panel şablonları ayrıştırılamadı")
	}

	ayristirilan := make([]string, 0, len(kume.Templates()))
	for _, t := range kume.Templates() {
		if t.Name() != "" {
			ayristirilan = append(ayristirilan, t.Name())
		}
	}
	slices.Sort(ayristirilan)

	var eksik []string
	for _, ad := range beklenenSablonlar {
		if !slices.Contains(ayristirilan, ad) {
			eksik = append(eksik, ad)
		}
	}
	if len(eksik) > 0 {
		return nil, errors.Internal(CodeSablonBozuk,
			"panelin beklediği şablon(lar) ayrıştırılamadı: %s (ayrıştırılanlar: %s)",
			strings.Join(eksik, ", "), strings.Join(ayristirilan, ", "))
	}

	var fazla []string
	for _, ad := range ayristirilan {
		if !slices.Contains(beklenenSablonlar, ad) {
			fazla = append(fazla, ad)
		}
	}
	if len(fazla) > 0 {
		return nil, errors.Internal(CodeSablonBozuk,
			"ayrıştırılan ama hiçbir yerde çağrılmayan şablon(lar): %s; "+
				"beklenenSablonlar listesi bayatlamış olmalı", strings.Join(fazla, ", "))
	}

	return &sablonSeti{sablonlar: kume}, nil
}

// yaz şablonu ÖNCE belleğe üretir, ancak sonra yanıta yazar.
//
// Sıra zorunludur ve gerekçesi ölçülmüştür: şablon doğrudan yazıcıya
// akıtılsaydı, ortada doğan bir hata 200 durum kodlu YARIM bir sayfa bırakırdı
// — başlık gönderildikten sonra ne panik yakalayıcı ne de hata yazıcısı bir şey
// yapabilir ve arıza istemcide sessizleşir. Tampon, hatayı hâlâ 500'e
// çevrilebilir bir yerde tutar.
func (s *sablonSeti) yaz(w http.ResponseWriter, r *http.Request, status int, ad string, veri any) {
	var buf bytes.Buffer
	if err := s.sablonlar.ExecuteTemplate(&buf, ad, veri); err != nil {
		corehttp.WriteError(r.Context(), w, errors.Wrap(err, errors.KindInternal,
			CodeSablonBozuk, "panel sayfası üretilemedi: %s", ad))
		return
	}
	corehttp.WriteHTML(r.Context(), w, status, buf.Bytes())
}
