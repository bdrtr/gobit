package files3

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreplugin "github.com/bdrtr/gobit/core/plugin"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// WHAT THESE TESTS DO NOT PROVE
//
// Nothing here verifies the signature against an AWS reference vector. A hand-
// written expected value would be a value produced by reading this same code,
// so it would confirm nothing but its own consistency — and a wrong constant
// would send someone "fixing" correct code to match it.
//
// What the unit tests below prove is the shape and the SENSITIVITY of the
// signature: that every input which must be covered actually changes the
// output, that the secret never leaves the process, and that a refused request
// is never mistaken for a stored object.
//
// The signature's CORRECTNESS is proved in s3_integration_test.go, against a
// real MinIO that validates SigV4 the way S3 does. That is the only proof worth
// having, and it needs Docker, which is why it sits behind the integration tag.

// fixedTime is the signing instant the tests use, so a signature is
// reproducible.
var fixedTime = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

// testCreds are the credentials the signing tests use. They are not real.
var testCreds = credentials{
	accessKeyID:     "AKIAIOSFODNN7EXAMPLE",
	secretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	region:          "eu-central-1",
}

// signedRequest builds and signs a request the way the provider does.
func signedRequest(t *testing.T, method, rawURL string, creds credentials) *http.Request {
	t.Helper()

	req, err := http.NewRequest(method, rawURL, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Content-Type", coreprovider.ContentTypePNG)

	creds.sign(req, hexSHA256(nil), fixedTime)

	return req
}

// authOf returns the signature field of the Authorization header.
func authOf(t *testing.T, req *http.Request) string {
	t.Helper()

	auth := req.Header.Get("Authorization")
	require.NotEmpty(t, auth)
	_, sig, found := strings.Cut(auth, "Signature=")
	require.True(t, found, "the Authorization header carries no Signature field: %q", auth)

	return sig
}

// --- the signature ----------------------------------------------------------

// TestTheSignatureIsDeterministic proves the same inputs produce the same
// signature.
//
// Without this the sensitivity tests below would prove nothing: a signature
// that differed every time would "change" for every input too.
func TestTheSignatureIsDeterministic(t *testing.T) {
	a := signedRequest(t, http.MethodPut, "https://b.s3.eu-central-1.amazonaws.com/k.png", testCreds)
	b := signedRequest(t, http.MethodPut, "https://b.s3.eu-central-1.amazonaws.com/k.png", testCreds)

	assert.Equal(t, authOf(t, a), authOf(t, b))
}

// TestTheSignatureCoversEveryInputItMustCover is the sensitivity proof.
//
// Each case changes ONE thing that the signature is required to cover. If any
// of them leaves the signature unchanged, that input is outside the signature —
// and an input outside the signature is one an intermediary can rewrite without
// invalidating anything. The host case is the sharpest: a signature that does
// not cover the host can be replayed against another bucket.
func TestTheSignatureCoversEveryInputItMustCover(t *testing.T) {
	const base = "https://bucket.s3.eu-central-1.amazonaws.com/key.png"
	reference := authOf(t, signedRequest(t, http.MethodPut, base, testCreds))

	t.Run("the host", func(t *testing.T) {
		other := signedRequest(t, http.MethodPut,
			"https://other.s3.eu-central-1.amazonaws.com/key.png", testCreds)
		assert.NotEqual(t, reference, authOf(t, other),
			"a signature that does not cover the host can be replayed against another bucket")
	})

	t.Run("the method", func(t *testing.T) {
		other := signedRequest(t, http.MethodDelete, base, testCreds)
		assert.NotEqual(t, reference, authOf(t, other),
			"a signature that does not cover the method turns a PUT into a DELETE")
	})

	t.Run("the path", func(t *testing.T) {
		other := signedRequest(t, http.MethodPut,
			"https://bucket.s3.eu-central-1.amazonaws.com/another.png", testCreds)
		assert.NotEqual(t, reference, authOf(t, other))
	})

	t.Run("the region", func(t *testing.T) {
		creds := testCreds
		creds.region = "us-east-1"
		assert.NotEqual(t, reference, authOf(t, signedRequest(t, http.MethodPut, base, creds)))
	})

	t.Run("the secret", func(t *testing.T) {
		creds := testCreds
		creds.secretAccessKey = "a different secret entirely"
		assert.NotEqual(t, reference, authOf(t, signedRequest(t, http.MethodPut, base, creds)))
	})

	t.Run("the payload hash", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPut, base, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Content-Type", coreprovider.ContentTypePNG)
		testCreds.sign(req, hexSHA256([]byte("some body")), fixedTime)

		assert.NotEqual(t, reference, authOf(t, req),
			"a signature that does not cover the body lets the stored object differ from the signed one")
	})

	t.Run("the instant", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPut, base, http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Content-Type", coreprovider.ContentTypePNG)
		testCreds.sign(req, hexSHA256(nil), fixedTime.Add(48*time.Hour))

		assert.NotEqual(t, reference, authOf(t, req),
			"the signing key is day-scoped; a captured header must not work tomorrow")
	})
}

