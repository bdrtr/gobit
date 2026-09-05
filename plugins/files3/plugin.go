// Package files3 stores gobit's uploads in an S3-compatible object store.
//
// The provider that comes in the box writes to local disk
// (internal/modules/file/local), which is correct for one process and wrong for
// two: with several instances behind a load balancer the file lands on the disk
// of whichever instance served the upload, and every request routed elsewhere
// gets a 404 — with nothing in the logs, because as far as that instance is
// concerned the key simply does not exist. This plugin is the answer for any
// deployment past a single box.
//
// # No new dependency
//
// The AWS SDK is not used. SigV4 is written by hand in sigv4.go, following the
// decision ADR 0014 already made for the error reporters, where the OTLP body
// and the Sentry envelope were hand-written rather than pulled in. This
// provider makes exactly two calls — PUT object and DELETE object — and the
// signing they need is a fixed recipe.
//
// # It works against MinIO and R2, not only AWS
//
// The endpoint, the region and the addressing style are all configuration.
// "S3-compatible" is the actual target: a self-hosted MinIO is the likeliest
// first deployment, and the difference from AWS is a path-style URL, which is a
// setting rather than a code path.
//
// # The address it returns is DURABLE
//
// [coreprovider.File.URL] is written into the product image record and stays
// there. A presigned URL would therefore rot silently — the record keeps a link
// that stops working when the signature expires, and nothing reports it. So the
// URL this provider returns is unsigned and stable, and serving the object is
// the store's or the CDN's job. An installation whose bucket is private must
// give S3_PUBLIC_BASE_URL pointing at whatever does serve it.
//
// # Usage
//
//	PLUGINS=file-s3
//	FILE_PROVIDER=s3
//	S3_BUCKET=uploads
//	S3_REGION=eu-central-1
//	S3_ACCESS_KEY_ID=...
//	S3_SECRET_ACCESS_KEY=...
package files3

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// Name is the plugin's name in the registry; the PLUGINS list recognizes it.
const Name = "file-s3"

// ProviderID is the provider's id.
//
// FILE_PROVIDER selects by this value and the value reaches durable data
// through the upload ledger, so it must stay stable across releases.
const ProviderID = "s3"

// The setting names. The values are read from the environment and are never
// written anywhere.
const (
	settingBucket    = "S3_BUCKET"
	settingRegion    = "S3_REGION"
	settingKeyID     = "S3_ACCESS_KEY_ID"
	settingSecret    = "S3_SECRET_ACCESS_KEY"
	settingSession   = "S3_SESSION_TOKEN"
	settingEndpoint  = "S3_ENDPOINT"
	settingPathStyle = "S3_PATH_STYLE"
	settingBaseURL   = "S3_PUBLIC_BASE_URL"
	settingPrefix    = "S3_KEY_PREFIX"
)

// Error codes.
const (
	codeMissingSetting = "file_s3_setting_missing"
	codeInvalidSetting = "file_s3_setting_invalid"
	codeBufferFailed   = "file_s3_buffer_failed"
	codeUploadFailed   = "file_s3_upload_failed"
	codeDeleteFailed   = "file_s3_delete_failed"
)

// The URL schemes an endpoint may use.
const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
)

// requestTimeout bounds a call that arrives with no deadline on its context.
//
// It does not replace the caller's deadline, which the contract asks for; it is
// the backstop for the case where the caller forgot. Without it an object store
// that accepts the connection and then stops answering holds the request
// handler open indefinitely.
const requestTimeout = 60 * time.Second

// keyBodyLength is the number of characters in the random part of a store key.
//
// 26 characters of the 32-symbol alphabet carry 130 bits. The number is not
// about collision probability alone: the key appears in a public URL, so it is
// also what stops someone from walking the bucket by guessing.
const keyBodyLength = 26

// keyAlphabet is Crockford Base32 without the letters that misread (I, L, O, U).
//
// The alphabet has no character that is special in a path or a URL, which is
// what makes a produced key structurally incapable of escaping its prefix.
const keyAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// extensions maps the accepted content types to a file extension.
//
// The map is the provider's own rather than the file module's: a plugin cannot
// import a module (Principle 2.4), and duplicating four lines is the price. The
// types themselves come from the CORE contract, so the two lists cannot drift
// apart in what they ACCEPT — only in what extension they choose, which is
// cosmetic.
var extensions = map[string]string{
	coreprovider.ContentTypeJPEG: ".jpg",
	coreprovider.ContentTypePNG:  ".png",
	coreprovider.ContentTypeGIF:  ".gif",
	coreprovider.ContentTypeWebP: ".webp",
}

// defaultExtension is used for a type the map does not know.
//
// An unknown type cannot normally arrive — the module applies the core's
// allow-list before calling — but a provider that produced an extensionless key
// for one would make the object unservable by anything that guesses a type from
// the name.
const defaultExtension = ".bin"

