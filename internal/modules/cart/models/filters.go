package models

// CartFilter sepet listelemesinin ölçütleridir.
//
// Filtre alanları işaretçidir: nil "bu ölçütü uygulama" demektir. Böylece
// "completed = false" (yalnızca tamamlanmamış sepetler) ile "completed
// filtresi yok" (hepsi) birbirinden ayrılır; bool'un sıfır değeri sessizce
// filtreye dönüşmez.
//
// Tip repository'nin değil models'ın içindedir: hem servis hem repository
// models'ı zaten import eder, dolayısıyla servisin depo arayüzü repository
// paketine bağlanmadan bu ölçütleri taşıyabilir (ADR 0001).
type CartFilter struct {
	// CustomerID verilirse yalnızca o müşterinin sepetleri döner.
	CustomerID *string
	// RegionID verilirse yalnızca o bölgenin sepetleri döner.
	RegionID *string
	// Completed verilirse sepetler tamamlanmış olup olmamasına göre süzülür.
	Completed *bool
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
