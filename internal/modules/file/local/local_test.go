package local_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/local"
)

// pngContent is test content carrying a valid PNG signature.
//
// A real signature is used because the provider derives the key's extension from
// the content type; a made-up string would make the test's extension claim
// meaningless.
var pngContent = append([]byte("\x89PNG\r\n\x1a\n"), []byte("body")...)

// newProvider produces a provider working over a temporary root directory.
//
// [testing.T.TempDir] gives a separate directory for every test and deletes it
// afterwards; the tests do not see each other's files.
func newProvider(t *testing.T) (prov *local.Provider, root string) {
	t.Helper()

	root = t.TempDir()

	prov, err := local.New(local.Options{Root: root})
	require.NoError(t, err)

	return prov, root
}

// rootEntries returns the file names in the root directory.
func rootEntries(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(root)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	return names
}

// TestAnEmptyRootIsRejectedAndTheTEMPDIRIsNOTFallenBackTo verifies that the
// setup does not silently slide into the temporary directory.
//
// The temporary directory is the most tempting answer to the "let it work
// without configuring anything" wish, and that is exactly why it is tested: had
// it been written, all the images would be lost on a restart, the addresses in
// the product records would stay in place and no error would be visible.
func TestAnEmptyRootIsRejectedAndTheTEMPDIRIsNOTFallenBackTo(t *testing.T) {
	t.Parallel()

	prov, err := local.New(local.Options{})

	require.Error(t, err, "the provider must not be buildable without a root directory")
	assert.Nil(t, prov)
	assert.Equal(t, local.CodeNotReady, coreerrors.CodeOf(err))
	assert.NotContains(t, err.Error(), os.TempDir(),
		"the temporary directory must not even be suggested as an alternative")
}

// TestTheRootDirectoryIsCreatedAtStartup verifies that a writable root is
// prepared at setup time.
//
// Had it been deferred to the first upload, a misspelled path would surface only
// when a customer tried to upload a file — whereas the only thing that can be
// corrected at that moment is the startup configuration.
func TestTheRootDirectoryIsCreatedAtStartup(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "not", "yet")

	prov, err := local.New(local.Options{Root: root})

	require.NoError(t, err)
	assert.Equal(t, root, prov.Root())

	info, statErr := os.Stat(root)
	require.NoError(t, statErr, "the root directory must be created during setup")
	assert.True(t, info.IsDir())
}

// TestTheProducedKeyCONTAINSNOPATH verifies that the storage key is a single
// file name.
//
// The claim is the STRUCTURAL protection against path traversal itself: the key
// is a name on a single plane, it carries no path separator and, joined with the
// root, it cannot come out from under the root. In the input there is anyway no
// such thing as a client file name field — that is, there is no value that would
// need "sanitizing" either.
func TestTheProducedKeyCONTAINSNOPATH(t *testing.T) {
	t.Parallel()

	prov, root := newProvider(t)

	file, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})

	require.NoError(t, err)
	assert.Equal(t, filepath.Base(file.Key), file.Key, "the key is not a PATH, it is a single name")
	assert.NotContains(t, file.Key, "/")
	assert.NotContains(t, file.Key, "..")
	assert.True(t, strings.HasSuffix(file.Key, ".png"),
		"the extension must derive from the detected type: %s", file.Key)
	assert.Equal(t, local.DefaultURLPrefix+"/"+file.Key, file.URL)
	assert.Equal(t, int64(len(pngContent)), file.Size)

	written, readErr := os.ReadFile(filepath.Join(root, file.Key))
	require.NoError(t, readErr)
	assert.Equal(t, pngContent, written)
}

// TestTwoUploadsGetSeparateKeys verifies that keys are not reused.
//
// A reused key would break two things at once: the uniqueness constraint in the
// ledger would be violated and — far worse — a published address would one day
// start showing ANOTHER image. The "immutable" cache header on the serving path
// rests on exactly this claim too.
func TestTwoUploadsGetSeparateKeys(t *testing.T) {
	t.Parallel()

	prov, _ := newProvider(t)
	ctx := context.Background()

	first, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})
	require.NoError(t, err)

	second, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})
	require.NoError(t, err)

	assert.NotEqual(t, first.Key, second.Key)
}

// cutOffReader is a reader that returns an error AFTER giving a few bytes.
//
// The moment the size bound is exceeded looks exactly like this: the body has
// started being read and the read is cut off in the middle.
type cutOffReader struct {
	data []byte
	err  error
	read bool
}

// Read satisfies io.Reader.
func (c *cutOffReader) Read(p []byte) (int, error) {
	if c.read {
		return 0, c.err
	}
	c.read = true

	n := copy(p, c.data)

	return n, nil
}

// TestNoHALFFILEIsLeftWhenTheReadIsCutOffHalfway verifies that a body exceeding
// the bound leaves no trace on the disk.
//
// It is a requirement of the core contract and its reasoning is concrete: a half
// object is a file that no record points at and whose key no delete path knows.
// Had it not been cleaned up, requests exceeding the size bound could fill the
// disk up even though they ARE REJECTED.
func TestNoHALFFILEIsLeftWhenTheReadIsCutOffHalfway(t *testing.T) {
	t.Parallel()

	prov, root := newProvider(t)
	boundErr := errors.New("the size bound was exceeded")

	_, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        &cutOffReader{data: pngContent, err: boundErr},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, boundErr,
		"the real error MUST BE PRESERVED; the caller recognizes it with errors.Is and classifies it")
	assert.Empty(t, rootEntries(t, root),
		"neither a half file nor a temporary file must be left")
}