// Plugin is the S3 file plugin.
type Plugin struct{}

// New builds the plugin.
func New() *Plugin { return &Plugin{} }

// Name returns the plugin's name.
func (p *Plugin) Name() string { return Name }

// Setup validates the configuration and registers the provider.
//
// Every fault stops startup. The reasoning is the local provider's, which
// refuses to fall back to a temporary directory: a misconfigured store is a
// configuration error that can be fixed at startup, while the same error
// discovered at the first upload is discovered in front of a customer.
func (p *Plugin) Setup(_ context.Context, h *coreplugin.Host) error {
	required := map[string]string{}
	for _, key := range []string{settingBucket, settingRegion, settingKeyID, settingSecret} {
		v, ok := h.Setting(key)
		if !ok {
			return coreerrors.Invalid(codeMissingSetting,
				"the %s plugin cannot be set up without the %s setting", Name, key)
		}
		required[key] = v
	}

	pathStyle, err := readBool(h, settingPathStyle)
	if err != nil {
		return err
	}

	endpoint, err := readEndpoint(h, required[settingRegion])
	if err != nil {
		return err
	}

	base, err := readBaseURL(h, endpoint, required[settingBucket], pathStyle)
	if err != nil {
		return err
	}

	prefix := normalizePrefix(h)
	sessionToken, _ := h.Setting(settingSession)

	// Neither the secret nor the access key id is logged. What is logged is
	// the shape of the configuration, which is what an operator comparing two
	// deployments actually needs.
	h.Logger().Info("registering the s3 file provider",
		"provider_id", ProviderID,
		"bucket", required[settingBucket],
		"region", required[settingRegion],
		"endpoint", endpoint.String(),
		"path_style", pathStyle,
		"public_base_url", base,
		"key_prefix", prefix,
		"temporary_credentials", sessionToken != "",
	)

	h.RegisterFileProvider(&provider{
		bucket:    required[settingBucket],
		endpoint:  endpoint,
		pathStyle: pathStyle,
		baseURL:   base,
		prefix:    prefix,
		creds: credentials{
			accessKeyID:     required[settingKeyID],
			secretAccessKey: required[settingSecret],
			sessionToken:    sessionToken,
			region:          required[settingRegion],
		},
		client: &http.Client{},
		now:    time.Now,
	})

	return nil
}

// readBool reads an optional boolean setting.
//
// Only the exact words are accepted. A permissive parser makes it easy to
// believe a setting is on when it is off, and for the addressing style that
// mistake produces a 403 whose message is about signing.
func readBool(h *coreplugin.Host, key string) (bool, error) {
	raw, ok := h.Setting(key)
	if !ok {
		return false, nil
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, coreerrors.Invalid(codeInvalidSetting,
			"%s only accepts %q or %q; %q was given", key, "true", "false", raw)
	}
}

// readEndpoint resolves the object store's address.
//
// With no S3_ENDPOINT the AWS regional address is derived, which is the only
// case that can be derived at all: a MinIO or R2 address is not a function of
// the region and has to be given.
func readEndpoint(h *coreplugin.Host, region string) (*url.URL, error) {
	raw, ok := h.Setting(settingEndpoint)
	if !ok {
		return &url.URL{Scheme: schemeHTTPS, Host: "s3." + region + ".amazonaws.com"}, nil
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, coreerrors.Invalid(codeInvalidSetting,
			"%s has to be an absolute URL such as %q; %q was given",
			settingEndpoint, "https://minio.example.test:9000", raw)
	}
	if u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP {
		return nil, coreerrors.Invalid(codeInvalidSetting,
			"%s has to use http or https; %q was given", settingEndpoint, raw)
	}

	return &url.URL{Scheme: u.Scheme, Host: u.Host}, nil
}

// readBaseURL resolves the public address prefix the stored URL is built from.
//
// Deriving it from the endpoint is the default and is right for a public bucket
// and for MinIO. It is wrong whenever a CDN sits in front, and there is no way
// to detect that from here — which is why the setting exists and why the
// derived value is logged at startup: an installation serving through a CDN can
// see, in the first log line, that gobit is about to write the wrong host into
// every image record.
func readBaseURL(h *coreplugin.Host, endpoint *url.URL, bucket string, pathStyle bool) (string, error) {
	if raw, ok := h.Setting(settingBaseURL); ok {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
			return "", coreerrors.Invalid(codeInvalidSetting,
				"%s has to be an absolute http(s) URL; %q was given", settingBaseURL, raw)
		}

		return strings.TrimSuffix(raw, "/"), nil
	}

	if pathStyle {
		return endpoint.Scheme + "://" + endpoint.Host + "/" + bucket, nil
	}

	return endpoint.Scheme + "://" + bucket + "." + endpoint.Host, nil
}