// TestTheSecretNeverLeavesTheProcess proves the signing secret appears nowhere
// on the wire.
//
// The Authorization header carries the access key ID openly — that is by
// design — but a secret reaching any header would hand the whole store's
// contents and delete rights to anyone who captured one request.
func TestTheSecretNeverLeavesTheProcess(t *testing.T) {
	req := signedRequest(t, http.MethodPut, "https://b.s3.eu-central-1.amazonaws.com/k.png", testCreds)

	for name, values := range req.Header {
		for _, v := range values {
			assert.NotContains(t, v, testCreds.secretAccessKey,
				"the signing secret leaked into the %s header", name)
		}
	}
}

// TestTheSignedHeadersListCoversTheHost proves host is declared as signed.
//
// Covering the host in the calculation but forgetting it in SignedHeaders makes
// the server verify a DIFFERENT canonical form than the one that was signed,
// and the failure is a 403 that reads as bad credentials.
func TestTheSignedHeadersListCoversTheHost(t *testing.T) {
	req := signedRequest(t, http.MethodPut, "https://b.s3.eu-central-1.amazonaws.com/k.png", testCreds)

	auth := req.Header.Get("Authorization")
	assert.Contains(t, auth, "SignedHeaders=")
	assert.Contains(t, auth, "host",
		"host must be declared in SignedHeaders, not merely used in the calculation")
	assert.Contains(t, auth, "x-amz-content-sha256")
	assert.Contains(t, auth, "x-amz-date")
}

// TestTheCanonicalHeaderBlockEndsWithANewline pins the single most common
// SigV4 mistake.
//
// The canonical form requires a newline after EVERY header line including the
// last, which leaves a blank line before SignedHeaders. Omitting it produces a
// signature that is wrong with nothing to explain why.
func TestTheCanonicalHeaderBlockEndsWithANewline(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut, "https://b.example.test/k", http.NoBody)
	require.NoError(t, err)
	req.Host = "b.example.test"
	req.Header.Set("X-Amz-Date", "20260314T092653Z")

	signed, canonical := canonicalize(req)

	assert.True(t, strings.HasSuffix(canonical, "\n"),
		"the canonical header block must end with a newline; got %q", canonical)
	assert.Equal(t, "host;x-amz-date", signed,
		"the signed header names have to be lowercase and sorted")
}

// TestTheSessionTokenIsSignedWhenPresent proves temporary credentials work.
//
// S3 rejects a session token that is sent but not signed. Some S3-compatible
// implementations accept it, which is the worst case: the setup works against a
// local MinIO and fails against AWS.
func TestTheSessionTokenIsSignedWhenPresent(t *testing.T) {
	creds := testCreds
	creds.sessionToken = "FQoGZXIvYXdzEExample"

	req := signedRequest(t, http.MethodPut, "https://b.s3.eu-central-1.amazonaws.com/k.png", creds)

	assert.Equal(t, creds.sessionToken, req.Header.Get("X-Amz-Security-Token"))
	assert.Contains(t, req.Header.Get("Authorization"), "x-amz-security-token",
		"the token has to be in SignedHeaders; AWS rejects it otherwise")
}

// --- the store key ----------------------------------------------------------

// TestTheStoreKeyCarriesNoPathCharacter proves a produced key cannot escape its
// prefix.
//
// The contract's whole defense against "../" is that the key is PRODUCED rather
// than taken from the client. That defense is only real if what is produced
// contains nothing a path parser treats specially.
func TestTheStoreKeyCarriesNoPathCharacter(t *testing.T) {
	for range 200 {
		key, err := newKey(coreprovider.ContentTypePNG)
		require.NoError(t, err)

		assert.NotContains(t, key, "/")
		assert.NotContains(t, key, "\\")
		assert.NotContains(t, key, "..")
		assert.Equal(t, key, url.PathEscape(key),
			"a key that changes under URL escaping signs differently than it is stored")
		assert.True(t, strings.HasSuffix(key, ".png"))
		assert.Len(t, key, keyBodyLength+len(".png"))
	}
}

