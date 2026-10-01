package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// TestThePanelStoresAFile is ADR 0325: the surface stores a body of an
// allowed type, returns its id and address, serves the address again by id,
// tells the panel the module's bound, refuses a type off the allow list and a
// body over the bound, and removes an upload.
func TestThePanelStoresAFile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc := newService(t, newFakeStore(), &fakeProvider{id: "fake"})
	surface := service.NewAdminSurface(svc)
	assert.Equal(t, testMaxBytes, surface.MaxUploadBytes())

	id, url, err := surface.UploadFile(ctx, coreprovider.ContentTypePNG, "dent.png", "usr_claims", strings.NewReader("png"))
	require.NoError(t, err)
	require.NotEmpty(t, id)
	served, err := surface.UploadURL(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, url, served, "the address the upload is served at")
	record, err := svc.GetUpload(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "usr_claims|dent.png|"+coreprovider.ContentTypePNG,
		record.UploadedBy+"|"+record.OriginalName+"|"+record.ContentType, "who sent it, its name and its type")

	_, _, err = surface.UploadFile(ctx, "text/html", "page.html", "usr_claims", strings.NewReader("<p>"))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a type off the allow list: %v", err)
	_, _, err = surface.UploadFile(ctx, coreprovider.ContentTypePNG, "huge.png", "usr_claims",
		strings.NewReader(strings.Repeat("x", int(testMaxBytes)+1)))
	require.Error(t, err)
	assert.True(t, errors.IsInvalid(err), "a body over the bound: %v", err)

	require.NoError(t, surface.DeleteUpload(ctx, id))
	_, err = surface.UploadURL(ctx, id)
	assert.True(t, errors.IsNotFound(err), "a removed upload is gone: %v", err)
}
