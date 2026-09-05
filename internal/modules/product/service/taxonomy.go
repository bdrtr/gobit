package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// CreateCollectionInput is the input of a new collection.
type CreateCollectionInput struct {
	Title    string
	Handle   string
	Metadata map[string]any
}

// CreateCategoryInput is the input of a new category.
type CreateCategoryInput struct {
	Name        string
	Handle      string
	Description *string
	ParentID    *string
	IsActive    *bool
	IsInternal  bool
	Rank        int32
}

// ListCategoriesOptions is the criteria of a category listing.
type ListCategoriesOptions struct {
	ParentID *string
	Limit    int
	Offset   int
}

// CreateCollection creates a collection.
//
// If the handle is left empty it is derived from the title, and it is unique;
// if it is already in use, errors.Conflict is returned.
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

// GetCollection returns the collection by id.
func (s *Service) GetCollection(ctx context.Context, id string) (models.Collection, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Collection{}, err
	}
	return s.repo.GetCollection(ctx, id)
}

// ListCollections returns the collections paginated.
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
	return ListResult[models.Collection]{Items: items, Count: &count, Offset: offset, Limit: limit}, nil
}

// CreateCategory creates a category.
//
// If a ParentID is given, the parent category is verified to EXIST: the foreign
// key of the database would reject this already, but the error coming from
// there is "a violated constraint"; the check here tells the client which id
// could not be found.
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

// GetCategory returns the category by id.
func (s *Service) GetCategory(ctx context.Context, id string) (models.Category, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Category{}, err
	}
	return s.repo.GetCategory(ctx, id)
}

// ListCategories returns the categories paginated.
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
	return ListResult[models.Category]{Items: items, Count: &count, Offset: offset, Limit: limit}, nil
}

// CreateTag creates a tag.
//
// The tag value is unique; if the same value is added a second time
// errors.Conflict is returned and the message carries the id of the EXISTING
// tag — that way the client can use the existing one without a second query.
func (s *Service) CreateTag(ctx context.Context, value string) (models.Tag, error) {
	clean, err := requireText("value", value, maxValueLen)
	if err != nil {
		return models.Tag{}, err
	}

	existing, err := s.repo.GetTagByValue(ctx, clean)
	switch {
	case err == nil:
		return models.Tag{}, errors.Conflict(codeInvalidInput,
			"the tag already exists: %q (%s)", clean, existing.ID)
	case !errors.IsNotFound(err):
		return models.Tag{}, err
	}

	return s.repo.CreateTag(ctx, models.Tag{ID: newID(prefixTag), Value: clean})
}

// ListTags returns the tags paginated.
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
	return ListResult[models.Tag]{Items: items, Count: &count, Offset: offset, Limit: limit}, nil
}
