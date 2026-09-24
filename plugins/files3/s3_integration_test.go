//go:build integration

package files3

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
)

// This file is the only real proof that the hand-written SigV4 in sigv4.go is
// CORRECT.
//
// The unit tests prove the signature's shape and its sensitivity — that every
// input which must be covered changes the output. They cannot prove the
// signature is the one S3 expects, because the only expected value available to
// them would be one produced by reading this same code.
//
// The server validates SigV4 the way S3 does. If the canonical form is wrong by
// a single newline, if the S3 path-encoding exception is missed, or if a header
// is signed but not declared, every test below fails with 403
// SignatureDoesNotMatch. That is the check that has to exist somewhere, and
// nothing smaller than a real implementation can perform it.

// s3Image is PINNED, following the repository's rule for the postgres and redis
// containers: an unpinned tag means the day the server changes its validation
// is a day this suite fails for reasons unrelated to the change being tested.
//
// # Why RustFS and no longer MinIO
//
// The pin was never the problem; the access under it was, twice. On 2026-09-11
// `minio/minio` on Docker Hub began refusing anonymous pulls of every tag, and
// the suite moved to MinIO's own registry (D80). On 2026-09-24 that registry
// answered "no such manifest" for the pinned tag and for `latest` alike: MinIO
// no longer distributes the community server as an image anywhere this lane can
// pull without credentials. Both times CI went red while every local run stayed
// green on a year-old cached image, which is the whole of D80's lesson and the
// reason it happened again — a cache is not a registry.
//
// RustFS is an independent S3 server under Apache-2.0, published on Docker Hub,
// and it holds the two properties this suite depends on, measured rather than
// assumed: a canonical header block without its trailing newline is refused
// (the signature tests go red), and an object is served anonymously only after
// the bucket policy grants it (the read-back test goes red without the policy).
// It is still a vendor repository, the class where access can change under a
// pin; what changed is that the suite no longer rests on one vendor's name.
const s3Image = "rustfs/rustfs:1.0.0"

// The container's root credentials. They are the test's own and reach nothing
// outside the container's lifetime.
const (
	s3AccessKey = "gobit-test-key"
	s3SecretKey = "gobit-test-secret"
	// The server answers for any region when the bucket was created without
	// one; the value still has to match between signing and verification.
	s3Region   = "us-east-1"
	testBucket = "gobit-uploads"
)

// startS3 brings up an S3 server and returns its endpoint.
func startS3(t *testing.T) *url.URL {
	t.Helper()

	ctx := t.Context()

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        s3Image,
			ExposedPorts: []string{"9000/tcp"},
			Env: map[string]string{
				"RUSTFS_ACCESS_KEY": s3AccessKey,
				"RUSTFS_SECRET_KEY": s3SecretKey,
			},
			// The probe is READINESS rather than liveness, and the difference is
			// not pedantic: /health answers 200 as soon as the process is up,
			// while /health/ready answers 503 until the storage behind the S3
			// API is initialized — measured on 1.0.0 in the first second after
			// start. Probing liveness makes the first request of the suite fail
			// at random; MinIO had the same gap under other names.
			WaitingFor: wait.ForHTTP("/health/ready").
				WithPort("9000/tcp").
				WithStartupTimeout(90 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err, "the S3 server could not be started; this suite needs Docker")

	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(ctr); err != nil {
			t.Logf("the S3 container could not be terminated: %v", err)
		}
	})

	host, err := ctr.Host(ctx)
	require.NoError(t, err)
	port, err := ctr.MappedPort(ctx, "9000/tcp")
	require.NoError(t, err)

	return &url.URL{Scheme: "http", Host: fmt.Sprintf("%s:%s", host, port.Port())}
}

