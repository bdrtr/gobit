// Package service holds the business rules of the product module.
//
// The contract of the layer: inputs are validated, ids are produced here, data
// access goes through [repository.Store] and what is returned to the outside is
// ALWAYS a core/errors typed error. Choosing the HTTP status code is not the API
// layer's job but that of the error class this layer returns.
//
// # Other modules' data
//
// Price (pricing) and stock (inventory) are NOT in this module and those modules
// are NOT IMPORTED (Principle 2.4, ADR 0001). The surfaces that are needed are
// defined in this package as narrow interfaces ([Linker], [Grapher]) and their
// concrete implementations are resolved from the container BY NAME. The store
// listing gathers price and stock over the links with the Query layer (ADR
// 0004).
package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/query"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// Linker is the NARROW surface product needs from the core's link service.
//
// Instead of the full LinkService only the three methods that are used are asked
// for: as the contract grows, the tests and the fakes of this module are not
// affected.
type Linker interface {
	// Create links fromID with toID; for the same pair it is a no-op.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete removes the link; if there is no link it is a no-op.
	Delete(ctx context.Context, name, fromID, toID string) error
	// List returns the toIDs linked to fromID.
	List(ctx context.Context, name, fromID string) ([]string, error)
}

// Grapher is the cross-module read surface (the core's Query layer).
//
// The store listing gathers the price and stock records of the variants with it;
// the pricing and inventory modules are neither imported nor known by name — the
// only thing that is known is the link names.
type Grapher interface {
	// Graph pulls the root records according to the spec and applies the expansions.
	Graph(ctx context.Context, spec query.GraphSpec) ([]query.Record, error)
}

// UploadReader is the NARROW surface product needs from the file module.
//
// The file module CANNOT be imported (Principle 2.4, and depguard enforces it),
// so the surface is declared here and satisfied STRUCTURALLY by whatever the
// container holds under the file module's interop name — the pattern of ADR
// 0001/0006, the same one the order module uses for the b2b spending rule.
//
// The record travels as JSON because its SHAPE belongs to the file module: it
// declares the fields, and naming a type here would either duplicate that shape
// or force an import. This module does not read the body at all; it only needs
// to know whether the record is there (see [Service.verifyImageUploads]). The
// callers that want the file behind an image decode it themselves.
type UploadReader interface {
	// UploadJSON returns the upload record as JSON, errors.NotFound if the id
	// belongs to no upload.
	//
	// A NIL body WITH A NIL ERROR is the third answer and it means "I cannot
	// answer": the file module is not installed in this setup. It is separate
	// from NotFound on purpose — "there is no such upload" is a fact about the
	// id, "there is no file module" is a fact about the installation, and
	// treating the second as the first would reject ids that are perfectly
	// good.
	UploadJSON(ctx context.Context, uploadID string) (json.RawMessage, error)
}

// EventPublisher is the NARROW surface the service needs from the event bus.
//
// core/eventbus is CORE and importing it is free (Principle 2.4); the narrowing
// here is there to reduce the dependency: the catalog only PUBLISHES, it does
// not subscribe and it does not close the bus. Binding to the whole of
// [eventbus.EventBus] would give the impression that the module has the
// authority to subscribe and to shut down.
//
// The [eventbus.Event] type is used as it is: the shape of the event is the
// core's contract and redefining it here would lead the two types to drift
// apart.
type EventPublisher interface {
	// Publish publishes the event and does NOT WAIT for the handlers.
	Publish(ctx context.Context, e eventbus.Event) error
}

// Options holds the dependencies of the service setup.
type Options struct {
	// Repo is required.
	Repo repository.Store
	// Links is needed for the endpoints that use the link definitions; if nil is
	// given, the link endpoints return a typed "not ready" error.
	Links Linker
	// Query is there for the price/stock expansion of the store listing; if nil
	// is given, the listing works without prices and without stock.
	Query Grapher
	// Uploads is the file module's read-back; if nil is given, an image's
	// upload id is recorded WITHOUT being verified (see
	// [Service.verifyImageUploads]).
	Uploads UploadReader
	// Events is the bus the catalog events are published on; if nil is given, the
	// events are silently skipped (rationale: [Service.publishProductEvent]).
	Events EventPublisher
	// Logger, if nil is given, means the logs are discarded.
	Logger *slog.Logger
}

