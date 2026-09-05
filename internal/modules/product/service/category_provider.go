package service

import (
	"context"

	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// This file is the CATEGORY half of the module's read surface (ADR 0004); the
// product and variant entities are in provider.go.
//
// # Why the category is an entity of its own
//
// The catalog listing takes a category id ([filterCategoryID]) and nobody
// outside this module has one. A panel has the WORD an operator typed and a
// storefront has the word a shopper clicked; without a way to turn that word
// into an id, the filter next door is a capability with no caller — which is
// exactly the state the storefront was in before its vocabulary endpoints were
// written (see api/store.go, "The storefront's vocabulary endpoints").
//
// Those endpoints solved it for HTTP only. The read layer is a different door:
// the admin panel reaches the modules only through Query (ADR 0011) and cannot
// call a storefront endpoint to fill a dropdown — it would have to speak HTTP to
// its own process, and it would get the SHOPPER's view of the taxonomy while
// standing on the operator's side of it.
//
// # What it is not
//
// It is not the product-category MEMBERSHIP. This entity answers "which
// categories exist", not "which products are in this one"; the second question
// is the product entity's category_id filter, and it is answered there — in
// SQL, once, with an EXISTS. Two entities answering one question is how two
// answers start to disagree.

// categoryProvider offers the category records to the Query layer.
type categoryProvider struct {
	repo repository.Store
}

// That the provider satisfies the core contract is pinned at compile time.
var _ query.Provider = (*categoryProvider)(nil)

// NewCategoryProvider builds the Query provider of the "category" entity.
//
// It takes the repository rather than the service, the way its siblings in
// provider.go do: the read surface is a projection of rows and it has no write
// rule to honor, so going through the service would add a layer whose only
// contribution is another place for the two to drift.
func NewCategoryProvider(repo repository.Store) query.Provider {
	return &categoryProvider{repo: repo}
}

// Entity returns the name of the entity the provider offers.
func (c *categoryProvider) Entity() string { return EntityCategory }

// List returns the category records.
//
// Supported filters: parent_id, public_only and id/ids. An unrecognized filter
// returns errors.Invalid (ADR 0004), for the reason [productProvider.List]
// gives: a filter that is silently dropped leaves the caller believing it asked
// a narrower question than it did.
//
// parent_id walks the tree one level at a time, which is what a navigation menu
// or a dropdown asks for; without it the whole tree comes back flat and the
// consumer has to rebuild the hierarchy itself.
//
// # A PANEL IS NOT A STOREFRONT: the flags are not applied by default
//
// [models.Category] carries two flags that hide a category from shoppers:
// is_active is the merchant's switch for one that is not ready, and is_internal
// marks one that exists for operators and was never meant to be browsable. The
// storefront's category endpoint passes PublicOnly true and therefore never
// lists either kind (see api/store.go).
//
// THIS PROVIDER DOES NOT. By default it returns every live category, switched
// off and internal ones included, and the caller narrows the set explicitly with
// public_only. The reason is the one the module already writes at
// [repository.CategoryFilter].PublicOnly and in queries/taxonomy.sql: a merchant
// who cannot SEE a category they switched off has no way to switch it back on.
// The consumer of this surface is the operator's panel, and a taxonomy dropdown
// that quietly omitted the switched-off rows would tell that operator their
// category had been deleted.
//
// The safe-looking alternative — default public_only to true, "so nothing
// leaks" — was rejected on both halves of its own argument. It does not prevent
// a leak: this surface is INSIDE the installation (ADR 0004; the storefront
// reaches the catalog through api/store.go, not through here), so there is no
// shopper on the other end to leak to. And it would make the read layer's answer
// disagree with the answer the same operator gets from the module's own admin
// listing ([Service.ListCategories] with PublicOnly false) — the same question,
// two lists, and no error to say which one is short.
//
// What makes the wide default safe rather than merely convenient is that the
// record REPORTS the two flags ([categoryRecord] offers is_active and
// is_internal): a consumer sees which rows are switched off instead of being
// handed a mixed list it cannot tell apart. A storefront-shaped consumer asks
// for public_only true and gets exactly the storefront's own set.
//
// # The id path applies the same criteria
//
// Given id/ids the categories are read in one batch and the remaining criteria
// are re-checked IN MEMORY, so the two paths answer alike. That is allowed here
// for the very reason it is refused for the catalog's taxonomy filters (see
// [productProvider.List]): parent_id, is_active and is_internal are scalar
// columns ON the record [repository.Store.ListCategoriesByIDs] returns, so the
// re-check reads data it is holding rather than a relation nobody fetched. The
// rule is one rule, not two — a criterion that cannot be answered from the
// record in hand must be refused, and these can be.
func (c *categoryProvider) List(ctx context.Context, opts query.ListOptions) ([]query.Record, error) {
	filter := repository.CategoryFilter{Limit: providerLimit(opts.Limit), Offset: opts.Offset}
	var ids []string

	for key, raw := range opts.Filters {
		switch key {
		case filterParentID:
			value, err := stringFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.ParentID = &value
		case filterPublicOnly:
			value, err := boolFilter(key, raw)
			if err != nil {
				return nil, err
			}
			filter.PublicOnly = value
		case filterID, filterIDs:
			values, err := stringsFilter(key, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, values...)
		default:
			return nil, unsupportedFilter(EntityCategory, key)
		}
	}

	categories, err := c.fetch(ctx, ids, filter)
	if err != nil {
		return nil, err
	}
	return records(categories, categoryRecord, opts.Fields, EntityCategory)
}

// fetch reads by id if an id filter was given, and by the criteria if not.
//
// The in-memory predicate is written to say the SAME thing as the SQL of
// [repository.Store.ListCategories] (see queries/taxonomy.sql): public_only
// keeps a row only while it is active AND not internal. The duplication is the
// price of the two read paths, and it is a price worth paying only because both
// halves read the same columns off the same row — the moment a criterion needs a
// second query to answer, it belongs in the refusal that the product provider's
// taxonomy filters take.
func (c *categoryProvider) fetch(
	ctx context.Context,
	ids []string,
	filter repository.CategoryFilter,
) ([]models.Category, error) {
	if len(ids) == 0 {
		return c.repo.ListCategories(ctx, filter)
	}

	categories, err := c.repo.ListCategoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]models.Category, 0, len(categories))
	for i := range categories {
		category := &categories[i]
		if filter.ParentID != nil && (category.ParentID == nil || *category.ParentID != *filter.ParentID) {
			continue
		}
		if filter.PublicOnly && (!category.IsActive || category.IsInternal) {
			continue
		}
		out = append(out, *category)
	}
	return page(out, filter.Limit, filter.Offset), nil
}

