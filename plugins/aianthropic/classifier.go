package aianthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/provider"
)

// Error codes.
const (
	// CodeRequestFailed reports that the call did not complete.
	CodeRequestFailed = "ai_anthropic_request_failed"
	// CodeUnexpectedStatus reports a non-2xx reply.
	CodeUnexpectedStatus = "ai_anthropic_unexpected_status"
	// CodeUnreadableAnswer reports a reply this plugin cannot turn into a
	// classification.
	CodeUnreadableAnswer = "ai_anthropic_unreadable_answer"
	// CodeLabelOutsideTheSet reports an answer that is not one of the labels
	// the caller offered.
	CodeLabelOutsideTheSet = "ai_anthropic_label_outside_the_set"
)

// apiVersion is the Messages API version header. It is pinned rather than
// tracked: an API version is a contract, and moving to a new one is a change
// somebody makes on purpose.
const apiVersion = "2023-06-01"

// maxTokens bounds the reply.
//
// The answer this plugin asks for is a label and one sentence, so the bound is
// small — and being small is a feature rather than a saving: a model that has
// started writing an essay has stopped answering the question, and the truncated
// reply then fails to parse instead of arriving as a confident wrong label.
const maxTokens = 300

// errorBodyLimit is how much of a failing reply is read for the message.
const errorBodyLimit = 2 << 10

// Classifier answers a closed question with the Messages API.
type Classifier struct {
	key    string
	model  string
	base   string
	client *http.Client
}

// ID returns the provider's identity.
func (c *Classifier) ID() string { return ProviderID }

// Classify asks the model and returns what it chose.
//
// # The answer format belongs to the PROVIDER
//
// The caller supplies the question and the labels; getting a parseable answer
// back is this plugin's problem, and it is solved here rather than in the
// contract because every provider solves it differently — one has structured
// output, another has tool calls, this one is asked for two lines. A contract
// that specified the mechanism would be a contract that only one provider could
// satisfy.
//
// # A label outside the set is a FAILURE, not a conclusion
//
// [provider.Classifier] says so and it matters most here, where the label is on
// its way into a column with a CHECK. Returning the model's word and letting
// the caller discover it cannot be stored would move the failure two layers
// away from the thing that caused it.
func (c *Classifier) Classify(
	ctx context.Context, in provider.ClassifyInput,
) (provider.Classification, error) {
	if len(in.Labels) < 2 {
		return provider.Classification{}, coreerrors.Invalid(CodeUnreadableAnswer,
			"a classification needs at least two labels to choose between; one label is "+
				"not a question")
	}

	body, err := json.Marshal(request{
		Model:     c.model,
		MaxTokens: maxTokens,
		System:    system(in.Labels),
		Messages: []message{{
			Role:    "user",
			Content: in.Instruction + "\n\n---\n" + in.Text + "\n---",
		}},
	})
	if err != nil {
		return provider.Classification{}, coreerrors.Wrap(err, coreerrors.KindInternal,
			CodeRequestFailed, "the request body could not be built")
	}

	answer, model, err := c.send(ctx, body)
	if err != nil {
		return provider.Classification{}, err
	}

	return read(answer, model, in.Labels)
}

// send performs the call and returns the answer text and the model that gave it.
func (c *Classifier) send(ctx context.Context, body []byte) (answer, model string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", "", coreerrors.Wrap(err, coreerrors.KindInternal, CodeRequestFailed,
			"the request could not be built")
	}

	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", apiVersion)
	// The key goes in a header and NOWHERE else: not in the URL, which lands in
	// proxy logs, and not in any error this package returns, which land in the
	// error reporter.
	req.Header.Set("x-api-key", c.key)

	res, err := c.client.Do(req)
	if err != nil {
		return "", "", coreerrors.Wrap(err, coreerrors.KindUnavailable, CodeRequestFailed,
			"the model could not be reached")
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		detail, _ := io.ReadAll(io.LimitReader(res.Body, errorBodyLimit))

		return "", "", statusError(res.StatusCode, strings.TrimSpace(string(detail)))
	}

	var decoded response
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return "", "", coreerrors.Wrap(err, coreerrors.KindInternal, CodeUnreadableAnswer,
			"the model's reply could not be decoded")
	}

	var text strings.Builder
	for _, block := range decoded.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}

	return text.String(), decoded.Model, nil
}

// statusError turns a non-2xx reply into the class that decides whether a
// caller should meet it again.
//
// 429 and 5xx are UNAVAILABLE — the question was fine and the service was not,
// which is the case a scheduled caller may sensibly retry next pass. Everything
// else is INTERNAL: a 400 or a 401 is a misconfiguration, and reporting it as a
// transient fault would let it repeat every quarter of an hour with nobody told
// it will never succeed.
//
// The body is included and the KEY is not: the reply says what was wrong with
// the request, and an operator reading the job's failure needs that sentence
// far more than they need to be told the call failed.
func statusError(status int, detail string) error {
	if status == http.StatusTooManyRequests || status >= http.StatusInternalServerError {
		return coreerrors.Unavailable(CodeUnexpectedStatus,
			"the model answered %d: %s", status, detail)
	}

	return coreerrors.Internal(CodeUnexpectedStatus,
		"the model answered %d: %s", status, detail)
}

// system is the format instruction, and it is the plugin's rather than the
// caller's.
func system(labels []string) string {
	return fmt.Sprintf(
		"Answer in exactly two lines and nothing else.\n"+
			"First line: LABEL: one of %s\n"+
			"Second line: REASON: one short sentence.",
		strings.Join(labels, ", "))
}

// read turns the answer into a classification, or fails.
func read(answer, model string, labels []string) (provider.Classification, error) {
	var label, reason string
	for _, line := range strings.Split(answer, "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, found := strings.CutPrefix(trimmed, "LABEL:"); found {
			label = strings.TrimSpace(rest)
		}
		if rest, found := strings.CutPrefix(trimmed, "REASON:"); found {
			reason = strings.TrimSpace(rest)
		}
	}

	chosen := ""
	for _, candidate := range labels {
		if strings.EqualFold(label, candidate) {
			// The CALLER's spelling is returned, not the model's. The caller is
			// about to put this in a column whose CHECK knows one casing, and
			// matching case-insensitively while returning what came back would
			// move the failure to the database.
			chosen = candidate
		}
	}
	if chosen == "" {
		return provider.Classification{}, coreerrors.Invalid(CodeLabelOutsideTheSet,
			"the model answered %q, which is not one of %s",
			label, strings.Join(labels, ", "))
	}
	if reason == "" {
		return provider.Classification{}, coreerrors.Invalid(CodeUnreadableAnswer,
			"the model chose %q and gave no reason; a proposal nobody can weigh is one "+
				"an operator can only ignore", chosen)
	}

	return provider.Classification{Label: chosen, Reason: reason, Model: model}, nil
}

// The wire types. They are the parts of the Messages API this plugin uses and
// no more; a field added upstream is ignored rather than breaking the decode.
type (
	request struct {
		Model     string    `json:"model"`
		MaxTokens int       `json:"max_tokens"`
		System    string    `json:"system"`
		Messages  []message `json:"messages"`
	}

	message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	response struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
)