// normalizePrefix reads the optional key prefix and trims its slashes.
//
// The prefix lets one bucket hold more than one installation. It is normalized
// rather than validated: a leading or trailing slash is an easy thing to type
// and produces a double slash in the key, which S3 accepts and stores as a
// DIFFERENT object than the one anyone meant.
func normalizePrefix(h *coreplugin.Host) string {
	raw, ok := h.Setting(settingPrefix)
	if !ok {
		return ""
	}

	return strings.Trim(raw, "/")
}

// provider is the [coreprovider.FileProvider] implementation over S3.
//
// It is safe for concurrent use: every field is set once at Setup, and
// http.Client is safe for concurrent use by definition.
type provider struct {
	bucket    string
	endpoint  *url.URL
	pathStyle bool
	baseURL   string
	prefix    string
	creds     credentials
	client    *http.Client
	// now is injectable so a signature can be reproduced in a test.
	now func() time.Time
}

// The provider satisfies the core contract; a drift is caught at compile time.
var _ coreprovider.FileProvider = (*provider)(nil)

// ID returns the provider's id.
func (p *provider) ID() string { return ProviderID }

// Upload buffers the body, then PUTs it.
//
// # Why the body is buffered, and why to DISK
//
// The contract asks for the body to be streamed, and the reason it gives is
// memory: a 50 MB upload held as a []byte is 50 MB of process memory, and a few
// at once bring the process down. That reasoning is honored here — the buffer
// is a temporary FILE, not a slice.
//
// Buffering at all is forced by HTTP and by SigV4 together. A PUT needs either
// a Content-Length or chunked transfer, and S3 does not accept chunked without
// its own streaming signature format; an io.Reader knows neither its length nor
// its hash. The alternatives were to implement aws-chunked streaming signatures
// (a second signing scheme, for a body the module already caps at a few
// megabytes) or to send UNSIGNED-PAYLOAD (which gives up S3's verification that
// the object it stored is the object that was signed). Buffering keeps both the
// memory bound and the integrity check.
//
// It also makes the contract's cleanup requirement UNNECESSARY rather than
// merely satisfied. The contract says a read that fails midway must leave no
// half-written object. Here a failed read happens before the request is built,
// so no object is ever created — there is nothing to clean up, and therefore no
// cleanup path that can itself fail.
func (p *provider) Upload(ctx context.Context, in coreprovider.UploadInput) (coreprovider.File, error) {
	if in.Body == nil {
		return coreprovider.File{}, coreerrors.Internal(codeUploadFailed,
			"the upload body cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindUnavailable, codeUploadFailed,
			"the upload was canceled before it began")
	}

	buf, size, hash, err := bufferBody(in.Body)
	if err != nil {
		return coreprovider.File{}, err
	}
	defer cleanup(buf)

	key, err := newKey(in.ContentType)
	if err != nil {
		return coreprovider.File{}, err
	}
	objectKey := p.objectKey(key)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, p.objectURL(objectKey), buf)
	if err != nil {
		return coreprovider.File{}, coreerrors.Wrap(err, coreerrors.KindInternal, codeUploadFailed,
			"the upload request could not be built")
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", in.ContentType)

	if err := p.do(ctx, req, hash, codeUploadFailed, "upload"); err != nil {
		return coreprovider.File{}, err
	}

	return coreprovider.File{
		Key:         objectKey,
		URL:         p.baseURL + "/" + objectKey,
		ContentType: in.ContentType,
		Size:        size,
	}, nil
}

// Delete removes the object. It is IDEMPOTENT.
//
// S3 answers 204 for a key that does not exist, so idempotence comes from the
// protocol rather than from a check here. That is deliberate: a check would
// mean a HEAD before every delete — one more round trip, and a window in which
// the answer stops being true.
//
// An empty key is not an error either, for the local provider's reason: no
// object can have been stored under it, so "deleted" is already the state, and
// returning an error would make a retryable cleanup flow fail forever.
func (p *provider) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.objectURL(key), http.NoBody)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindInternal, codeDeleteFailed,
			"the delete request could not be built")
	}

	return p.do(ctx, req, hexSHA256(nil), codeDeleteFailed, "delete")
}