// TestTwoKeysDiffer proves the key is random rather than derived from a clock.
//
// A time-derived key collides under concurrency, and the loser of the collision
// silently OVERWRITES the winner's object — one customer's upload replacing
// another's, with no error anywhere.
func TestTwoKeysDiffer(t *testing.T) {
	seen := make(map[string]struct{}, 500)
	for range 500 {
		key, err := newKey(coreprovider.ContentTypeJPEG)
		require.NoError(t, err)
		_, dup := seen[key]
		require.False(t, dup, "the same store key was produced twice: %s", key)
		seen[key] = struct{}{}
	}
}

// TestAnUnknownTypeStillGetsAnExtension proves no key is left extensionless.
func TestAnUnknownTypeStillGetsAnExtension(t *testing.T) {
	key, err := newKey("application/octet-stream")

	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(key, defaultExtension))
}

// --- the request/response path ----------------------------------------------

// newProvider builds a provider pointed at a test server.
func newProvider(t *testing.T, server *httptest.Server) *provider {
	t.Helper()

	u, err := url.Parse(server.URL)
	require.NoError(t, err)

	return &provider{
		bucket:    "uploads",
		endpoint:  &url.URL{Scheme: u.Scheme, Host: u.Host},
		pathStyle: true,
		baseURL:   server.URL + "/uploads",
		creds:     testCreds,
		client:    server.Client(),
		now:       func() time.Time { return fixedTime },
	}
}

// TestARefusedUploadIsNotReportedAsStored is the silent-failure proof, and it
// is the most important test in this file.
//
// An object store answers a refused write with a status and an XML body, not
// with a transport error. A client that only checks the transport error returns
// a URL for an object that was never stored; the image record then holds a link
// to nothing, and the fault surfaces weeks later as a broken image rather than
// as an upload failure.
func TestARefusedUploadIsNotReportedAsStored(t *testing.T) {
	for name, status := range map[string]int{
		"access denied":  http.StatusForbidden,
		"no such bucket": http.StatusNotFound,
		"server error":   http.StatusInternalServerError,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`<Error><Code>AccessDenied</Code></Error>`))
			}))
			defer srv.Close()

			_, err := newProvider(t, srv).Upload(t.Context(), coreprovider.UploadInput{
				ContentType: coreprovider.ContentTypePNG,
				Body:        strings.NewReader("a png, allegedly"),
			})

			require.Error(t, err, "status %d must not be reported as a stored object", status)
			assert.Equal(t, codeUploadFailed, coreerrors.CodeOf(err))
			assert.Contains(t, err.Error(), "AccessDenied",
				"the store's own message is the only thing that distinguishes the causes")
		})
	}
}

// TestASuccessfulUploadReturnsADurableAddress proves the happy path and the
// address it produces.
func TestASuccessfulUploadReturnsADurableAddress(t *testing.T) {
	var gotPath, gotType, gotAuth string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	file, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("the bytes"),
	})

	require.NoError(t, err)
	assert.Equal(t, "/uploads/"+file.Key, gotPath, "path-style addressing puts the bucket in the path")
	assert.Equal(t, coreprovider.ContentTypePNG, gotType)
	assert.Equal(t, "the bytes", string(gotBody), "the buffered body has to be sent in full")
	assert.Contains(t, gotAuth, "AWS4-HMAC-SHA256")
	assert.Equal(t, int64(len("the bytes")), file.Size)
	assert.Equal(t, srv.URL+"/uploads/"+file.Key, file.URL)
	assert.NotContains(t, file.URL, "X-Amz-Signature",
		"the stored address must be unsigned; a presigned URL rots in the database")
}

// cutReader fails partway through, the way MaxBytesReader ends an oversized
// body.
type cutReader struct {
	data []byte
	read int
}

func (c *cutReader) Read(p []byte) (int, error) {
	if c.read >= len(c.data) {
		return 0, errors.New("http: request body too large")
	}
	n := copy(p, c.data[c.read:c.read+1])
	c.read += n

	return n, nil
}

// TestABodyCutMidwayReachesTheStoreAtAll proves the contract's cleanup
// requirement is met by making it unnecessary.
//
// The contract says a read that fails midway must leave no half-written object.
// Because the body is buffered before the request is built, a failed read
// happens BEFORE anything is sent — so no object is created and there is no
// cleanup path that can itself fail. The test asserts the store was never
// contacted.
func TestABodyCutMidwayReachesTheStoreAtAll(t *testing.T) {
	contacted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := newProvider(t, srv).Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        &cutReader{data: []byte("half a file")},
	})

	require.Error(t, err)
	assert.Equal(t, codeBufferFailed, coreerrors.CodeOf(err))
	assert.False(t, contacted,
		"a body that failed to read must never produce a request; there would be nothing to clean up")
}

