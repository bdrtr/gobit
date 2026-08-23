// Package models customer modülünün domain modellerini tanımlar.
//
// Buradaki tipler veritabanı tiplerinden ARINDIRILMIŞTIR: pgtype bu pakete
// girmez, dönüşüm repository sarmalayıcısında yapılır. Böylece servis ve API
// katmanları depolama ayrıntısına bağlanmaz. Zamanlar UTC'dir; silme SOFT'tur.
package models

import (
	"strings"
	"time"
)

// Alan uzunluk sınırları.
//
// Sınırlar keyfi değildir: e-posta için 320 karakter RFC 5321'in yerel bölüm
// (64) + "@" + alan adı (255) üst sınırıdır. Diğerleri, tek bir isteğin
// veritabanına sınırsız metin yazmasını engelleyen makul tavanlardır ve
// migration'daki CHECK kısıtlarıyla ikinci kez zorlanır.
const (
	// MaxEmailLen bir e-posta adresinin azami uzunluğudur.
	MaxEmailLen = 320
	// MaxNameLen ad/soyad/şirket gibi kısa metin alanlarının azami uzunluğudur.
	MaxNameLen = 255
	// MaxPhoneLen telefon numarasının azami uzunluğudur.
	MaxPhoneLen = 32
	// MaxAddressLen adresin satırları için azami uzunluktur.
	MaxAddressLen = 255
	// MaxPostalCodeLen posta kodunun azami uzunluğudur.
	MaxPostalCodeLen = 32
)

