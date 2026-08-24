package models

// CollectionFilter ödeme koleksiyonu listelemesinin süzgeç ve sayfalama
// parametreleridir.
//
// İşaretçi alanlar "verilmedi" ile "boş verildi" ayrımını korur: nil bir
// Reference süzgeç uygulanmadığı, boş dizeye işaret eden bir Reference ise
// referansı boş olan kayıtların istendiği anlamına gelir. Değer tipi
// kullanılsaydı ikisi ayırt edilemezdi.
type CollectionFilter struct {
	// Reference verilirse yalnızca o referansa sahip koleksiyonlar döner.
	Reference *string
	// Status verilirse yalnızca o durumdaki koleksiyonlar döner.
	Status *string
	// Limit döndürülecek azami satır sayısıdır.
	Limit int64
	// Offset atlanacak satır sayısıdır.
	Offset int64
}
