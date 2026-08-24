package models

// OrderFilter sipariş listelemesinin ölçütleridir.
//
// Filtre alanları işaretçidir: nil "bu ölçütü uygulama" demektir. Böylece
// "status = pending" ile "durum filtresi yok" birbirinden ayrılır; bir tipin
// sıfır değeri sessizce filtreye dönüşmez.
//
// Tip repository'nin değil models'ın içindedir: hem servis hem repository
// models'ı zaten import eder, dolayısıyla servisin depo arayüzü repository
// paketine bağlanmadan bu ölçütleri taşıyabilir (ADR 0001).
type OrderFilter struct {
	// CustomerID verilirse yalnızca o müşterinin siparişleri döner.
	CustomerID *string
	// RegionID verilirse yalnızca o bölgenin siparişleri döner.
	RegionID *string
	// Status verilirse siparişler duruma göre süzülür.
	Status *OrderStatus
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}

// ChildFilter siparişin iade/değişim/hasar kayıtlarının listeleme ölçütüdür.
//
// Üçü aynı şekli paylaşır (bir sipariş + sayfalama) ve tek tip olması
// çağrı yerinde parametrelerin yer değiştirmesini imkânsız kılar.
type ChildFilter struct {
	// OrderID kayıtların ait olduğu siparişdir; zorunludur.
	OrderID string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
