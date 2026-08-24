// Package provider dış dünyaya bağlanan bileşenlerin (ödeme, kargo, bildirim,
// dosya) çekirdek sözleşmelerini tanımlar.
//
// Buradaki arayüzler ÇEKİRDEKTEDİR ve hiçbir modülü tanımaz (Prensip 2.4).
// Somut sağlayıcılar ya bir modülün içinde (örn. payment modülündeki manuel
// sağlayıcı) ya da bir plugin'de yaşar ve Registry'ye kaydolur; çekirdek onları
// yalnızca bu arayüzler üzerinden görür.
//
// # Neden çekirdekte
//
// Sağlayıcı sözleşmesi bir modüle ait olsaydı, plugin'ler o modülü import
// etmek zorunda kalırdı ve Faz 9'un "çekirdeğe dokunmadan sağlayıcı ekle"
// hedefi kırılırdı. Sözleşmenin çekirdekte olması, plugin ile modülü
// birbirinden bağımsız kılar.
package provider

// Provider tüm sağlayıcıların ortak yüzeyidir.
type Provider interface {
	// ID sağlayıcının benzersiz kimliğidir (örn. "manual", "stripe").
	// Kayıt ve seçim bu kimlikle yapılır; kalıcı verilere yazıldığı için
	// bir sürümden diğerine DEĞİŞMEMELİDİR.
	ID() string
}