// do signs the request, sends it, and turns a non-2xx answer into an error.
//
// The status check is the point of the function. An object store answers a
// refused write with a status and an XML body; a client that only looks at the
// transport error treats "403 AccessDenied" as a SUCCESS and returns a URL for
// an object that was never stored. The image record then holds a link to
// nothing, and the fault surfaces as a broken image on the storefront rather
// than as an upload error.
func (p *provider) do(ctx context.Context, req *http.Request, payloadHash, code, what string) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, requestTimeout)
		defer cancel()
		req = req.WithContext(ctx)
	}

	p.creds.sign(req, payloadHash, p.now())

	resp, err := p.client.Do(req)
	if err != nil {
		return coreerrors.Wrap(err, coreerrors.KindUnavailable, code,
			"the object store could not be reached for the %s", what)
	}
	defer resp.Body.Close() //nolint:errcheck // the outcome is already decided by the status

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// The body is drained so the connection can be reused. An undrained
		// body is a connection that is closed instead of pooled, which turns
		// into a new TLS handshake per upload.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

		return nil
	}

	// The store's own message is included because it is the only thing that
	// distinguishes the causes: AccessDenied, NoSuchBucket and
	// SignatureDoesNotMatch all arrive as 403 and mean entirely different
	// repairs. It is truncated because an error message is copied into logs
	// and error reports, and the body is not this process's to bound.
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))

	return coreerrors.Unavailable(code,
		"the object store refused the %s with status %d: %s",
		what, resp.StatusCode, strings.TrimSpace(string(detail)))
}

// objectKey applies the configured prefix.
func (p *provider) objectKey(key string) string {
	if p.prefix == "" {
		return key
	}

	return p.prefix + "/" + key
}

// objectURL builds the request address for an object key.
//
// Path style puts the bucket in the path, virtual-hosted style puts it in the
// host. Both are signed the same way; what differs is which one the store
// implements, and MinIO's default is the former while AWS deprecated it.
func (p *provider) objectURL(objectKey string) string {
	if p.pathStyle {
		return p.endpoint.Scheme + "://" + p.endpoint.Host + "/" + p.bucket + "/" + objectKey
	}

	return p.endpoint.Scheme + "://" + p.bucket + "." + p.endpoint.Host + "/" + objectKey
}

// bufferBody writes the body to a temporary file and returns it rewound,
// together with its size and hex SHA-256.
//
// The file is created with [os.CreateTemp] and REMOVED FROM THE DIRECTORY
// immediately by the caller's cleanup; on the failure paths the defer in Upload
// closes and removes it. A buffer that outlived a failed upload would be a file
// nothing points at, which is the same class of garbage the local provider's
// temporary-file handling exists to prevent.
func bufferBody(body io.Reader) (buf *os.File, size int64, payloadHash string, err error) {
	f, err := os.CreateTemp("", "gobit-s3-upload-*")
	if err != nil {
		return nil, 0, "", coreerrors.Wrap(err, coreerrors.KindUnavailable, codeBufferFailed,
			"a temporary file could not be opened for the upload")
	}

	// The name is unlinked at once. The open descriptor keeps the data
	// readable, and the file disappears from the filesystem the moment this
	// process lets go of it — including if the process is killed mid-upload.
	// This is what makes a leaked buffer structurally impossible rather than
	// dependent on a cleanup path being reached.
	_ = os.Remove(f.Name())

	// The hash is accumulated DURING the copy rather than in a second pass over
	// the buffered file: two passes could disagree, and the second one would
	// read back bytes that are already in hand.
	digest := sha256.New()
	size, err = io.Copy(io.MultiWriter(f, digest), body)
	if err != nil {
		cleanup(f)

		// This is the case the contract calls out: a body cut off midway,
		// which is exactly how MaxBytesReader ends an over-sized upload.
		return nil, 0, "", coreerrors.Wrap(err, coreerrors.KindInvalid, codeBufferFailed,
			"the upload body could not be read in full")
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		cleanup(f)

		return nil, 0, "", coreerrors.Wrap(err, coreerrors.KindInternal, codeBufferFailed,
			"the buffered upload could not be rewound")
	}

	return f, size, hex.EncodeToString(digest.Sum(nil)), nil
}

// cleanup closes a buffer file. The name is already unlinked, so closing is
// what frees the space.
func cleanup(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// newKey produces the store key.
//
// The key is produced HERE and never derives from anything the client sent —
// which is what makes writing outside the prefix with "../" structurally
// impossible rather than a matter of how many encoding tricks a sanitizer
// remembers. See the [coreprovider.UploadInput] documentation.
func newKey(contentType string) (string, error) {
	ext, known := extensions[contentType]
	if !known {
		ext = defaultExtension
	}

	raw := make([]byte, keyBodyLength)
	if _, err := rand.Read(raw); err != nil {
		// A key that is not random is a key that can be guessed, and the key
		// appears in a public URL. Falling back to a counter or a timestamp
		// was rejected for that reason; an unavailable random source is a
		// failure worth reporting.
		return "", coreerrors.Wrap(err, coreerrors.KindInternal, codeUploadFailed,
			"a random store key could not be produced")
	}

	var b strings.Builder
	b.Grow(keyBodyLength + len(ext))
	for _, v := range raw {
		b.WriteByte(keyAlphabet[int(v)%len(keyAlphabet)])
	}
	b.WriteString(ext)

	return b.String(), nil
}
