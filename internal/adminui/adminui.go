package adminui

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/query"
)

// Panelin yol öneki ve container'da çözdüğü adlar.
const (
	// URLPrefix panelin yol önekidir.
	//
	// Yönetim API'sinin önekinin ALTINDA değildir ve bu bilinçlidir: oraya
	// konsaydı adres çubuğundan gelen her sayfa 401 alırdı (koruma yalnızca
	// Authorization başlığı okur), HTML uçları OpenAPI belgesine sızardı ve
	// router ağacını gezen yetki testi her sayfadan 403 beklerdi (ADR 0011).
	//
	// Önekin koruma yığınına AÇIKÇA eklenmesi zorunludur: kapsam segment
	// sınırında eşleştiği için bu ağaç kendiliğinden ne kimliğe ne kotaya
	// girer.
	URLPrefix = "/admin/ui"

	// ServiceQuery cross-module okuma katmanının container'daki adıdır.
	ServiceQuery = "core.query"
)

// Hata kodları. İstemciler bunlara göre dallanabilir; mesajlar değişebilir,
// kodlar değişmez.
const (
	// CodeNotReady panelin eksik bağımlılıkla kurulduğunu bildirir.
	CodeNotReady = "adminui_not_ready"
	// CodeDependencyMissing container'da beklenen bir servisin bulunmadığını
	// bildirir.
	CodeDependencyMissing = "adminui_dependency_missing"
	// CodeSablonBozuk şablonun ayrıştırılamadığını ya da üretilemediğini
	// bildirir.
	CodeSablonBozuk = "adminui_template_invalid"
)

// Katalog panelin okuma yüzeyidir ve TÜKETEN tarafta tanımlıdır (ADR 0001).
//
// Yüzey Query katmanının tamamı DEĞİL, panelin kullandığı tek metottur: dar
// tutulması, bir gün Query'nin başka bir metodunun değişmesinin paneli
// derleme zamanında kırmamasını sağlar. Panel hiçbir modülü import etmez;
// katalog verisi bu arayüzden gelir (ADR 0004/0006).
type Katalog interface {
	// Graph kök kayıtları çeker, link'leri çözer ve birleşik veriyi döner.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// UI yönetim panelidir. Eşzamanlı kullanıma güvenlidir.
type UI struct {
	katalog   Katalog
	sablonlar *sablonSeti
}

// FromContainer paneli container üzerinde kurar.
//
// Ad KONVANSİYONELDİR ve internal/arch'taki kablolama denetimi onu ters yönde
// kullanır: bileşim kökünün kurduğu ama denetimin yapıcı olarak GÖREMEDİĞİ bir
// paketi yakalamak için. Aynı ad internal/workflows ağacında da geçerlidir;
// bu paket o kalıbın ikinci örneğidir (ADR 0011).
//
// Şablonlar BURADA ayrıştırılır, ilk istekte değil: bozuk bir şablon sunucuyu
// açılışta düşürmelidir. Bileşim kökü hatayı çıkış koduna çevirir, yani arıza
// kullanıcının karşısında değil, dağıtımda görünür.
func FromContainer(c *container.Container) (*UI, error) {
	if c == nil {
		return nil, errors.Internal(CodeNotReady,
			"yönetim paneli container olmadan kurulamaz")
	}

	katalog, err := container.Resolve[Katalog](c, ServiceQuery)
	if err != nil {
		return nil, errors.Wrap(err, errors.KindOf(err), CodeDependencyMissing,
			"yönetim paneli %q servisini çözemedi", ServiceQuery)
	}

	sablonlar, err := sablonlariYukle()
	if err != nil {
		return nil, err
	}

	return &UI{katalog: katalog, sablonlar: sablonlar}, nil
}
