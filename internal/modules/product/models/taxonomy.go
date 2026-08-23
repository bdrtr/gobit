package models

import "time"

// Category ürünleri ağaç biçiminde gruplayan kategoridir.
//
// ParentID aynı tablonun kendisine referansıdır: kategori ağacı tek modülün
// içinde yaşadığı için burada foreign key serbesttir (yasak olan CROSS-MODULE
// foreign key'dir, Prensip 2.2).
type Category struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Handle      string     `json:"handle"`
	Description *string    `json:"description,omitempty"`
	ParentID    *string    `json:"parent_id,omitempty"`
	IsActive    bool       `json:"is_active"`
	IsInternal  bool       `json:"is_internal"`
	Rank        int32      `json:"rank"`
	CreatedAt   time.Time  `json:"created_at,omitzero"`
	UpdatedAt   time.Time  `json:"updated_at,omitzero"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// Collection ürünleri düz bir kümede toplayan koleksiyondur (örn. "Yaz 2026").
type Collection struct {
	ID        string         `json:"id"`
	Title     string         `json:"title"`
	Handle    string         `json:"handle"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt *time.Time     `json:"deleted_at,omitempty"`
}

// Tag ürüne iliştirilen serbest etikettir.
type Tag struct {
	ID        string     `json:"id"`
	Value     string     `json:"value"`
	CreatedAt time.Time  `json:"created_at,omitzero"`
	UpdatedAt time.Time  `json:"updated_at,omitzero"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// Image ürünün görselidir. Dosyanın kendisi bu modülde tutulmaz; yalnızca
// erişilebilir bir bağlantı (URL) saklanır.
type Image struct {
	ID        string         `json:"id"`
	ProductID string         `json:"product_id"`
	URL       string         `json:"url"`
	Rank      int32          `json:"rank"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitzero"`
	UpdatedAt time.Time      `json:"updated_at,omitzero"`
	DeletedAt *time.Time     `json:"deleted_at,omitempty"`
}