// newLiveProvider builds a provider pointed at the container, and creates the
// bucket through the same signing path the provider uses.
//
// Creating the bucket with the provider's own credentials object is
// deliberate: if the signing were broken, the bucket creation would fail first
// and the failure would name the setup step rather than surfacing later as a
// confusing upload error.
func newLiveProvider(t *testing.T, endpoint *url.URL) *provider {
	t.Helper()

	creds := credentials{
		accessKeyID:     s3AccessKey,
		secretAccessKey: s3SecretKey,
		region:          s3Region,
	}

	p := &provider{
		bucket:    testBucket,
		endpoint:  endpoint,
		pathStyle: true, // what self-hosted S3 servers address by default
		baseURL:   endpoint.String() + "/" + testBucket,
		creds:     creds,
		client:    &http.Client{},
		now:       time.Now,
	}

	createBucketWhenReady(t, p)

	return p
}

// createBucketWhenReady retries past the server's initialization window.
//
// The readiness probe closes most of the race, but a server can answer it and
// still return 503 to the next S3 call — MinIO did, measured, and the retry is
// kept for any server that does. The retry is bounded and only covers 503 — a 403 is returned at
// once, because a signature fault must not be hidden behind a retry loop that
// eventually reports a timeout instead of the real cause.
func createBucketWhenReady(t *testing.T, p *provider) {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for {
		status, body := putBucket(t, p)
		if status < 300 {
			return
		}
		if status != http.StatusServiceUnavailable || time.Now().After(deadline) {
			require.Less(t, status, 300,
				"the bucket could not be created — if this is 403 SignatureDoesNotMatch, "+
					"the hand-written SigV4 is wrong and every other failure below is a consequence: %s",
				strings.TrimSpace(body))

			return
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// putBucket issues one signed bucket PUT and reports the outcome.
func putBucket(t *testing.T, p *provider) (status int, body string) {
	t.Helper()

	target := p.endpoint.String() + "/" + p.bucket
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, target, http.NoBody)
	require.NoError(t, err)

	p.creds.sign(req, hexSHA256(nil), p.now())

	resp, err := p.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test helper

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	return resp.StatusCode, string(raw)
}

// get fetches an object with no credentials at all.
func get(t *testing.T, rawURL string) (status int, body []byte) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, http.NoBody)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test helper

	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)

	return resp.StatusCode, body
}

// TestARealS3AcceptsTheSignature is the proof the whole file exists for.
//
// A 403 here means the canonical request this code builds is not the one the
// server rebuilds, and the unit tests cannot tell the difference.
func TestARealS3AcceptsTheSignature(t *testing.T) {
	p := newLiveProvider(t, startS3(t))

	const payload = "the bytes that were signed"

	file, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(payload),
	})

	require.NoError(t, err, "a real S3 implementation refused the hand-written signature")
	assert.Equal(t, int64(len(payload)), file.Size)
	assert.True(t, strings.HasSuffix(file.Key, ".png"))
	assert.Equal(t, coreprovider.ContentTypePNG, file.ContentType)
}

// TestTheStoredObjectIsTheOneThatWasSigned proves the payload hash is not
// merely well-formed but correct.
//
// S3 verifies x-amz-content-sha256 against the body it received. A hash
// computed over the wrong bytes — over the buffered file after a partial
// rewind, say — is rejected, so a passing upload is evidence that what was
// hashed and what was sent are the same thing.
func TestTheStoredObjectIsTheOneThatWasSigned(t *testing.T) {
	endpoint := startS3(t)
	p := newLiveProvider(t, endpoint)

	const payload = "content integrity is what the payload hash buys"

	file, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader(payload),
	})
	require.NoError(t, err)

	// The object is fetched with NO credentials, through the address the
	// provider returned. Reading it back is what proves the URL is durable in
	// the sense the contract asks for: it works without a signature.
	makeBucketPublic(t, p)

	status, body := get(t, file.URL)

	require.Equal(t, http.StatusOK, status,
		"the address the provider stored has to serve the object")
	assert.Equal(t, payload, string(body),
		"the object read back must be byte-identical to the one uploaded")
}