// TestAHalfWrittenFileIsNOTSERVABLE pins the observable consequence of the
// atomic write.
//
// Had a file appeared in the root directory during the write and been completed
// afterwards, a serve request arriving in that interval would return a corrupt
// image. The temporary name starting with a dot, and the key form rejecting a
// body that starts with a dot, closes that interval structurally — the test
// verifies that the temporary name really is not servable.
func TestAHalfWrittenFileIsNOTSERVABLE(t *testing.T) {
	t.Parallel()

	prov, root := newProvider(t)

	temp, err := os.CreateTemp(root, ".uploading-*")
	require.NoError(t, err)
	require.NoError(t, temp.Close())

	_, _, err = prov.Open(context.Background(), filepath.Base(temp.Name()))

	require.Error(t, err, "the temporary name MUST NOT BE a servable key")
	assert.True(t, coreerrors.IsInvalid(err), "error: %v", err)
	assert.Equal(t, local.CodeInvalidKey, coreerrors.CodeOf(err))
}

// TestDeleteIsIDEMPOTENT verifies that a key that does not exist does not give
// an error.
//
// The delete is the cleanup step of the flow that removes the record, and that
// flow can be retried. The second call blowing up would make a file whose record
// is already deleted impossible to clean up — that is, it would make permanent
// exactly the garbage it has to clean.
func TestDeleteIsIDEMPOTENT(t *testing.T) {
	t.Parallel()

	prov, root := newProvider(t)
	ctx := context.Background()

	file, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})
	require.NoError(t, err)

	require.NoError(t, prov.Delete(ctx, file.Key), "the first delete")
	require.NoError(t, prov.Delete(ctx, file.Key), "the SECOND delete must not give an error either")
	assert.Empty(t, rootEntries(t, root))
}

// TestDeletingAnInvalidKeyIsNotAnError verifies that a key with a broken form
// behaves idempotently too.
//
// A file written with such a key can never exist, so the "having been deleted"
// end state already holds. Returning an error would make the delete flow repeat
// forever over something that cannot be corrected.
func TestDeletingAnInvalidKeyIsNotAnError(t *testing.T) {
	t.Parallel()

	prov, _ := newProvider(t)

	assert.NoError(t, prov.Delete(context.Background(), "../../etc/passwd"))
}

// TestOpenREJECTSPathTraversal verifies that the key check protects the serving
// path.
//
// In the normal flow the key comes from the record in the database, that is, it
// is already a value this provider produced. The check exists all the same and it
// IS NOT a "sanitizing": a broken key is not corrected, it is rejected. That way,
// whoever the caller is, a path expression leading outside the root directory can
// never be constructed.
func TestOpenREJECTSPathTraversal(t *testing.T) {
	t.Parallel()

	prov, root := newProvider(t)

	// A file OUTSIDE the root that really exists: only this way can we prove that
	// the rejection comes from the key's form and not from "the file does not
	// exist".
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))

	keys := map[string]string{
		"up one directory":    "../" + filepath.Base(outside),
		"two directories up":  "../../etc/passwd",
		"absolute path":       "/etc/passwd",
		"embedded separator":  "ABC/../../etc/passwd",
		"without extension":   strings.Repeat("A", 26),
		"two dots":            strings.Repeat("A", 26) + ".png.png",
		"lowercase body":      strings.Repeat("a", 26) + ".png",
		"short body":          "ABC.png",
		"uppercase extension": strings.Repeat("A", 26) + ".PNG",
	}

	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			_, _, err := prov.Open(context.Background(), key)

			require.Error(t, err, "the key %q must not be accepted", key)
			assert.Equal(t, local.CodeInvalidKey, coreerrors.CodeOf(err),
				"the rejection must come from the key's FORM, not from the file's absence")
		})
	}
}

// TestOpenReturnsExactlyWhatWasWritten verifies the content the serving path
// reads.
func TestOpenReturnsExactlyWhatWasWritten(t *testing.T) {
	t.Parallel()

	prov, _ := newProvider(t)
	ctx := context.Background()

	file, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})
	require.NoError(t, err)

	content, modTime, err := prov.Open(ctx, file.Key)
	require.NoError(t, err)
	defer func() { _ = content.Close() }()

	got, err := io.ReadAll(content)
	require.NoError(t, err)
	assert.Equal(t, pngContent, got)
	assert.False(t, modTime.IsZero(), "conditional requests need the change time")
}

// TestOpeningADeletedFileReturnsNotFound verifies that the delete really closes
// the serving.
func TestOpeningADeletedFileReturnsNotFound(t *testing.T) {
	t.Parallel()

	prov, _ := newProvider(t)
	ctx := context.Background()

	file, err := prov.Upload(ctx, coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(string(pngContent)),
	})
	require.NoError(t, err)
	require.NoError(t, prov.Delete(ctx, file.Key))

	_, _, err = prov.Open(ctx, file.Key)

	require.Error(t, err)
	assert.True(t, coreerrors.IsNotFound(err), "error: %v", err)
}

// TestAnUnrecognizedTypeGetsTheDefaultExtension verifies that a type that is not
// in the mapping does not block the upload.
//
// The extension is only a human convenience; the serving decision does not look
// at it, the Content-Type is written from the detected type in the record. The
// allow list, in turn, is applied not here but in the service layer — the
// provider does not question the type it is given.
func TestAnUnrecognizedTypeGetsTheDefaultExtension(t *testing.T) {
	t.Parallel()

	prov, _ := newProvider(t)

	file, err := prov.Upload(context.Background(), coreprovider.UploadInput{
		ContentType: "application/octet-stream",
		Body:        strings.NewReader("raw"),
	})

	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(file.Key, ".bin"), "the key: %s", file.Key)
}