// Service is the public service of the product module.
//
// It is registered in the container under the name "product.service". All of its
// methods are goroutine-safe (they hold no state; the state is in the database).
type Service struct {
	repo    repository.Store
	links   Linker
	graph   Grapher
	uploads UploadReader
	events  EventPublisher
	log     *slog.Logger
}

// New builds the service with the given dependencies.
//
// If no repo is given it returns an error: a catalog service without a
// repository would blow up on every call, and that has to be seen at setup time.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Invalid(codeNotReady,
			"the product service cannot be built without a repository")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Service{
		repo:    opts.Repo,
		links:   opts.Links,
		graph:   opts.Query,
		uploads: opts.Uploads,
		events:  opts.Events,
		log:     log,
	}, nil
}

// ListResult is the result of a paginated list.
type ListResult[T any] struct {
	Items []T
	// Count is the total number of records, INDEPENDENT of limit/offset; it is the
	// source of the "count" field in the API envelope.
	//
	// It is A POINTER and nil means "NOT COUNTED" — it does NOT mean "zero
	// records". The count is optional (see [ListProductsOptions.SkipCount]) and
	// when it is skipped a plain int field would carry 0; 0 is a lie here, because
	// it cannot be told apart from the sentence "no matching records". Moving the
	// distinction into the type FORCES the caller to ask the question it has to ask
	// before reading the number: using a nil like a plain number does not compile.
	Count  *int
	Offset int
	Limit  int
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	//
	// It is empty rather than "the key of the last row" when there is nothing
	// more, and the difference is the point: a cursor that always came back
	// would make a client walk one extra request into an empty page before it
	// could tell it was done.
	NextCursor string
}

// CreateProductInput is the input of a new product.
//
// If Handle is left empty it is derived from the title. If the
// Options/Variants/Images fields are filled, the product and its child records
// are written IN A SINGLE TRANSACTION: a half-finished catalog record (a product
// without variants, or an ownerless variant) does not come into being.
type CreateProductInput struct {
	Handle        string
	Title         string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        models.Status
	IsGiftcard    bool
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
	Options       []CreateOptionInput
	Variants      []CreateVariantInput
	Images        []CreateImageInput
	TagIDs        []string
	CategoryIDs   []string
}

// CreateImageInput is the input of an image to be added to a product.
type CreateImageInput struct {
	URL string
	// UploadID is the id of the upload this image was made from; it may be left
	// EMPTY when the address was not uploaded through this installation.
	//
	// It is a plain string rather than a pointer on purpose: an image is
	// CREATED here, not patched, so there is no "not given" to tell apart from
	// "cleared" — both mean the same thing, and the empty string says it
	// without a second nil for the caller to reason about.
	UploadID string
	Rank     int32
	Metadata map[string]any
}

// UpdateProductInput is a partial update of a product.
//
// The PATCH contract: a field left nil DOES NOT CHANGE. Pulling a field to NULL
// cannot be done through this endpoint; that is the accepted price of the fact
// that the distinction between "not given" and "empty it" cannot be expressed
// with a single null in JSON.
type UpdateProductInput struct {
	Handle        *string
	Title         *string
	Subtitle      *string
	Description   *string
	Thumbnail     *string
	Status        *models.Status
	Discountable  *bool
	Weight        *int32
	Length        *int32
	Height        *int32
	Width         *int32
	Material      *string
	OriginCountry *string
	CollectionID  *string
	Metadata      map[string]any
	TagIDs        []string
	CategoryIDs   []string
}

// ProductListing names this listing inside a cursor.
//
// A cursor carries the name of the listing it belongs to so that one handed to
// a different listing is REFUSED rather than silently selecting the wrong rows
// out of a key space it does not describe.
const ProductListing = "products"

