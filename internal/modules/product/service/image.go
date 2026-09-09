package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/product/models"
	"github.com/bdrtr/gobit/internal/modules/product/repository"
)

// UpdateImageInput is a partial update of one product image.
//
// # The address is NOT here
//
// An image's `url` and its upload binding were written together, and the reverse
// read of that binding is what an operator uses to decide whether a file is
// still in use. Letting the address move on its own would put the row's own
// column at odds with the link record — the same disagreement the module already
// refuses to open a "bind this image to that upload" endpoint for (the reason is
// written on that route in the api package). Replacing the picture is a new
// image and the
// removal of the old one, which is two calls that say what they did.
type UpdateImageInput struct {
	// AltText is what the picture shows, for a reader who cannot see it. An
	// empty STRING is a value and not an absence: it is HTML's own word for a
	// decorative image, so passing "" clears the text on purpose.
	AltText *string
	// Rank is the image's position among the product's images.
	Rank *int32
	// Metadata is the caller's free-form extra data.
	Metadata map[string]any
}

// AddProductImage adds one image to a product that already exists.
//
// Until this existed a product's images were fixed at creation: [CreateProduct]
// took them and nothing else ever wrote the table, so a picture added later
// meant deleting the product.
//
// # The rank defaults to LAST
//
// A zero rank means "not given", as it does in [CreateProductInput.Images], and
// the value it turns into is one past the highest rank the product already
// carries. Zero itself would put the new picture FIRST, which is the one
// position nobody means by "add an image".
func (s *Service) AddProductImage(
	ctx context.Context, productID string, in CreateImageInput,
) (models.Image, error) {
	id, err := requireID("product_id", productID)
	if err != nil {
		return models.Image{}, err
	}
	if _, err := s.repo.GetProduct(ctx, id); err != nil {
		return models.Image{}, err
	}

	built, err := buildImages(id, []CreateImageInput{in})
	if err != nil {
		return models.Image{}, err
	}
	image := built[0]

	if in.Rank == 0 {
		next, err := s.nextImageRank(ctx, id)
		if err != nil {
			return models.Image{}, err
		}
		image.Rank = next
	}

	// The order is the create path's and for the create path's reason: the
	// binding goes in first, so the only residue a failure can leave is a link
	// row whose image was never written (see [Service.linkImageUploads]).
	if err := s.verifyImageUploads(ctx, built); err != nil {
		return models.Image{}, err
	}
	if err := s.linkImageUploads(ctx, built); err != nil {
		return models.Image{}, err
	}

	return s.repo.CreateImage(ctx, image)
}

// nextImageRank returns one past the highest rank the product's images carry.
func (s *Service) nextImageRank(ctx context.Context, productID string) (int32, error) {
	byProduct, err := s.repo.ListImagesByProductIDs(ctx, []string{productID})
	if err != nil {
		return 0, err
	}

	images := byProduct[productID]
	var highest int32

	for i := range images {
		if images[i].Rank > highest {
			highest = images[i].Rank
		}
	}
	if len(images) == 0 {
		return 0, nil
	}

	return highest + 1, nil
}

// UpdateProductImage patches one image of a product.
//
// This is what closes the gap ADR 0104 left open: `alt_text` was published and
// could not be CORRECTED, so a wrong description of a picture was permanent for
// as long as the product was.
//
// Both identifiers reach the query. Reading only the image's would let a caller
// edit another product's picture by naming a product of their own, and the
// answer would look like a success.
func (s *Service) UpdateProductImage(
	ctx context.Context, productID, imageID string, in UpdateImageInput,
) (models.Image, error) {
	product, err := requireID("product_id", productID)
	if err != nil {
		return models.Image{}, err
	}
	image, err := requireID("image_id", imageID)
	if err != nil {
		return models.Image{}, err
	}

	patch := repository.ImagePatch{Rank: in.Rank, Metadata: in.Metadata}
	if in.AltText != nil {
		// The text is trimmed for [CreateImageInput.AltText]'s reason: an alt
		// text made of one space is a description somebody thinks they wrote.
		trimmed := strings.TrimSpace(*in.AltText)
		if len(trimmed) > maxAltTextLen {
			return models.Image{}, invalid("alt_text can be at most %d characters (given: %d)",
				maxAltTextLen, len(trimmed))
		}
		patch.AltText = &trimmed
	}
	if patch.AltText == nil && patch.Rank == nil && patch.Metadata == nil {
		return models.Image{}, invalid(
			"no field was given to update: alt_text, rank or metadata is required")
	}

	return s.repo.UpdateImage(ctx, product, image, patch)
}

// RemoveProductImage removes one image from a product.
//
// The upload binding goes with it, and the FILE does not: it belongs to the file
// module and may back another product's image. What the cleanup protects is the
// reverse read — an image no storefront shows any more must not keep answering
// "yes, this file is in use" to an operator deciding whether to delete it.
func (s *Service) RemoveProductImage(ctx context.Context, productID, imageID string) error {
	product, err := requireID("product_id", productID)
	if err != nil {
		return err
	}
	image, err := requireID("image_id", imageID)
	if err != nil {
		return err
	}

	// The record is read BEFORE the deletion, because the binding it names is
	// what the cleanup needs and a deleted row cannot be asked.
	existing, err := s.repo.GetImageOfProduct(ctx, product, image)
	if err != nil {
		return err
	}
	if err := s.repo.SoftDeleteImage(ctx, product, image); err != nil {
		return err
	}
	s.cleanupImageUploadLinks(ctx, []models.Image{existing})

	return nil
}
