package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
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
	// PublicOnly hides the categories a shopper is not meant to see: the ones
	// the merchant switched off (is_active) and the ones that exist for
	// operators (is_internal).
	//
	// The default is FALSE, so the admin surface keeps seeing everything without
	// asking. The storefront is the caller that has to say true, and it is the
	// only one — a default that hid rows would mean a merchant losing sight of a
	// category the moment they switched it off.
	PublicOnly bool
	Limit      int
	Offset     int
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

// DeleteCollection SOFT deletes the collection and releases its products.
//
// # Why anything happens to the products at all
//
// A collection is the only taxonomy row a product points at with a COLUMN of
// its own (collection_id) rather than through a map table, and that column's
// "ON DELETE SET NULL" cannot fire against a soft delete — the row stays
// physically in place, so the database never runs it. Leaving the pointer alone
// was tried on paper and rejected: the storefront listing filters by
// collection_id WITHOUT joining the collection, so a shopper following an old
// link would keep getting the products of a collection that no longer exists,
// while the merchant's list no longer shows it.
//
// Both statements are in ONE transaction. Split, a failure between them leaves
// either a deleted collection with products still pointing at it, or products
// released from a collection that is still on the shelf.
//
// The number released is logged rather than returned: deleting one row can
// change hundreds of products, and the API's answer to a DELETE is the id it
// deleted, not a report about a side effect.
func (s *Service) DeleteCollection(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}

	released := 0
	err := s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		// The delete goes first because it is the statement that decides
		// whether there is anything to do: an unknown id stops here, before a
		// single product has been touched.
		if err := tx.SoftDeleteCollection(ctx, id); err != nil {
			return err
		}
		n, err := tx.ClearCollectionProducts(ctx, id)
		if err != nil {
			return err
		}
		released = n
		return nil
	})
	if err != nil {
		return err
	}

	if released > 0 {
		s.log.InfoContext(ctx, "the deleted collection's products were released",
			"collection", id, "products", released)
	}
	return nil
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

	filter := repository.CategoryFilter{
		ParentID:   opts.ParentID,
		PublicOnly: opts.PublicOnly,
		Limit:      limit,
		Offset:     offset,
	}

	items, err := s.repo.ListCategories(ctx, filter)
	if err != nil {
		return ListResult[models.Category]{}, err
	}
	count, err := s.repo.CountCategories(ctx, filter)
	if err != nil {
		return ListResult[models.Category]{}, err
	}
	return ListResult[models.Category]{Items: items, Count: &count, Offset: offset, Limit: limit}, nil
}

// DeleteCategory SOFT deletes the category; a node with children is REFUSED.
//
// # Why the refusal rather than a rule that decides for the merchant
//
// parent_id says "ON DELETE SET NULL", which cannot fire against a soft delete,
// so the children of a deleted node keep pointing at it. What that produces is
// worse than a dangling id: the tree is walked from the root down by parent_id,
// so the whole subtree DISAPPEARS from every listing while its rows stay live —
// a category the merchant can neither see nor reach nor put back.
//
// Two alternatives were rejected. Re-parenting the children onto the deleted
// node's parent changes the shape of a merchant's tree without being asked, and
// a grandparent is not a synonym for a parent. Clearing their parent_id is
// worse still: it promotes the whole subtree to the TOP LEVEL of the storefront
// menu, which is the most visible place in the catalog. A refusal is the only
// answer that cannot surprise: the operator moves or deletes the children and
// says what should happen to them.
//
// # The window this does not close
//
// The child count and the delete are two statements. A CreateCategory naming
// this node as its parent, landing between them, leaves exactly the orphan the
// guard exists to prevent. Closing it means locking the PARENT row in both
// paths — FOR UPDATE here, FOR SHARE in the create — which is a lock the create
// path does not take today and a new ordering rule for a subtree nobody can
// reach in the meantime. It is named here rather than left to be re-found, the
// way D19 records the same shape for LinkSalesChannel.
func (s *Service) DeleteCategory(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}

	return s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		children, err := tx.CountChildCategories(ctx, id)
		if err != nil {
			return err
		}
		if children > 0 {
			return errors.Conflict(codeInUse,
				"the category has %d subcategories and cannot be deleted; move or delete them first (%s)",
				children, id)
		}
		return tx.SoftDeleteCategory(ctx, id)
	})
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

// ListOptionValuesOptions is the set of criteria of the option vocabulary.
type ListOptionValuesOptions struct {
	// SalesChannelIDs are the channels the request is bound to; the value comes
	// from the request's IDENTITY and not from the query string, exactly as in
	// [StoreListOptions].
	SalesChannelIDs []string
	// PublicOnly narrows the vocabulary to published products.
	PublicOnly bool
	Limit      int
	Offset     int
}

// ListOptionValues returns the storefront's option vocabulary: the DISTINCT
// (option title, value) pairs the VISIBLE catalog offers.
//
// # Why the vocabulary is text and carries no id
//
// It is the one vocabulary endpoint that could not copy the shape of the other
// three. A collection, a category and a tag are entities a product REFERS to,
// so their listing hands the client an id and the filter takes that id. An
// option is not: `product_option.product_id` is NOT NULL, so an option belongs
// to exactly one product and an option-value id names one product's one value.
// A catalog filter on such an id would return at most one product, which is not
// a filter. What a client needs here is the TEXT pair, and that is what this
// returns.
//
// # Why the caller's visibility decides what is in it
//
// Every entry exists because some product carries it, so an unfiltered
// vocabulary would name values off draft products and off products sold in
// channels the caller holds no key for — telling the caller exactly what the
// product listing refuses to tell them. The same two conditions the storefront
// listing applies are applied here, on the product the value hangs from.
func (s *Service) ListOptionValues(
	ctx context.Context,
	opts ListOptionValuesOptions,
) (ListResult[models.OptionValuePair], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.OptionValuePair]{}, err
	}

	filter := repository.OptionValueFilter{
		SalesChannelIDs: opts.SalesChannelIDs,
		Limit:           limit,
		Offset:          offset,
	}
	if opts.PublicOnly {
		published := models.StatusPublished.String()
		filter.Status = &published
	}

	items, err := s.repo.ListOptionValues(ctx, filter)
	if err != nil {
		return ListResult[models.OptionValuePair]{}, err
	}

	count, err := s.repo.CountOptionValues(ctx, filter)
	if err != nil {
		return ListResult[models.OptionValuePair]{}, err
	}

	return ListResult[models.OptionValuePair]{Items: items, Count: &count, Offset: offset, Limit: limit}, nil
}

// DeleteTag SOFT deletes the tag.
//
// It has no guard, and that is the difference between a tag and the other two.
// A tag is a LABEL: nothing in the catalog is structured by it, the products
// that carry it stay exactly as they are, and every read of a product's tags
// already joins product_tag with "deleted_at IS NULL" — so the label simply
// stops being printed. A refusal like the category's would be asking the
// merchant to untag products one by one to retire a misspelling, which is the
// exact job they are trying to avoid.
//
// The tag can be created again afterwards: the value index is partial
// (WHERE deleted_at IS NULL), so a deleted value does not block a new tag —
// which is what that index was built for and what nothing had ever exercised.
func (s *Service) DeleteTag(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}
	return s.repo.SoftDeleteTag(ctx, id)
}
