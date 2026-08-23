// Package models product modülünün domain modellerini tanımlar.
//
// Bu tipler modülün DIŞARIYA gösterdiği şekildir: servis bunları döner, API
// katmanı doğrudan JSON'a yazar. Bilinçli olarak veritabanı tiplerinden
// (pgtype.*) arındırılmışlardır — pgtype repository sınırının içinde kalır;
// aksi hâlde sürücü seçimi modülün tüm katmanlarına sızardı.
//
// Zaman alanları UTC'dir ve veritabanı saatinden gelir. Silme SOFT'tur:
// DeletedAt dolu bir kayıt hiçbir okuma sorgusundan dönmez.
package models

import "time"

// Status bir ürünün yayın durumudur.
//
// Değer kümesi veritabanındaki product_status_check kısıtıyla birebir aynıdır;
// ikisinden biri değişirse diğeri de değişmelidir.
type Status string

// Ürün yayın durumları.
const (
	// StatusDraft taslaktır; store API'sinde görünmez.
	StatusDraft Status = "draft"
	// StatusPublished yayındadır; store API'sinin varsayılan filtresidir.
	StatusPublished Status = "published"
	// StatusArchived arşivlenmiştir; store API'sinde görünmez ama silinmemiştir.
	StatusArchived Status = "archived"
)

// Valid durumun tanımlı değerlerden biri olup olmadığını bildirir.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusPublished, StatusArchived:
		return true
	default:
		return false
	}
}

// String durumun metin karşılığını döner.
func (s Status) String() string { return string(s) }

// Product katalogdaki bir üründür.
//
// Ürünün FİYATI ve STOĞU burada YOKTUR: ikisi de ayrı modüllerin verisidir
// (Prensip 2.3) ve varyant üzerinden link'lerle bağlanır. Ürün yalnızca
// katalog bilgisini taşır.
type Product struct {
	ID            string         `json:"id"`
	Handle        string         `json:"handle"`
	Title         string         `json:"title"`
	Subtitle      *string        `json:"subtitle,omitempty"`
	Description   *string        `json:"description,omitempty"`
	Thumbnail     *string        `json:"thumbnail,omitempty"`
	Status        Status         `json:"status"`
	IsGiftcard    bool           `json:"is_giftcard"`
	Discountable  bool           `json:"discountable"`
	Weight        *int32         `json:"weight,omitempty"`
	Length        *int32         `json:"length,omitempty"`
	Height        *int32         `json:"height,omitempty"`
	Width         *int32         `json:"width,omitempty"`
	Material      *string        `json:"material,omitempty"`
	OriginCountry *string        `json:"origin_country,omitempty"`
	CollectionID  *string        `json:"collection_id,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	DeletedAt     *time.Time     `json:"deleted_at,omitempty"`

	// Aşağıdaki alanlar ilişkili kayıtlardır; yalnızca çağıran istediğinde
	// doldurulur ve boşken JSON'a hiç yazılmaz. Doldurulmaları TOPLU
	// sorgularla yapılır (bkz. repository ...ByProductIDs), kayıt başına
	// sorguyla değil.
	Variants   []Variant  `json:"variants,omitempty"`
	Options    []Option   `json:"options,omitempty"`
	Images     []Image    `json:"images,omitempty"`
	Tags       []Tag      `json:"tags,omitempty"`
	Categories []Category `json:"categories,omitempty"`
}