// TestDeleteIsIdempotent proves a missing key is not an error.
//
// Deletion is the cleanup step of a retryable flow. A second call blowing up
// would make a file whose record is already gone impossible to clean up — it
// would make permanent exactly the garbage it exists to remove.
func TestDeleteIsIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// S3 answers 204 for a key that never existed.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := newProvider(t, srv)

	require.NoError(t, p.Delete(t.Context(), "NEVEREXISTED0000000000000.png"))
	require.NoError(t, p.Delete(t.Context(), "NEVEREXISTED0000000000000.png"))
}

// TestDeleteOfAnEmptyKeyContactsNothing proves an empty key short-circuits.
func TestDeleteOfAnEmptyKeyContactsNothing(t *testing.T) {
	contacted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	require.NoError(t, newProvider(t, srv).Delete(t.Context(), "   "))
	assert.False(t, contacted)
}

// --- configuration ----------------------------------------------------------

// TestTheDerivedEndpointIsTheRegionalAWSAddress proves the only case that CAN
// be derived is the one that is.
func TestTheDerivedEndpointIsTheRegionalAWSAddress(t *testing.T) {
	u, err := readEndpoint(hostWith(t, nil), "eu-central-1")

	require.NoError(t, err)
	assert.Equal(t, "https://s3.eu-central-1.amazonaws.com", u.String())
}

// TestAnEndpointWithoutASchemeIsRefused proves a half-written endpoint stops
// startup rather than producing requests to nowhere.
func TestAnEndpointWithoutASchemeIsRefused(t *testing.T) {
	for _, raw := range []string{"minio.example.test:9000", "ftp://minio.example.test", "not a url at all"} {
		t.Run(raw, func(t *testing.T) {
			_, err := readEndpoint(hostWith(t, map[string]string{settingEndpoint: raw}), "eu-central-1")

			require.Error(t, err)
			assert.Equal(t, codeInvalidSetting, coreerrors.CodeOf(err))
		})
	}
}

// TestTheBaseURLFollowsTheAddressingStyle proves the derived public address
// matches how the object is actually addressed.
//
// A mismatch here is the quietest fault this plugin can have: the upload
// succeeds, the record is written, and the URL points at a host that serves
// nothing.
func TestTheBaseURLFollowsTheAddressingStyle(t *testing.T) {
	endpoint := &url.URL{Scheme: "https", Host: "s3.eu-central-1.amazonaws.com"}

	virtual, err := readBaseURL(hostWith(t, nil), endpoint, "uploads", false)
	require.NoError(t, err)
	assert.Equal(t, "https://uploads.s3.eu-central-1.amazonaws.com", virtual)

	path, err := readBaseURL(hostWith(t, nil), endpoint, "uploads", true)
	require.NoError(t, err)
	assert.Equal(t, "https://s3.eu-central-1.amazonaws.com/uploads", path)
}

// TestAnExplicitBaseURLWinsAndIsTrimmed proves the CDN case overrides the
// derived value.
func TestAnExplicitBaseURLWinsAndIsTrimmed(t *testing.T) {
	h := hostWith(t, map[string]string{settingBaseURL: "https://cdn.example.test/assets/"})

	base, err := readBaseURL(h, &url.URL{Scheme: "https", Host: "s3.test"}, "uploads", false)

	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example.test/assets", base,
		"a trailing slash would produce a double slash in every stored address")
}

// TestThePrefixIsNormalized proves a typed slash cannot produce a second,
// different object path.
func TestThePrefixIsNormalized(t *testing.T) {
	for _, raw := range []string{"tenant-a", "/tenant-a", "tenant-a/", "/tenant-a/"} {
		assert.Equal(t, "tenant-a", normalizePrefix(hostWith(t, map[string]string{settingPrefix: raw})))
	}
}

// TestPathStyleOnlyAcceptsTheExactWords proves a permissive parser cannot make
// an installation believe path-style is on when it is off.
func TestPathStyleOnlyAcceptsTheExactWords(t *testing.T) {
	for _, raw := range []string{"1", "yes", "TRUE", "on"} {
		_, err := readBool(hostWith(t, map[string]string{settingPathStyle: raw}), settingPathStyle)
		require.Error(t, err, "%q must not be read as a boolean", raw)
	}

	on, err := readBool(hostWith(t, map[string]string{settingPathStyle: "true"}), settingPathStyle)
	require.NoError(t, err)
	assert.True(t, on)
}

// hostWith builds a plugin Host carrying just the given settings.
//
// The other Host dependencies are nil: the configuration readers under test
// touch nothing but the settings map, and passing real ones would make a
// failure here ambiguous about which of them broke.
func hostWith(t *testing.T, settings map[string]string) *coreplugin.Host {
	t.Helper()

	return coreplugin.NewHost(nil, nil, nil, nil, settings)
}
