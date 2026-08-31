package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// DTO'lar domain modellerinden AYRI tutulur: JSON alan adları dış sözleşmedir
// ve modelde yapılan bir yeniden adlandırma istemciyi kırmamalıdır.

// uploadDTO bir yükleme kaydının yanıt gövdesidir.
//
// # DEPO ANAHTARI ALANI YOKTUR
//
// Kayıtta vardır ama yayımlanmaz. İstemcinin dosyaya erişmek için ihtiyacı
// olan tek şey [uploadDTO.URL]'dir ve anahtarı ayrıca yayımlamak, aynı şeyi
// iki farklı sözleşmeyle vaat etmek olurdu: bugün adres anahtardan türüyor
// ama bir nesne deposunda adres imzalıdır ve anahtarla hiç ilgisi yoktur.
// İkisini birden yayımlayan bir uç, o gün sessizce yalan söylemeye başlardı.
//
// # GÜNCELLEME ZAMANI ALANI YOKTUR
//
// Bir yükleme kaydı hiç güncellenmez — değiştirme ucu yoktur, dosya da
// değişmez. updated_at'i yayımlamak, değişebileceğini vaat etmek olurdu.
type uploadDTO struct {
	// ID kaydın kimliğidir; silme ucu bunu alır.
	ID string `json:"id"`
	// URL dosyanın erişilebilir adresidir.
	//
	// Yerel sağlayıcıda KÖKE GÖRELİDİR ("/files/…"); farklı bir kökenden
	// sunulan vitrin, önüne kendi kökenini koyar.
	URL string `json:"url"`
	// ContentType dosyanın İÇERİĞİNDEN tespit edilmiş tipidir.
	//
	// İstemcinin yükleme sırasında bildirdiği tip DEĞİLDİR ve ondan farklı
	// olabilir; yanıtta dönmesinin sebebi tam da budur — istemci ne
	// gönderdiğini değil, sistemin ne SAKLADIĞINI görmelidir.
	ContentType string `json:"content_type"`
	// Size dosyanın bayt cinsinden boyutudur.
	Size int64 `json:"size"`
	// Checksum içeriğin SHA-256 özetidir (küçük harf onaltılık).
	Checksum string `json:"checksum"`
	// ProviderID dosyayı saklayan sağlayıcının kimliğidir.
	ProviderID string `json:"provider_id"`
	// OriginalName istemcinin bildirdiği dosya adıdır; boşsa alan hiç görünmez.
	//
	// GÖSTERİM verisidir: yönetim panelinde "hangi dosyayı yüklemiştim"
	// sorusunu yanıtlar. Hiçbir yol ifadesine ve hiçbir HTTP başlığına
	// girmez.
	OriginalName string `json:"original_name,omitempty"`
	// UploadedBy yüklemeyi yapan çağıranın kimliğidir; boşsa alan görünmez.
	UploadedBy string `json:"uploaded_by,omitempty"`
	// CreatedAt yüklemenin yapıldığı andır (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
}

// toUploadDTO domain kaydını yanıt gövdesine çevirir.
func toUploadDTO(u models.Upload) uploadDTO {
	return uploadDTO{
		ID:           u.ID,
		URL:          u.URL,
		ContentType:  u.ContentType,
		Size:         u.Size,
		Checksum:     u.Checksum,
		ProviderID:   u.ProviderID,
		OriginalName: u.OriginalName,
		UploadedBy:   u.UploadedBy,
		CreatedAt:    u.CreatedAt,
	}
}
