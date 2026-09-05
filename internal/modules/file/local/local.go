// Package local is the default file provider, the one that keeps files ON THE
// LOCAL DISK, in a configured root directory (plan Section 5.6).
//
// [Provider] satisfies the FileProvider contract in core/provider and
// is the only provider that comes out of the box: gobit is a framework and
// cannot know which object store will be used, but it is obliged to show that
// the upload path is standing.
//
// # The storage key is produced by the PROVIDER
//
// [Provider.Upload]'s input carries NO file name (the core contract's
// decision). The key is produced here: a time-ordered identifier + the
// extension derived from the detected content type. Because no string coming
// from the client turns into a path component, writing outside the root with
// "../" is STRUCTURALLY impossible — it does not hang on a "sanitizing" step
// working correctly.
//
// The key is on a single plane (there are no subdirectories) and this
// simplifies the claim on the serving path too: a valid key holds NO path
// separator at all, that is, the join of the key and the root cannot come out
// from under the root. Let the known limit be written down as well: millions of
// entries in a single directory slow the file system's directory scan down. At
// that point the right answer is not splitting into subdirectories, it is
// leaving the local disk and moving to an object store.
//
// # The write is ATOMIC
//
// The file is first written under a temporary name in the same directory, it is
// fsynced, and only then moved to its final name ([os.Rename]). Writing
// straight to the final name would have meant that a half-written file could be
// served at that moment: the browser shows a corrupt image and, because the file
// "exists", no retry fixes it.
//
// Opening the temporary file IN THE SAME DIRECTORY is mandatory: [os.Rename] is
// atomic only within the same file system. Had the temporary file been opened in
// /tmp, the move would fail with EXDEV in most installations, and where it did
// not, atomicity would be lost.
package local

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// ID is the provider's identifier; this is the FILE_PROVIDER default.
const ID = "local"

// DefaultURLPrefix is the default path prefix of the produced addresses.
//
// The prefix IS NOT and cannot be UNDER /admin/v1 or /store/v1; the reasoning is
// in the serving section of the internal/modules/file/api package (in short: the
// <img> tag in the storefront cannot send headers).
const DefaultURLPrefix = "/files"

// Error codes.
const (
	// CodeNotReady reports that the provider was built without a root
	// directory.
	CodeNotReady = "file_local_not_ready"
	// CodeRootUnusable reports that the root directory could not be opened or
	// written to.
	CodeRootUnusable = "file_local_root_unusable"
	// CodeWriteFailed reports that the file could not be written to disk.
	CodeWriteFailed = "file_local_write_failed"
	// CodeInvalidKey reports that the storage key is not in the form this
	// provider produces.
	CodeInvalidKey = "file_local_invalid_key"
	// CodeReadFailed reports that the file could not be read.
	CodeReadFailed = "file_local_read_failed"
)

// tempPrefix is the temporary name prefix of half-written files.
//
// It starts with a dot, and a valid key CAN NOT have a body that starts with a
// dot ([keyValid]); that is, a temporary file left behind after a crash never
// matches a servable key.
const tempPrefix = ".uploading-"

// dirPerm is the permission used while the root directory is created.
const dirPerm os.FileMode = 0o750

// extensions is the mapping from the detected content type to the file
// extension.
//
// The extension is only a HUMAN and TOOL convenience: whoever inspects or backs
// up the root directory by hand understands from it what the file is. The
// serving decision DOES NOT LOOK at it — the Content-Type is written from the
// detected type in the record. That is why the extension being ".bin" for an
// unrecognized type is harmless too.
var extensions = map[string]string{
	coreprovider.ContentTypeJPEG: ".jpg",
	coreprovider.ContentTypePNG:  ".png",
	coreprovider.ContentTypeGIF:  ".gif",
	coreprovider.ContentTypeWebP: ".webp",
}

// defaultExtension is used for the types that are not in the mapping.
const defaultExtension = ".bin"

// Options are the setup settings of the provider.
type Options struct {
	// Root is the root directory the files will be written into; it is
	// required.
	Root string
	// URLPrefix is the path prefix of the produced addresses; if empty,
	// [DefaultURLPrefix].
	URLPrefix string
	// Logger is the target the cleanup warnings are written to; if nil, the logs
	// are discarded.
	//
	// It exists only to report the temporary file LEFT BEHIND: that file has no
	// database record whatsoever, so if it is not logged its existence cannot be
	// noticed from anywhere and the disk silently fills up.
	Logger *slog.Logger
}

// Provider is the provider that writes the files to the local disk.
// It is safe for concurrent use: its state is nothing but unchanging settings.
type Provider struct {
	root      string
	urlPrefix string
	log       *slog.Logger
}

// That Provider satisfies the core contract is verified at compile time; a
// signature drift does not get left to run time.
var _ coreprovider.FileProvider = (*Provider)(nil)

