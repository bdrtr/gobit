package models

// InventoryItemFilter kalem listelemesinin ölçütleridir.
//
// Filtre alanları işaretçidir: nil "bu ölçütü uygulama" demektir. Böylece
// "requires_shipping = false" ile "requires_shipping filtresi yok" birbirinden
// ayrılır; bool'un sıfır değeri sessizce filtreye dönüşmez.
//
// Tip repository'nin değil models'ın içindedir: hem servis hem repository
// models'ı zaten import eder, dolayısıyla servisin depo arayüzü repository
// paketine bağlanmadan bu ölçütleri taşıyabilir.
type InventoryItemFilter struct {
	// SKU verilirse yalnızca o stok koduna sahip kalem döner.
	SKU *string
	// RequiresShipping verilirse sevkiyat gerektiren/gerektirmeyen kalemler
	// ayrıştırılır.
	RequiresShipping *bool
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
