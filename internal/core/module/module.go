// Package module commerce modüllerinin uygulaması gereken sözleşmeyi ve
// modülleri sırayla ayağa kaldıran kaydı (registry) tanımlar.
//
// Bir modül kendi modellerine, tablolarına ve servisine sahiptir; başka bir
// modülün paketini import ETMEZ (plan Bölüm 2.1/2.4, ADR 0001). Modüller arası
// erişim container'dan isimle çözülen servis interface'leri üzerinden olur.
package module

import (
	"context"
	"io/fs"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/internal/core/container"
)

// Module bir commerce modülünün çekirdeğe sunduğu sözleşmedir (plan Bölüm 5.1).
type Module interface {
	// Name modülün benzersiz adıdır (örn. "product"). Container'daki servis
	// adlarında ve migration versiyon tablosunda önek olarak kullanılır.
	Name() string

	// Register modülün servislerini container'a kaydeder, link tanımlarını ve
	// event subscriber'larını bildirir.
	//
	// DİKKAT: Bu aşamada BAŞKA modüllerin servisleri henüz kayıtlı olmayabilir.
	// Bu yüzden başka modülün servisi burada Resolve EDİLMEMELİ; container'a
	// tembel yapıcı verilmeli ve çözüm ilk kullanımda yapılmalıdır.
	Register(ctx context.Context, c *container.Container) error

	// Migrations modülün migration dosyalarını döner (genellikle embed.FS).
	// Modülün migration'ı yoksa nil dönebilir.
	Migrations() fs.FS

	// Routes modülün store/admin route'larını verilen router'a bağlar.
	Routes(r chi.Router)
}