// New produces a provider working on the given root directory.
//
// The directory is created HERE and, if it cannot be created, an error is
// returned. Trying it at setup time is deliberate: a root that cannot be written
// to, if it waits until the first upload, surfaces as a fault in front of the
// customer — whereas a misspelled path or a missing mount point is a
// configuration error that can be corrected at startup.
//
// An empty root is REJECTED and the TEMPORARY DIRECTORY is not fallen back to.
// The temporary directory would be tempting ("let it work without configuring
// anything") but it would silently lose the images on a restart: the address
// stays permanently in the product record while the file is gone, and no error
// is visible. Silent data loss is always more expensive than a configuration
// error that blows up at startup.
func New(opts Options) (*Provider, error) {
	if opts.Root == "" {
		return nil, coreerrors.Internal(CodeNotReady,
			"the %q file provider cannot be built without a root directory", ID)
	}

	if err := os.MkdirAll(opts.Root, dirPerm); err != nil {
		return nil, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeRootUnusable,
			"the file root directory could not be prepared: %s", opts.Root)
	}

	prefix := opts.URLPrefix
	if prefix == "" {
		prefix = DefaultURLPrefix
	}

	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Provider{root: opts.Root, urlPrefix: prefix, log: log}, nil
}

// ID returns the provider's identifier.
func (p *Provider) ID() string { return ID }

// Root returns the provider's root directory; it is for logging and diagnosis.
func (p *Provider) Root() string { return p.root }

// Upload writes the body to disk and returns the reachable address.
//
// The key is produced here; there is no file name in the input (see the package
// documentation).
//
// If the read fails halfway — a body exceeding the size bound is cut off in
// exactly this way — the temporary file is DELETED and the error is returned.
// The cleanup is a requirement of the core contract: a half object is a file
// that no record points at and whose key no delete path knows, that is, requests
// exceeding the bound could fill the disk up anyway.
func (p *Provider) Upload(ctx context.Context, in coreprovider.UploadInput) (coreprovider.File, error) {
	if in.Body == nil {
		return coreprovider.File{}, coreerrors.Internal(CodeWriteFailed,
			"the upload body cannot be nil")
	}
	// If the context has been canceled it returns without writing a single byte.
	// io.Copy DOES NOT SEE the ctx; the file system calls do not block, but
	// writing to disk while the client is long gone has no return either.
	if err := ctx.Err(); err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"the upload was canceled before it started")
	}

	key := newKey(in.ContentType, time.Now())

	temp, err := os.CreateTemp(p.root, tempPrefix+"*")
	if err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeRootUnusable,
			"the temporary file could not be opened: %s", p.root)
	}
	tempPath := temp.Name()

	// The cleanup IS DEFERRED, it is not sprinkled over the error branches.
	//
	// Today's branches are complete, but two situations stay outside them: a
	// PANIC in between (the Recoverer turns it into a 500 and the request ends)
	// and any early return that gets added later. In both, a half ".uploading"
	// file is left behind; the disk silently fills up and nobody notices,
	// because that file has no record at all. The defer takes the cleanup out of
	// depending on a new branch being remembered.
	//
	// A cleanup error IS NOT SWALLOWED, but it DOES NOT REPLACE the real error
	// either: it is logged. Had it replaced it, the diagnosis of the read error
	// would have been erased.
	moved := false

	defer func() {
		if moved {
			return
		}

		if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.log.Warn("the temporary upload file could not be deleted, it has to be cleaned up by hand",
				"path", tempPath, "error", err)
		}
	}()

	written, err := writeAndClose(temp, in.Body)
	if err != nil {
		return coreprovider.File{}, err
	}

	if err := os.Rename(tempPath, filepath.Join(p.root, key)); err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"the file could not be moved to its final name: %s", key)
	}

	moved = true

	return coreprovider.File{
		Key:         key,
		URL:         p.urlPrefix + "/" + key,
		ContentType: in.ContentType,
		Size:        written,
	}, nil
}

// Delete deletes the file from disk. It IS IDEMPOTENT: a key that does not exist
// is not an error.
//
// An INVALID key is not an error either and this is deliberate: a file written
// with such a key can never exist, so the "having been deleted" end state
// already holds. Returning an error would make the delete flow repeat forever
// over something that cannot be corrected.
func (p *Provider) Delete(_ context.Context, key string) error {
	if !keyValid(key) {
		return nil
	}

	if err := os.Remove(filepath.Join(p.root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeWriteFailed,
			"the file could not be deleted: %s", key)
	}

	return nil
}