// TestAWrongSecretIsRefusedByTheServer proves the server really is validating.
//
// Without this the suite could pass against a store that ignores signatures
// entirely, and every other test here would be worthless.
func TestAWrongSecretIsRefusedByTheServer(t *testing.T) {
	endpoint := startS3(t)
	p := newLiveProvider(t, endpoint)

	p.creds.secretAccessKey = "not the secret this server knows"

	_, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("should never be stored"),
	})

	require.Error(t, err, "the server must reject a wrongly signed request; "+
		"if it does not, this suite proves nothing about the signature")
	assert.Equal(t, codeUploadFailed, coreerrors.CodeOf(err))
}

// TestDeleteRemovesTheObjectAndIsIdempotent proves the second call of the
// contract against a real store.
func TestDeleteRemovesTheObjectAndIsIdempotent(t *testing.T) {
	endpoint := startS3(t)
	p := newLiveProvider(t, endpoint)
	makeBucketPublic(t, p)

	file, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypePNG,
		Body:        strings.NewReader("to be removed"),
	})
	require.NoError(t, err)

	status, _ := get(t, file.URL)
	require.Equal(t, http.StatusOK, status, "the object has to exist before it is deleted")

	require.NoError(t, p.Delete(t.Context(), file.Key))

	status, _ = get(t, file.URL)
	assert.Equal(t, http.StatusNotFound, status, "the object has to be gone")

	// The contract requires idempotence, and a real store is where it counts:
	// the cleanup flow that calls Delete can be retried, and a second call
	// failing would make an already-orphaned record impossible to clean up.
	require.NoError(t, p.Delete(t.Context(), file.Key),
		"deleting an absent key must not be an error")
}

// TestAKeyPrefixKeepsTwoInstallationsApart proves the prefix reaches the stored
// key and the returned address together.
//
// A prefix applied to one and not the other is the quiet failure: the upload
// succeeds, the record is written, and the address points at an object that
// lives one path segment away.
func TestAKeyPrefixKeepsTwoInstallationsApart(t *testing.T) {
	endpoint := startS3(t)
	p := newLiveProvider(t, endpoint)
	p.prefix = "tenant-a"
	makeBucketPublic(t, p)

	file, err := p.Upload(t.Context(), coreprovider.UploadInput{
		ContentType: coreprovider.ContentTypeJPEG,
		Body:        strings.NewReader("prefixed"),
	})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(file.Key, "tenant-a/"), "the key carries the prefix")
	assert.Contains(t, file.URL, "/tenant-a/", "the address carries the same prefix")

	status, body := get(t, file.URL)
	require.Equal(t, http.StatusOK, status,
		"the prefixed address has to resolve; a prefix applied to only one of the two is a silent fault")
	assert.Equal(t, "prefixed", string(body))
}

// makeBucketPublic opens anonymous read on the bucket.
//
// This is what a real installation does with a CDN or a public asset bucket,
// and it is what makes the durable-address claim testable: an address that
// needs a signature to work is not the address the contract asks for.
func makeBucketPublic(t *testing.T, p *provider) {
	t.Helper()

	policy := fmt.Sprintf(`{
	  "Version": "2012-10-17",
	  "Statement": [{
	    "Effect": "Allow",
	    "Principal": {"AWS": ["*"]},
	    "Action": ["s3:GetObject"],
	    "Resource": ["arn:aws:s3:::%s/*"]
	  }]
	}`, p.bucket)

	target := p.endpoint.String() + "/" + p.bucket + "?policy="
	req, err := http.NewRequestWithContext(
		t.Context(), http.MethodPut, target, strings.NewReader(policy))
	require.NoError(t, err)
	req.ContentLength = int64(len(policy))

	p.creds.sign(req, hexSHA256([]byte(policy)), p.now())

	resp, err := p.client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test helper

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	require.Less(t, resp.StatusCode, 300,
		"the bucket policy could not be set: %s", strings.TrimSpace(string(body)))
}
