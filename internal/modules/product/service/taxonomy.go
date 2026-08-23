package service

import (
	"context"

	"github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// CreateCollectionInput yeni bir koleksiyonun girdisidir.
type CreateCollectionInput struct {
	Title    string
	Handle   string
	Metadata map[string]any
}

// CreateCategoryInput yeni bir kategorinin girdisidir.
type CreateCategoryInput struct {
	Name        string
	Handle      string
	Description *string
	ParentID    *string
	IsActive    *bool
	IsInternal  bool
	Rank        int32
}

// ListCategoriesOptions kategori listelemesinin ölçütleridir.
type ListCategoriesOptions struct {
	ParentID *string
	Limit    int
	Offset   int
}

// CreateCollection koleksiyon oluşturur.
//
// Handle boş bırakılırsa başlıktan üretilir ve benzersizdir; kullanımdaysa
// errors.Conflict döner.
func (s *Service) CreateCollection(ctx context.Context, in CreateCollectionInput) (models.Collection, error) {
	title, err := requireText("title", in.Title, maxTitleLen)
	if err != nil {
		return models.Collection{}, err
	}
	handle, err := resolveHandle(in.Handle, title)
	if err != nil {
		return models.Collection{}, err
	}

	return s.repo.CreateCollection(ctx, models.Collection{
		ID:       newID(prefixCollection),
		Title:    title,
		Handle:   handle,
		Metadata: in.Metadata,
	})
}

// GetCollection koleksiyonu kimliğe göre döner.
func (s *Service) GetCollection(ctx context.Context, id string) (models.Collection, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Collection{}, err
	}
	return s.repo.GetCollection(ctx, id)
}

// ListCollections koleksiyonları sayfalı döner.
func (s *Service) ListCollections(ctx context.Context, limit, offset int) (ListResult[models.Collection], error) {
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return ListResult[models.Collection]{}, err
	}

	items, err := s.repo.ListCollections(ctx, limit, offset)
	if err != nil {
		return ListResult[models.Collection]{}, err
	}
	count, err := s.repo.CountCollections(ctx)
	if err != nil {
		return ListResult[models.Collection]{}, err
	}
	return ListResult[models.Collection]{Items: items, Count: count, Offset: offset, Limit: limit}, nil
}

// CreateCategory kategori oluşturur.
//
// ParentID verilirse üst kategorinin VAR OLDUĞU doğrulanır: veritabanı foreign
// key'i bunu zaten reddederdi, ama oradan dönen hata "ihlal edilen kısıt"tır;
// buradaki kontrol istemciye hangi kimliğin bulunamadığını söyler.
func (s *Service) CreateCategory(ctx context.Context, in CreateCategoryInput) (models.Category, error) {
	name, err := requireText("name", in.Name, maxTitleLen)
	if err != nil {
		return models.Category{}, err
	}
	handle, err := resolveHandle(in.Handle, name)
	if err != nil {
		return models.Category{}, err
	}
	description, err := trimOptional(in.Description, "description", maxDescriptionLen)
	if err != nil {
		return models.Category{}, err
	}

	if in.ParentID != nil {
		parentID, err := requireID("parent_id", *in.ParentID)
		if err != nil {
			return models.Category{}, err
		}
		if _, err := s.repo.GetCategory(ctx, parentID); err != nil {
			return models.Category{}, err
		}
		in.ParentID = &parentID
	}

	isActive := true
	if in.IsActive != nil {
		isActive = *in.IsActive
	}

	return s.repo.CreateCategory(ctx, models.Category{
		ID:          newID(prefixCategory),
		Name:        name,
		Handle:      handle,
		Description: description,
		ParentID:    in.ParentID,
		IsActive:    isActive,
		IsInternal:  in.IsInternal,
		Rank:        in.Rank,
	})
}

// GetCategory kategoriyi kimliğe göre döner.
func (s *Service) GetCategory(ctx context.Context, id string) (models.Category, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Category{}, err
	}
	return s.repo.GetCategory(ctx, id)
}

// ListCategories kategorileri sayfalı döner.
func (s *Service) ListCategories(ctx context.Context, opts ListCategoriesOptions) (ListResult[models.Category], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.Category]{}, err
	}

	items, err := s.repo.ListCategories(ctx, opts.ParentID, limit, offset)
	if err != nil {
		return ListResult[models.Category]{}, err
	}
	count, err := s.repo.CountCategories(ctx, opts.ParentID)
	if err != nil {
		return ListResult[models.Category]{}, err
	}
	return ListResult[models.Category]{Items: items, Count: count, Offset: offset, Limit: limit}, nil
}

// CreateTag etiket oluşturur.
//
// Etiket değeri benzersizdir; aynı değer ikinci kez eklenmek istenirse
// errors.Conflict döner ve mesaj MEVCUT etiketin kimliğini taşır — istemci
// böylece ikinci bir sorgu yapmadan var olanı kullanabilir.
func (s *Service) CreateTag(ctx context.Context, value string) (models.Tag, error) {
	clean, err := requireText("value", value, maxValueLen)
	if err != nil {
		return models.Tag{}, err
	}

	existing, err := s.repo.GetTagByValue(ctx, clean)
	switch {
	case err == nil:
		return models.Tag{}, errors.Conflict(codeInvalidInput,
			"etiket zaten var: %q (%s)", clean, existing.ID)
	case !errors.IsNotFound(err):
		return models.Tag{}, err
	}

	return s.repo.CreateTag(ctx, models.Tag{ID: newID(prefixTag), Value: clean})
}

// ListTags etiketleri sayfalı döner.
func (s *Service) ListTags(ctx context.Context, limit, offset int) (ListResult[models.Tag], error) {
	limit, offset, err := normalizePaging(limit, offset)
	if err != nil {
		return ListResult[models.Tag]{}, err
	}

	items, err := s.repo.ListTags(ctx, limit, offset)
	if err != nil {
		return ListResult[models.Tag]{}, err
	}
	count, err := s.repo.CountTags(ctx)
	if err != nil {
		return ListResult[models.Tag]{}, err
	}
	return ListResult[models.Tag]{Items: items, Count: count, Offset: offset, Limit: limit}, nil
}