// Customer bir müşteridir; misafir de kayıtlı da olabilir.
//
// # Misafir ve hesap ayrımı
//
// [Customer.HasAccount] ikisini ayıran TEK alandır. Ayrımın veri modelindeki
// karşılığı e-posta benzersizliğidir ve karar şudur:
//
//   - KAYITLI hesabın e-postası benzersizdir (kısmi benzersiz indeks:
//     UNIQUE (email) WHERE has_account AND deleted_at IS NULL).
//   - MİSAFİR kayıtlarının e-postası benzersiz DEĞİLDİR; aynı e-postayla
//     istenildiği kadar misafir kaydı açılabilir.
//
// Gerekçe: bu iki gereksinim aynı anda doğrudur ve tek bir tam benzersizlik
// kısıtıyla ifade edilemez. Misafir siparişi bir kimlik değil, tek seferlik bir
// alışverişin iletişim bilgisidir; aynı adresle ikinci kez sipariş vermek
// yasaklanamaz — yasaklansaydı vitrin, hiç hesap açmamış bir müşteriye
// "bu e-posta kullanılıyor" derdi ve müşteri kendi geçmiş siparişi yüzünden
// alışveriş yapamazdı. Kayıtlı hesap ise bir kimliktir: Faz 8'de gelecek
// "e-posta ile giriş" iki eşleşen kayıt arasında seçim yapamayacağı için tek
// olmak ZORUNDADIR.
//
// Kısmi indeks tam olarak bu ayrımı ifade eder ve kuralı uygulamaya değil
// veritabanına bağlar; misafirden hesaba geçişin çakışma davranışı için bkz.
// internal/modules/customer/service, ConvertGuestToAccount.
type Customer struct {
	// ID "cust_" önekli, zaman sıralı kimliktir.
	ID string
	// Email müşterinin e-posta adresidir; daima KÜÇÜK harfe normalize edilmiş
	// hâlde saklanır (bkz. [NormalizeEmail]).
	Email string
	// FirstName müşterinin adıdır; boş olabilir.
	FirstName string
	// LastName müşterinin soyadıdır; boş olabilir.
	LastName string
	// Phone müşterinin telefon numarasıdır; boş olabilir.
	Phone string
	// HasAccount kaydın bir hesap mı yoksa misafir kaydı mı olduğunu bildirir.
	HasAccount bool
	// Metadata çağıranın serbestçe yazdığı yapısal bağlamdır; boş olabilir.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// IsGuest kaydın misafir kaydı olup olmadığını bildirir.
func (c Customer) IsGuest() bool { return !c.HasAccount }

// CustomerGroup müşteri segmentidir (örn. "VIP", "B2B").
//
// Müşteri-grup ilişkisi ÇOKA-ÇOKTUR: bir müşteri birden çok gruba, bir grup
// birden çok müşteriye sahip olabilir. Grubun kimliği pricing modülünün kural
// bağlamındaki "customer_group_id" özniteliğine karşılık gelir; iki modül
// arasında derleme zamanı ya da veritabanı bağı YOKTUR (Prensip 2.2/2.4), bağ
// yalnızca hesaplama bağlamında kurulur.
type CustomerGroup struct {
	// ID "custgrp_" önekli kimliktir.
	ID string
	// Name grubun görünen adıdır; canlı kayıtlar arasında benzersizdir.
	Name string
	// Metadata çağıranın serbestçe yazdığı yapısal bağlamdır; boş olabilir.
	Metadata map[string]any
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// CustomerAddress bir müşteriye ait adrestir.
//
// Varsayılan kargo ve fatura işaretleri müşteri başına EN FAZLA BİR adreste
// bulunabilir; kısıt veritabanındaki kısmi benzersiz indekslerle zorlanır
// (bkz. migrations/000001_customer_init.up.sql).
type CustomerAddress struct {
	// ID "addr_" önekli kimliktir.
	ID string
	// CustomerID adresin sahibi müşteridir.
	CustomerID string
	// FirstName adresin üzerindeki addır; boş olabilir.
	FirstName string
	// LastName adresin üzerindeki soyaddır; boş olabilir.
	LastName string
	// Company şirket adıdır; boş olabilir.
	Company string
	// Address1 adresin ilk satırıdır; zorunludur.
	Address1 string
	// Address2 adresin ikinci satırıdır; boş olabilir.
	Address2 string
	// City şehirdir; zorunludur.
	City string
	// CountryCode ISO 3166-1 alpha-2 ülke kodudur; daima BÜYÜK harf saklanır.
	CountryCode string
	// PostalCode posta kodudur; boş olabilir.
	PostalCode string
	// Phone adresin iletişim telefonudur; boş olabilir.
	Phone string
	// IsDefaultShipping adresin varsayılan kargo adresi olduğunu bildirir.
	IsDefaultShipping bool
	// IsDefaultBilling adresin varsayılan fatura adresi olduğunu bildirir.
	IsDefaultBilling bool
	// CreatedAt kaydın oluşturulma anıdır (UTC).
	CreatedAt time.Time
	// UpdatedAt kaydın son güncellenme anıdır (UTC).
	UpdatedAt time.Time
	// DeletedAt soft delete anıdır; nil ise kayıt canlıdır.
	DeletedAt *time.Time
}

// NormalizeEmail e-postayı saklama biçimine çevirir: kırpılır ve KÜÇÜK harfe
// indirilir.
//
// Normalizasyon SAKLAMADA yapılır, okumada değil. Benzersizlik indeksi ham
// sütun üzerindedir; "Ali@X.com" ile "ali@x.com" aynı hesabı göstermeliyse
// ikisinin de aynı baytlara inmesi gerekir. Okuma anında normalize etmek,
// tabloya iki farklı yazımın girmesini engellemezdi.
//
// Küçük harfe indirme yerel bölüm (@ öncesi) için teknik olarak RFC'ye aykırı
// sayılabilir — RFC 5321 yerel bölümü büyük/küçük harfe duyarlı bırakır — ama
// pratikte hiçbir sağlayıcı bu ayrımı kullanmaz ve duyarlı bırakmak aynı
// müşteriye iki hesap açtırırdı. Ticari doğruluk burada standart harfiyetinin
// önündedir ve karar bilinçlidir.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// NormalizeCountryCode ülke kodunu saklama biçimine çevirir: kırpılır ve BÜYÜK
// harfe çıkarılır. Doğrulama çağırana aittir.
func NormalizeCountryCode(code string) string {
	return strings.ToUpper(strings.TrimSpace(code))
}