// FetchByIDs returns the category records of the given ids in a SINGLE query.
//
// An id that is not found produces no record and that is not an error (ADR
// 0004). NO flag narrowing is applied here and none should be: this is the call
// an expansion makes, the id has already been named by a link, and hiding the
// record would leave the consumer holding an identifier whose record "does not
// exist" — a dangling reference is a worse answer than a switched-off category.
//
// No link definition lands on this entity today ([Definitions] declares four and
// none of them names it), so in this installation the method is the contract's
// half rather than a hot path. It is written to be right anyway: the day a
// module links something to a category, the wrong version of this method would
// be found by the consumer, not by us.
func (c *categoryProvider) FetchByIDs(ctx context.Context, ids, fields []string) ([]query.Record, error) {
	categories, err := c.repo.ListCategoriesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return records(categories, categoryRecord, fields, EntityCategory)
}

// categoryRecord turns a category into a Query record.
//
// The keys are the JSON field names of [models.Category], for the reason
// [productRecord] states: one dictionary for the side writing the query and the
// side reading the response.
//
// is_active and is_internal are OFFERED, and that is what makes the provider's
// wide default honest rather than careless (see [categoryProvider.List]): a
// consumer that gets every category can tell which of them a shopper would see.
// Leaving them out would hand the panel a list in which a switched-off category
// looks exactly like a live one.
//
// deleted_at is NOT offered, and it is not an oversight: every read behind this
// provider filters on it, so a record that arrives here is a live one by
// construction. A field that can only ever be null is a question with one
// answer, and offering it would invite a consumer to filter on something this
// surface cannot vary.
func categoryRecord(c models.Category) query.Record {
	return query.Record{
		"id":           c.ID,
		"name":         c.Name,
		"handle":       c.Handle,
		"description":  deref(c.Description),
		"parent_id":    deref(c.ParentID),
		"is_active":    c.IsActive,
		"is_internal":  c.IsInternal,
		"rank":         c.Rank,
		fieldCreatedAt: c.CreatedAt,
		fieldUpdatedAt: c.UpdatedAt,
	}
}