// ListProductsOptions is the set of criteria of the product listing.
type ListProductsOptions struct {
	Status       *models.Status
	CollectionID *string
	Handle       *string
	Search       *string
	// CategoryID and TagID narrow the listing to a category or a tag. A product
	// may belong to several of either and is still returned ONCE.
	CategoryID *string
	TagID      *string
	// SalesChannelIDs is the sales channel filter; for its meaning and the
	// nil/empty distinction see [StoreListOptions.SalesChannelIDs].
	//
	// The admin listing DOES NOT FILL it: an admin identity has no sales channel
	// and it has to see the catalog as a whole.
	SalesChannelIDs []string
	Limit           int
	Offset          int
	// After is the opaque position from a previous page's NextCursor; the zero
	// value is the first page.
	//
	// It is what makes a deep page cheap. Offset makes the database walk and
	// DISCARD every row it skips, so its cost grows with depth — measured on
	// this table at 52,000 rows: 0.31 ms for the first page, 34.71 ms for the
	// last. The same last page reached by cursor is 0.08 ms, because the
	// ordering key goes into the index condition instead of into a counter.
	After corepage.Cursor
	// WithRelations true means the variants, options, images, tags and categories
	// are filled with BULK queries (no query is made per product).
	WithRelations bool
	// SkipCount true means the total count query is NOT RUN AT ALL and the
	// [ListResult.Count] field of the result comes back nil.
	//
	// # Why it exists
	//
	// Independently of the page size, the count has to walk THE WHOLE filtered
	// SET, and on a large catalog it is the entire cost of the request.
	// Measured (gobit_load: 52,004 products, 52,000 channel assignments, LIMIT
	// 20, sales channel filter on; median of 15 calls, from the Go side):
	//
	//	repo.ListProducts                      0.26 ms
	//	repo.CountProducts                    64.07 ms
	//	ListProducts (list + count + links)   67.00 ms
	//	ListProducts (with SkipCount)          0.65 ms
	//
	// That is, 99% of the request's SQL is the count, and the cost grows with
	// the catalog. The plan of the count is to walk the WHOLE product table and
	// do an index probe into the link table per row (EXPLAIN: Seq Scan on
	// product, 52,004 rows, SubPlan 52,004 loops, 156,013 buffers).
	//
	// # MAKING THE COUNT CHEAPER was tried first
	//
	// Three separate SQL forms counting the same set were measured with the same
	// data (psql, prepared statement, warmed up):
	//
	//	                           no filter   with q (single match)
	//	correlated (today's)        62-71 ms                 13.8 ms
	//	two EXISTS (hash)           43-54 ms                       —
	//	GROUP BY + hash join        33-45 ms                 30.0 ms
	//
	// The hash form is twice as fast without a filter but twice as slow with a
	// SELECTIVE filter: hashing the whole link table lays down a constant 30 ms
	// floor, while the correlated form probes only for the rows that survive.
	// What is more, the list query MUST be correlated (its ability to stop at
	// the LIMIT comes from that; measured: 26.8 ms -> 0.8 ms) and the rule lives
	// in a SINGLE template (see repository/saleschannel.go). Splitting the form
	// would be a second definition of the visibility rule.
	//
	// What remains true is this: counting is O(catalog). No form makes it
	// sublinear, because the answer to the question "how many" cannot be known
	// without looking at the whole set. So the solution is not to speed the
	// count up, but NOT TO ASK IT AT ALL WHEN IT IS NOT WANTED.
	//
	// # Why the flag is written NEGATIVELY
	//
	// The zero value has to be TODAY'S behavior. Had the field been "WithCount
	// bool", EVERY caller that uses this type and does not know about the field
	// would silently stop counting and the number in its envelope would
	// disappear — the compiler does not count a missing field in a struct
	// literal as an error. A negative flag is ugly, but it is not silent.
	SkipCount bool
}

