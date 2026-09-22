package testinfra

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/psyb0t/aichteeteapee"
	"github.com/psyb0t/ctxerrors"
)

// SystemOnePath is the endpoint the Jev adapter posts to. The fake answers
// on exactly this path so a wrong path in the adapter shows up as a 404
// rather than silently working.
const SystemOnePath = "/v1/systemone"

// DefaultFakeModel is the versioned model the fake claims answered. It
// differs from the "jev-latest" alias a caller asks for, which is what lets
// a test prove the resolved model, not the requested one, is recorded.
const DefaultFakeModel = "jev-1.13.0-fake"

// statusOverloaded is System One's non-standard overload status.
const statusOverloaded = 529

// Token counts the fake reports, so a test can assert exact usage.
const (
	FakeInputTokens  = 123
	FakeOutputTokens = 45
)

// FakeRequest is one captured provider call.
type FakeRequest struct {
	// Body is the decoded System One request.
	Body FakeSystemOneRequest

	// Authorization is the credential header the adapter sent. Tests assert
	// the adapter authenticates, and that the value never reaches a log.
	Authorization string
}

// FakeSystemOneRequest mirrors the request the adapter builds.
type FakeSystemOneRequest struct {
	State     any                         `json:"state"`
	Model     string                      `json:"model"`
	Questions map[string]FakeWireQuestion `json:"questions"`
}

// FakeWireQuestion is one question as it arrives on the wire.
type FakeWireQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

