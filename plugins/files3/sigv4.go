package files3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"
	"time"
)

// AWS Signature Version 4, written by hand.
//
// The alternative was the AWS SDK, and it was rejected for the reason ADR 0014
// already settled for the error reporters: a plugin does not bring a
// dependency. The SDK is large, it pulls a transitive tree, and this provider
// uses exactly two of its operations. What is written here is the whole of
// SigV4 for those two calls and nothing else.
//
// The signing procedure is a fixed recipe and every step of it is a place where
// a mistake produces the SAME symptom: HTTP 403 SignatureDoesNotMatch. That
// symptom reads to an operator as "my credentials are wrong", which is why the
// steps below are commented with what each one is for — a wrong guess about the
// cause sends someone rotating keys that were never the problem.

// The signing algorithm's constants.
const (
	algorithm    = "AWS4-HMAC-SHA256"
	service      = "s3"
	terminator   = "aws4_request"
	amzDateFmt   = "20060102T150405Z"
	dateStampFmt = "20060102"
)

// credentials are the static credentials the provider signs with.
type credentials struct {
	accessKeyID string
	// secretAccessKey is the signing secret. It is NEVER logged and never put
	// into an error message.
	secretAccessKey string
	// sessionToken is set only for temporary credentials (STS). Empty for the
	// long-lived kind.
	sessionToken string
	region       string
}

// sign adds the SigV4 headers to the request in place.
//
// payloadHash is the hex-encoded SHA-256 of the body. The caller computes it
// because only the caller knows whether the body is in hand; passing an
// io.Reader here would make this function responsible for consuming a body it
// does not own.
//
// S3 also accepts the literal "UNSIGNED-PAYLOAD" in that position, which is
// tempting because it removes the need to know the body before sending. This
// provider does not use it: the body has to be known anyway (HTTP needs a
// Content-Length an io.Reader cannot give, so [provider.Upload] buffers first),
// and once it is buffered the hash costs one pass over bytes already in hand.
// Paying it buys end-to-end integrity — S3 verifies that the object it stored
// is the object that was signed.
//
// now is a parameter rather than a call to time.Now so the signature is
// reproducible in a test. A signing function that reads the clock itself can
// only be tested against itself.
func (c credentials) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate := now.Format(amzDateFmt)
	dateStamp := now.Format(dateStampFmt)

	// These three headers are part of what is signed, so they have to be on
	// the request BEFORE the canonical form is built. x-amz-date rather than
	// Date: the two can disagree after a proxy rewrites one of them, and S3
	// reads the x-amz- one.
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if c.sessionToken != "" {
		// A temporary credential's token must be signed too. Sending it
		// unsigned is accepted by some implementations and rejected by S3,
		// which is exactly the kind of difference that works in a test
		// environment and fails in production.
		req.Header.Set("X-Amz-Security-Token", c.sessionToken)
	}
	// Host is not in req.Header — Go keeps it in req.Host — but it MUST be
	// signed; a signature that does not cover the host can be replayed against
	// another bucket.
	if req.Host == "" {
		req.Host = req.URL.Host
	}

	signedHeaders, canonicalHeaders := canonicalize(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI(req.URL.EscapedPath()),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, c.region, service, terminator}, "/")

	stringToSign := strings.Join([]string{
		algorithm,
		amzDate,
		scope,
		hexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signature := hex.EncodeToString(hmacSHA256(c.signingKey(dateStamp), stringToSign))

	req.Header.Set("Authorization",
		algorithm+
			" Credential="+c.accessKeyID+"/"+scope+
			", SignedHeaders="+signedHeaders+
			", Signature="+signature)
}

// signingKey derives the day/region/service-scoped key.
//
// The chain is what makes a leaked signature bounded: the key that signed a
// request is usable only for that day, that region and S3, so a captured
// Authorization header cannot be replayed tomorrow or against another service.
func (c credentials) signingKey(dateStamp string) []byte {
	k := hmacSHA256([]byte("AWS4"+c.secretAccessKey), dateStamp)
	k = hmacSHA256(k, c.region)
	k = hmacSHA256(k, service)

	return hmacSHA256(k, terminator)
}

// canonicalize produces the SignedHeaders list and the CanonicalHeaders block.
//
// Only the headers listed in SignedHeaders are covered by the signature; a
// header sent but not signed is one a proxy may add, drop or rewrite without
// invalidating anything. Everything this provider sets is therefore signed.
func canonicalize(req *http.Request) (signedHeaders, canonicalHeaders string) {
	names := make([]string, 0, len(req.Header)+1)
	values := make(map[string]string, len(req.Header)+1)

	// host is signed even though Go does not keep it in the header map.
	names = append(names, "host")
	values["host"] = req.Host

	for name, vals := range req.Header {
		lower := strings.ToLower(name)
		// Authorization is the field being produced; signing it would be
		// circular. Content-Length is recomputed by intermediaries and is
		// excluded by the specification.
		if lower == "authorization" || lower == "content-length" {
			continue
		}
		names = append(names, lower)
		// Multiple values are joined with a comma; each is trimmed. The
		// trimming matters: a value with leading space signs differently from
		// the one the server sees after its own parsing.
		trimmed := make([]string, len(vals))
		for i, v := range vals {
			trimmed[i] = strings.TrimSpace(v)
		}
		values[lower] = strings.Join(trimmed, ",")
	}

	sort.Strings(names)

	var b strings.Builder
	for _, n := range names {
		b.WriteString(n)
		b.WriteString(":")
		b.WriteString(values[n])
		// Every header line ends with a newline, INCLUDING the last one. The
		// canonical form then has a blank line before SignedHeaders. Omitting
		// this newline is the single most common way to produce a signature
		// that is wrong in a way nothing explains.
		b.WriteString("\n")
	}

	return strings.Join(names, ";"), b.String()
}

// canonicalURI normalizes the path for signing.
//
// S3 is the exception among AWS services: its canonical URI is the path encoded
// ONCE, not twice. Applying the general rule here produces a signature that is
// correct for every other service and wrong for this one — and the failure is a
// 403 that says nothing about encoding.
//
// The path arrives already escaped from [net/url.URL.EscapedPath]; what is left
// is to make sure it is absolute.
func canonicalURI(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}

	return path
}

// hmacSHA256 is one link of the signing chain.
func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))

	return h.Sum(nil)
}

// hexSHA256 is the hash form the canonical request and the payload use.
func hexSHA256(b []byte) string {
	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:])
}