// CreateProduct creates a product and returns it together with its child
// records.
//
// The handle is unique: if it is in use, errors.Conflict is returned. The
// conflict is caught in two layers at once — here with a readable message, in
// the database with a partial unique index. The second one is the only real
// guarantee that two concurrent requests cannot slip past each other.
func (s *Service) CreateProduct(ctx context.Context, in CreateProductInput) (models.Product, error) {
	product, err := s.buildProduct(in)
	if err != nil {
		return models.Product{}, err
	}
	if err := s.ensureHandleFree(ctx, product.Handle, ""); err != nil {
		return models.Product{}, err
	}

	options, err := buildOptions(product.ID, in.Options)
	if err != nil {
		return models.Product{}, err
	}
	images, err := buildImages(product.ID, in.Images)
	if err != nil {
		return models.Product{}, err
	}
	tagIDs, err := uniqueIDs("tag_ids", in.TagIDs)
	if err != nil {
		return models.Product{}, err
	}
	categoryIDs, err := uniqueIDs("category_ids", in.CategoryIDs)
	if err != nil {
		return models.Product{}, err
	}

	// The upload half of the images is settled BEFORE the transaction opens:
	// first that the uploads exist, then the bindings themselves. Both can
	// fail, and failing here means the create returns with NOTHING written —
	// which is the whole reason they run in this order (see
	// [Service.linkImageUploads]).
	if err := s.verifyImageUploads(ctx, images); err != nil {
		return models.Product{}, err
	}
	if err := s.linkImageUploads(ctx, images); err != nil {
		return models.Product{}, err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.CreateProduct(ctx, product); err != nil {
			return err
		}
		if _, err := writeOptions(ctx, tx, options); err != nil {
			return err
		}
		// Walked BY INDEX: an image record is a large struct and copying one
		// per iteration is a cost the linter counts (gocritic rangeValCopy).
		for i := range images {
			if _, err := tx.CreateImage(ctx, images[i]); err != nil {
				return err
			}
		}
		if len(tagIDs) > 0 {
			if err := tx.SetProductTags(ctx, product.ID, tagIDs); err != nil {
				return err
			}
		}
		if len(categoryIDs) > 0 {
			if err := tx.SetProductCategories(ctx, product.ID, categoryIDs); err != nil {
				return err
			}
		}
		for i, v := range in.Variants {
			if _, err := createVariantTx(ctx, tx, product.ID, v, int32From(i)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return models.Product{}, err
	}

	created, err := s.GetProduct(ctx, product.ID)
	if err != nil {
		return models.Product{}, err
	}
	s.publishProductEvent(ctx, EventProductCreated, created.ID, created.Status)
	return created, nil
}

// GetProduct returns the product together with its related records.
//
// A deleted product counts as NOT FOUND (errors.NotFound); that is the read-side
// counterpart of the soft delete.
func (s *Service) GetProduct(ctx context.Context, id string) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}

	product, err := s.repo.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}

	products := []models.Product{product}
	if err := s.attachRelations(ctx, products); err != nil {
		return models.Product{}, err
	}
	return products[0], nil
}

// GetProductByHandle returns the product by its handle.
func (s *Service) GetProductByHandle(ctx context.Context, handle string) (models.Product, error) {
	if _, err := requireID("handle", handle); err != nil {
		return models.Product{}, err
	}

	product, err := s.repo.GetProductByHandle(ctx, handle)
	if err != nil {
		return models.Product{}, err
	}

	products := []models.Product{product}
	if err := s.attachRelations(ctx, products); err != nil {
		return models.Product{}, err
	}
	return products[0], nil
}