// FakeAnswer is one answer the fake returns.
type FakeAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Noul          *float64           `json:"noul,omitempty"`
	Confidence    *float64           `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

// FakeResponse is a complete System One reply.
type FakeResponse struct {
	Model   string                `json:"model"`
	Answers map[string]FakeAnswer `json:"answers"`
	Usage   FakeUsage             `json:"usage"`
}

// FakeUsage is the billed token report.
type FakeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// FakeReply is what the fake should do for one call. Exactly one of Status
// and Response drives the outcome: a non-zero Status produces that error
// response, otherwise Response is returned with 200.
type FakeReply struct {
	// Status, when non-zero, is the HTTP status to answer with.
	Status int

	// RetryAfter, when non-zero, is sent as the Retry-After header on an
	// error reply. It is how a test drives the adapter's backoff.
	RetryAfter time.Duration

	// Delay holds the response open, which is how a test exercises
	// timeouts and client cancellation.
	Delay time.Duration

	// Body, when non-nil, is returned verbatim instead of an encoded
	// Response. It is how a test sends malformed or oversized payloads.
	Body []byte

	// Response is the successful reply.
	Response FakeResponse
}

// FakeProvider is an HTTP server that speaks the System One contract.
//
// It is a faithful fake rather than a mock: the service under test reaches
// it over real HTTP through its real adapter, so serialization, status
// handling, retry behavior, and cancellation are all genuinely exercised.
type FakeProvider struct {
	server *http.Server
	port   int

	mu       sync.Mutex
	replies  []FakeReply
	fallback FakeReply
	requests []FakeRequest
}

// StartFakeProvider binds a fake provider on a host port the app container
// can reach.
//
// It listens on all interfaces, not loopback, because the consumer is a
// container reaching in through the host gateway.
func StartFakeProvider(port int, fallback FakeReply) (*FakeProvider, error) {
	fake := &FakeProvider{port: port, fallback: fallback}

	mux := http.NewServeMux()
	mux.HandleFunc(SystemOnePath, fake.handle)

	var listenConfig net.ListenConfig

	listener, err := listenConfig.Listen(
		context.Background(), "tcp", ":"+strconv.Itoa(port),
	)
	if err != nil {
		return nil, ctxerrors.Wrap(err, "bind the fake provider")
	}

	fake.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second, //nolint:mnd // test fixture
	}

	go func() {
		// The only expected end is Close, which returns ErrServerClosed.
		_ = fake.server.Serve(listener)
	}()

	return fake, nil
}

// Port is the host port the fake listens on.
func (f *FakeProvider) Port() int {
	return f.port
}

// URL is the base URL to configure the service with.
func (f *FakeProvider) URL() string {
	return ContainerURLForHostPort(f.port)
}

// Close stops the fake.
func (f *FakeProvider) Close() error {
	if err := f.server.Close(); err != nil {
		return ctxerrors.Wrap(err, "close the fake provider")
	}

	return nil
}

// Enqueue adds replies consumed in order, one per call. Once the queue is
// empty the fallback answers every further call. This is what lets a test
// script "fail twice, then succeed" and assert the retry actually happened.
func (f *FakeProvider) Enqueue(replies ...FakeReply) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.replies = append(f.replies, replies...)
}

// SetFallback replaces the reply used once the queue drains.
func (f *FakeProvider) SetFallback(reply FakeReply) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fallback = reply
}

// Requests returns a copy of every call the fake received.
func (f *FakeProvider) Requests() []FakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]FakeRequest(nil), f.requests...)
}

// CallCount is how many provider calls arrived, which is the assertion that
// distinguishes a retried request from a non-retried one.
func (f *FakeProvider) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.requests)
}

// Reset clears recorded calls and queued replies between subtests.
func (f *FakeProvider) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = nil
	f.replies = nil
}

func (f *FakeProvider) handle(w http.ResponseWriter, r *http.Request) {
	var body FakeSystemOneRequest

	decodeErr := json.NewDecoder(r.Body).Decode(&body)

	reply := f.record(FakeRequest{
		Body:          body,
		Authorization: r.Header.Get("Authorization"),
	})

	if decodeErr != nil {
		w.WriteHeader(http.StatusBadRequest)

		return
	}

	if !f.wait(r, reply.Delay) {
		return
	}

	f.write(w, reply)
}

// wait holds the response for the scripted delay, and reports false when the
// client gave up first. Returning without writing is what a cancelled call
// looks like to the adapter.
func (f *FakeProvider) wait(r *http.Request, delay time.Duration) bool {
	if delay <= 0 {
		return true
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-r.Context().Done():
		return false
	}
}

func (f *FakeProvider) write(w http.ResponseWriter, reply FakeReply) {
	if reply.RetryAfter > 0 {
		w.Header().Set(
			"Retry-After",
			strconv.Itoa(int(reply.RetryAfter.Seconds())),
		)
	}

	if len(reply.Body) > 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(replyStatus(reply))
		_, _ = w.Write(reply.Body)

		return
	}

	if reply.Status != 0 && reply.Status != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.Status)
		// A real provider echoes a message here. It deliberately contains
		// a marker so a test can prove the marker never reaches a caller.
		_, _ = w.Write([]byte(
			`{"error":{"message":"` + ProviderErrorMarker + `"}}`,
		))

		return
	}

	w.Header().Set(
		aichteeteapee.HeaderNameContentType, aichteeteapee.ContentTypeJSON,
	)
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(reply.Response); err != nil {
		slog.Error("fake provider failed to encode a reply", "err", err)
	}
}

// ProviderErrorMarker is planted in every fake error body. A test asserts it
// never appears in a Vibecheck response or log line, which is how the
// provider-message-leak guarantee is verified end to end.
const ProviderErrorMarker = "PROVIDER-ECHOED-STATE-DO-NOT-LEAK"

func replyStatus(reply FakeReply) int {
	if reply.Status == 0 {
		return http.StatusOK
	}

	return reply.Status
}

// record stores the call and pops the reply that answers it.
func (f *FakeProvider) record(request FakeRequest) FakeReply {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.requests = append(f.requests, request)

	if len(f.replies) == 0 {
		return f.fallback
	}

	reply := f.replies[0]
	f.replies = f.replies[1:]

	return reply
}

// OverloadedReply is the 529 the provider sends when it is saturated.
func OverloadedReply() FakeReply {
	return FakeReply{Status: statusOverloaded}
}

// UnauthorizedReply is the 401 a bad credential earns. It must never be
// retried, and a test asserts exactly that through CallCount.
func UnauthorizedReply() FakeReply {
	return FakeReply{Status: http.StatusUnauthorized}
}

// RateLimitedReply is a 429 carrying a Retry-After hint.
func RateLimitedReply(retryAfter time.Duration) FakeReply {
	return FakeReply{
		Status:     http.StatusTooManyRequests,
		RetryAfter: retryAfter,
	}
}

// NoulReply answers every noul question with the same value.
func NoulReply(questionIDs []string, value float64) FakeReply {
	answers := make(map[string]FakeAnswer, len(questionIDs))
	for _, id := range questionIDs {
		noul := value
		answers[id] = FakeAnswer{Type: "noul", Noul: &noul}
	}

	return FakeReply{Response: FakeResponse{
		Model:   DefaultFakeModel,
		Answers: answers,
		Usage: FakeUsage{
			InputTokens:  FakeInputTokens,
			OutputTokens: FakeOutputTokens,
		},
	}}
}

// AnswerReply builds a reply from explicit per-question answers.
func AnswerReply(answers map[string]FakeAnswer) FakeReply {
	return FakeReply{Response: FakeResponse{
		Model:   DefaultFakeModel,
		Answers: answers,
		Usage: FakeUsage{
			InputTokens:  FakeInputTokens,
			OutputTokens: FakeOutputTokens,
		},
	}}
}

// ChoiceAnswer builds one choice answer with its probability distribution.
//
// The caller supplies a probability for every option the question declares.
// Vibecheck rejects a partial distribution, so a fixture naming only the
// winning option would exercise the protocol-error path instead of the
// decision it means to test.
func ChoiceAnswer(
	choice string,
	probabilities map[string]float64,
) FakeAnswer {
	return FakeAnswer{
		Type:          "choice",
		Choice:        choice,
		Probabilities: probabilities,
	}
}

// NoulAnswer builds one noul answer. A noul carries no confidence: the value
// already is the graded belief.
func NoulAnswer(value float64) FakeAnswer {
	return FakeAnswer{Type: "noul", Noul: &value}
}

// ScoreAnswer builds one score answer. The caller adds the per-level
// probabilities and legend the contract requires.
func ScoreAnswer(value float64) FakeAnswer {
	return FakeAnswer{Type: "score", Score: &value}
}