// Open opens the file for reading and returns the moment it last changed.
//
// It is not in the core contract ([coreprovider.FileProvider]) and it must not
// be: serving the file is not the provider's job and an object store leaves the
// serving to the CDN. On the local disk, on the other hand, there is nobody else
// to serve it, so this provider adds a method ON TOP OF the contract; the HTTP
// layer looks for it through the narrow interface it defines itself (the
// consuming-side pattern of ADR 0001).
//
// # The key form IS VALIDATED
//
// In the normal flow the value comes from the record in the database, that is,
// it is already a key this provider produced. The validation is done all the
// same and it IS NOT a "sanitizing": a broken key is not corrected, it is
// REJECTED. That way, whoever the caller is — today's record path or another
// path to be written tomorrow — a path expression leading outside the root can
// never be constructed.
func (p *Provider) Open(_ context.Context, key string) (io.ReadSeekCloser, time.Time, error) {
	if !keyValid(key) {
		return nil, time.Time{}, coreerrors.Invalid(CodeInvalidKey,
			"invalid storage key: %q", key)
	}

	// The G304 suppression is deliberate and rests exactly on the check above:
	// key has passed through [keyValid] and the accepted alphabet holds NO path
	// separator, dot-dot or NUL — that is, the join cannot come out from under
	// the root. Whoever removes the check is obliged to review this line too;
	// this comment is that link itself.
	f, err := os.Open(filepath.Join(p.root, key)) //nolint:gosec // G304: the key form is validated, it cannot leave the root
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, coreerrors.NotFound(CodeReadFailed,
				"the file could not be found in the store: %s", key)
		}

		return nil, time.Time{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeReadFailed,
			"the file could not be opened: %s", key)
	}

	info, err := f.Stat()
	if err != nil {
		_ = f.Close()

		return nil, time.Time{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeReadFailed,
			"the file info could not be read: %s", key)
	}

	return f, info.ModTime(), nil
}

// writeAndClose writes the body into the temporary file, commits it to disk and
// closes it.
//
// The [os.File.Sync] call is deliberate: the move (rename) makes the file NAME
// atomic but does not guarantee that the CONTENT reached the disk. Without the
// Sync, a machine going down right after the move could be left with a file
// standing under the final name but empty inside — that is, exactly the "corrupt
// image" we want to avoid.
func writeAndClose(f *os.File, body io.Reader) (int64, error) {
	written, copyErr := io.Copy(f, body)

	if copyErr == nil {
		copyErr = f.Sync()
	}

	// The close is attempted in every case; leaking an open file descriptor is
	// unacceptable on the error path too.
	if closeErr := f.Close(); copyErr == nil {
		copyErr = closeErr
	}

	if copyErr != nil {
		// The wrapping PRESERVES the chain: the caller (the service) looks for
		// the size bound error inside this chain with errors.Is and turns it
		// into a client error. Classifying it here would be making a decision
		// the provider does not know.
		return 0, coreerrors.Wrap(copyErr, coreerrors.KindInternal, CodeWriteFailed,
			"the file could not be written to disk")
	}

	return written, nil
}

// newKey produces a storage key from the detected content type.
func newKey(contentType string, t time.Time) string {
	ext, known := extensions[contentType]
	if !known {
		ext = defaultExtension
	}

	// The prefix is given EMPTY: the key is not a record identifier, and had it
	// carried the "upl_" prefix, two different things (the record identifier and
	// the storage key) would get mixed up with each other in the log and in the
	// address bar.
	return models.NewID("", t) + ext
}

// keyValid reports that the value is in the form this provider produces.
//
// The form: a 26-character Crockford Base32 body + a dot + a lowercase/digit
// extension. The accepted alphabet holds NO path separator, dot-dot or NUL; that
// is, a key counted as valid cannot point outside the root directory.
//
// The check is done over the ALPHABET, not by "searching for forbidden
// sequences": searching for "../" would have meant adding one more line to the
// list for every new encoding trick (%2e%2e, backslash, embedded NUL, Unicode
// normalization), and its correctness would depend on how many tricks whoever
// wrote the list remembered that day. Rejecting everything outside the permitted
// alphabet carries no such debt.
func keyValid(key string) bool {
	body, ext, found := split(key)
	if !found || len(body) != models.IDBodyLength() || ext == "" {
		return false
	}

	for _, r := range body {
		// The Crockford Base32 alphabet: 0-9 and the capital letters other than
		// I, L, O, U. Instead of repeating the whole alphabet here, a class check
		// is done; the aim is not to decode the key but to guarantee that no
		// character able to produce a path got through.
		if (r < '0' || r > '9') && (r < 'A' || r > 'Z') {
			return false
		}
	}

	for _, r := range ext {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// split separates the key into body and extension; if there is no dot, found is
// false.
//
// The separation is done by the SINGLE dot, not by the LAST one: a value
// carrying more than one dot is not a key this provider produced and it should
// be rejected rather than parsed.
func split(key string) (body, ext string, found bool) {
	dot := -1
	for i, r := range key {
		if r != '.' {
			continue
		}
		if dot >= 0 {
			return "", "", false
		}
		dot = i
	}

	if dot < 0 {
		return "", "", false
	}

	return key[:dot], key[dot+1:], true
}