// ListProducts returns the products matching the criteria, paginated.
//
// The total count CAN BE TURNED OFF with [ListProductsOptions.SkipCount]; while
// it is off the count query is not run at all and [ListResult.Count] comes back
// nil. The rationale and the measurement are in the doc of that field.
func (s *Service) ListProducts(ctx context.Context, opts ListProductsOptions) (ListResult[models.Product], error) {
	limit, offset, err := normalizePaging(opts.Limit, opts.Offset)
	if err != nil {
		return ListResult[models.Product]{}, err
	}

	filter := repository.ProductFilter{
		CollectionID:    opts.CollectionID,
		Handle:          opts.Handle,
		Search:          opts.Search,
		CategoryID:      opts.CategoryID,
		TagID:           opts.TagID,
		SalesChannelIDs: opts.SalesChannelIDs,
		Limit:           limit,
		Offset:          offset,
		After:           opts.After,
	}
	if opts.Status != nil {
		status, err := normalizeStatus(*opts.Status)
		if err != nil {
			return ListResult[models.Product]{}, err
		}
		value := status.String()
		filter.Status = &value
	}

	// One row MORE than asked for is fetched, and the extra one is dropped
	// below. That is how "is there a next page" is answered without a second
	// query and without a count: if the extra row came back, there is more.
	filter.Limit = limit + 1

	products, err := s.repo.ListProducts(ctx, filter)
	if err != nil {
		return ListResult[models.Product]{}, err
	}

	nextCursor := ""
	if len(products) > limit {
		products = products[:limit]
		last := products[len(products)-1]
		nextCursor = corepage.Encode(ProductListing, corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	var count *int

	if !opts.SkipCount {
		// The count query ignores limit and offset, so the +1 above does not
		// reach it; passing the same filter keeps the two in step on everything
		// that DOES matter to it.
		n, err := s.repo.CountProducts(ctx, filter)
		if err != nil {
			return ListResult[models.Product]{}, err
		}

		count = &n
	}

	if opts.WithRelations {
		if err := s.attachRelations(ctx, products); err != nil {
			return ListResult[models.Product]{}, err
		}
	}

	return ListResult[models.Product]{
		Items:      products,
		Count:      count,
		Offset:     offset,
		Limit:      limit,
		NextCursor: nextCursor,
	}, nil
}

// UpdateProduct updates the product partially.
func (s *Service) UpdateProduct(ctx context.Context, id string, in UpdateProductInput) (models.Product, error) {
	if _, err := requireID("id", id); err != nil {
		return models.Product{}, err
	}

	patch, err := buildProductPatch(in)
	if err != nil {
		return models.Product{}, err
	}
	if patch.Handle != nil {
		if err := s.ensureHandleFree(ctx, *patch.Handle, id); err != nil {
			return models.Product{}, err
		}
	}

	tagIDs, err := uniqueIDs("tag_ids", in.TagIDs)
	if err != nil {
		return models.Product{}, err
	}
	categoryIDs, err := uniqueIDs("category_ids", in.CategoryIDs)
	if err != nil {
		return models.Product{}, err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if _, err := tx.UpdateProduct(ctx, id, patch); err != nil {
			return err
		}
		// A nil slice means "do not touch", an empty slice means "remove them all";
		// the distinction can only be preserved with the nil of a slice that is not
		// behind a pointer.
		if in.TagIDs != nil {
			if err := tx.SetProductTags(ctx, id, tagIDs); err != nil {
				return err
			}
		}
		if in.CategoryIDs != nil {
			if err := tx.SetProductCategories(ctx, id, categoryIDs); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return models.Product{}, err
	}

	updated, err := s.GetProduct(ctx, id)
	if err != nil {
		return models.Product{}, err
	}
	s.publishProductEvent(ctx, EventProductUpdated, updated.ID, updated.Status)
	return updated, nil
}

// DeleteProduct SOFT deletes the product and its child records.
//
// The price/stock links of the variants, the sales channel links of the product
// and the upload links of its images are cleaned up as well: if the link of a
// deleted record stayed behind, the queries of other modules would end up at a
// record that does not exist. The link cleanup is OUTSIDE the database
// transaction (the link tables belong to the core); that is why its failure does
// not undo the deletion, it is logged as a warning and the orphan links are
// harmless — ids are never reused.
func (s *Service) DeleteProduct(ctx context.Context, id string) error {
	if _, err := requireID("id", id); err != nil {
		return err
	}

	variantIDs, err := s.repo.ListVariantIDsByProduct(ctx, id)
	if err != nil {
		return err
	}
	// The images are read BEFORE the deletion for the same reason the variant
	// ids are: after the soft delete the listing filters them out, and their
	// upload ids — the far end of the bindings to be removed — would be
	// unreachable.
	images, err := s.imagesForLinkCleanup(ctx, id)
	if err != nil {
		return err
	}

	err = s.repo.InTx(ctx, func(ctx context.Context, tx repository.Store) error {
		if err := tx.SoftDeleteProduct(ctx, id); err != nil {
			return err
		}
		return tx.SoftDeleteProductChildren(ctx, id)
	})
	if err != nil {
		return err
	}

	for _, variantID := range variantIDs {
		s.cleanupVariantLinks(ctx, variantID)
	}
	s.cleanupProductSalesChannels(ctx, id)
	s.cleanupImageUploadLinks(ctx, images)

	// The event is published AFTER the link cleanup: in an ordering that calls the
	// subscriber to read the links of the record, a subscriber arriving while the
	// cleanup was half done would still see the links of the deleted product.
	s.publishProductEvent(ctx, EventProductDeleted, id, "")
	return nil
}

// buildProduct validates the input and builds the product model to be written.
func (s *Service) buildProduct(in CreateProductInput) (models.Product, error) {
	title, err := requireText("title", in.Title, maxTitleLen)
	if err != nil {
		return models.Product{}, err
	}
	handle, err := resolveHandle(in.Handle, title)
	if err != nil {
		return models.Product{}, err
	}
	status, err := normalizeStatus(in.Status)
	if err != nil {
		return models.Product{}, err
	}

	subtitle, err := trimOptional(in.Subtitle, "subtitle", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}
	description, err := trimOptional(in.Description, "description", maxDescriptionLen)
	if err != nil {
		return models.Product{}, err
	}
	thumbnail, err := trimOptional(in.Thumbnail, "thumbnail", maxURLLen)
	if err != nil {
		return models.Product{}, err
	}
	material, err := trimOptional(in.Material, "material", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}
	originCountry, err := trimOptional(in.OriginCountry, "origin_country", maxValueLen)
	if err != nil {
		return models.Product{}, err
	}

	discountable := true
	if in.Discountable != nil {
		discountable = *in.Discountable
	}

	return models.Product{
		ID:            newID(prefixProduct),
		Handle:        handle,
		Title:         title,
		Subtitle:      subtitle,
		Description:   description,
		Thumbnail:     thumbnail,
		Status:        status,
		IsGiftcard:    in.IsGiftcard,
		Discountable:  discountable,
		Weight:        in.Weight,
		Length:        in.Length,
		Height:        in.Height,
		Width:         in.Width,
		Material:      material,
		OriginCountry: originCountry,
		CollectionID:  in.CollectionID,
		Metadata:      in.Metadata,
	}, nil
}

// buildProductPatch validates the update input and turns it into a repository patch.
func buildProductPatch(in UpdateProductInput) (repository.ProductPatch, error) {
	patch := repository.ProductPatch{
		Discountable:  in.Discountable,
		Weight:        in.Weight,
		Length:        in.Length,
		Height:        in.Height,
		Width:         in.Width,
		Material:      in.Material,
		OriginCountry: in.OriginCountry,
		CollectionID:  in.CollectionID,
		Metadata:      in.Metadata,
	}

	if in.Title != nil {
		title, err := requireText("title", *in.Title, maxTitleLen)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		patch.Title = &title
	}
	if in.Handle != nil {
		handle, err := validateHandle(*in.Handle)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		patch.Handle = &handle
	}
	if in.Status != nil {
		status, err := normalizeStatus(*in.Status)
		if err != nil {
			return repository.ProductPatch{}, err
		}
		value := status.String()
		patch.Status = &value
	}

	subtitle, err := trimOptional(in.Subtitle, "subtitle", maxValueLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	description, err := trimOptional(in.Description, "description", maxDescriptionLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	thumbnail, err := trimOptional(in.Thumbnail, "thumbnail", maxURLLen)
	if err != nil {
		return repository.ProductPatch{}, err
	}
	patch.Subtitle = subtitle
	patch.Description = description
	patch.Thumbnail = thumbnail

	return patch, nil
}

// buildImages validates the image inputs and turns them into models.
func buildImages(productID string, in []CreateImageInput) ([]models.Image, error) {
	out := make([]models.Image, 0, len(in))
	for i, img := range in {
		url, err := requireText("images[].url", img.URL, maxURLLen)
		if err != nil {
			return nil, err
		}
		// The URL stays MANDATORY even when an upload is named. It is what the
		// storefront renders, and deriving it from the upload would put a
		// second address rule in this module — one that would go wrong the day
		// the file module starts signing addresses. Whoever uploads the file
		// gets the address in the same response as the id and sends both.
		uploadID, err := optionalUploadID(img.UploadID)
		if err != nil {
			return nil, err
		}
		rank := img.Rank
		if rank == 0 {
			// If no rank was given the submission order is preserved; otherwise all the
			// images would stay at the same rank and the listing would look random.
			rank = int32From(i)
		}
		out = append(out, models.Image{
			ID:        newID(prefixImage),
			ProductID: productID,
			URL:       url,
			UploadID:  uploadID,
			Rank:      rank,
			Metadata:  img.Metadata,
		})
	}
	return out, nil
}

// optionalUploadID validates an image's upload id and turns it into the
// nullable form the record carries.
//
// An empty id is not an error: it is the answer "this image was not made from
// an upload of ours". A NON-empty one goes through [requireID], which does NOT
// trim — an id with a stray space would land on one row in the image table and
// on a different one in the link table, so the corruption is rejected instead
// of silently corrected.
func optionalUploadID(value string) (*string, error) {
	if value == "" {
		return nil, nil
	}
	id, err := requireID("images[].upload_id", value)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// ensureHandleFree verifies that the handle is not used by another product.
//
// exceptID is the id of the product being updated; its own handle does not count
// as a conflict.
func (s *Service) ensureHandleFree(ctx context.Context, handle, exceptID string) error {
	existing, err := s.repo.GetProductByHandle(ctx, handle)
	switch {
	case err == nil:
		if existing.ID == exceptID {
			return nil
		}
		return errors.Conflict(codeHandleTaken,
			"the handle is already in use: %q (product: %s)", handle, existing.ID)
	case errors.IsNotFound(err):
		return nil
	default:
		return err
	}
}

// attachRelations fills the related records of the given products IN BULK.
//
// The number of queries is INDEPENDENT of the number of products: no matter how
// many products there are, a fixed number of queries is made for the variants,
// options, option values, variant-value links, images, tags and categories. A
// query per product would mean N+1.
func (s *Service) attachRelations(ctx context.Context, products []models.Product) error {
	if len(products) == 0 {
		return nil
	}
	ids := make([]string, 0, len(products))
	for i := range products {
		ids = append(ids, products[i].ID)
	}

	variants, err := s.repo.ListVariantsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	if err := s.attachVariantOptionValues(ctx, variants); err != nil {
		return err
	}

	options, err := s.repo.ListOptionsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	options, err = s.attachOptionValues(ctx, options)
	if err != nil {
		return err
	}

	images, err := s.repo.ListImagesByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	tags, err := s.repo.ListTagsByProductIDs(ctx, ids)
	if err != nil {
		return err
	}
	categories, err := s.repo.ListCategoriesByProductIDs(ctx, ids)
	if err != nil {
		return err
	}

	variantsByProduct := groupBy(variants, func(v models.Variant) string { return v.ProductID })
	optionsByProduct := groupBy(options, func(o models.Option) string { return o.ProductID })

	for i := range products {
		id := products[i].ID
		products[i].Variants = variantsByProduct[id]
		products[i].Options = optionsByProduct[id]
		products[i].Images = images[id]
		products[i].Tags = tags[id]
		products[i].Categories = categories[id]
	}
	return nil
}

// attachVariantOptionValues fills the option values of the variants in a SINGLE query.
func (s *Service) attachVariantOptionValues(ctx context.Context, variants []models.Variant) error {
	if len(variants) == 0 {
		return nil
	}
	ids := make([]string, 0, len(variants))
	for i := range variants {
		ids = append(ids, variants[i].ID)
	}

	values, err := s.repo.ListVariantOptionValues(ctx, ids)
	if err != nil {
		return err
	}
	for i := range variants {
		variants[i].OptionValues = values[variants[i].ID]
	}
	return nil
}

// attachOptionValues fills the values of the options in a SINGLE query.
func (s *Service) attachOptionValues(ctx context.Context, options []models.Option) ([]models.Option, error) {
	if len(options) == 0 {
		return options, nil
	}
	ids := make([]string, 0, len(options))
	for i := range options {
		ids = append(ids, options[i].ID)
	}

	values, err := s.repo.ListOptionValuesByOptionIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byOption := groupBy(values, func(v models.OptionValue) string { return v.OptionID })
	for i := range options {
		options[i].Values = byOption[options[i].ID]
	}
	return options, nil
}

// groupBy groups the slice by the given key; the order is preserved.
func groupBy[T any](items []T, key func(T) string) map[string][]T {
	out := make(map[string][]T, len(items))
	for _, item := range items {
		k := key(item)
		out[k] = append(out[k], item)
	}
	return out
}
