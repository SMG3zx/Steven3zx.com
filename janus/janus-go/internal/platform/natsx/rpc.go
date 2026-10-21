package natsx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"go.opentelemetry.io/otel/trace"
)

// DefaultRequestTimeout bounds a Call whose context has no deadline.
const DefaultRequestTimeout = 10 * time.Second

// The statuses this package itself answers or reports with, matching section 4
// of docs/architecture.md.
const (
	statusInvalid     = 400
	statusInternal    = 500
	statusUnavailable = 503
)

// RemoteError is a request that failed: the responder answered with the
// micro error headers, or nobody responded (Status 503).
type RemoteError struct {
	Status  int
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("%d %s", e.Status, e.Message)
}

// Call sends request as JSON on subject, waits for the answer and decodes it
// into reply, which may be nil. A failure the responder reported, or the
// absence of any responder, is a *RemoteError.
func Call(ctx context.Context, conn *nats.Conn, subject string, request, reply any) error {
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode request for %s: %w", subject, err)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultRequestTimeout)
		defer cancel()
	}
	msg, err := Request(ctx, conn, subject, data)
	if errors.Is(err, nats.ErrNoResponders) {
		return &RemoteError{Status: statusUnavailable, Message: "nothing answers " + subject}
	}
	if err != nil {
		return err
	}
	if code := msg.Header.Get(micro.ErrorCodeHeader); code != "" {
		status, convErr := strconv.Atoi(code)
		if convErr != nil {
			status = statusInternal
		}
		return &RemoteError{Status: status, Message: msg.Header.Get(micro.ErrorHeader)}
	}
	if reply == nil {
		return nil
	}
	if err := json.Unmarshal(msg.Data, reply); err != nil {
		return fmt.Errorf("decode reply from %s: %w", subject, err)
	}
	return nil
}

// statusCoder is an error that chooses the status it is answered with, such as
// contract.Error. Any other error is answered with 500.
type statusCoder interface{ StatusCode() int }

// AddEndpoint adds an endpoint named name on subject to service. Each request
// is decoded from JSON into Req, handled in a span that continues the caller's
// trace, and answered with Rep as JSON or with the error's status.
func AddEndpoint[Req, Rep any](service micro.Service, name, subject string, handle func(context.Context, Req) (Rep, error)) error {
	return service.AddEndpoint(name, micro.HandlerFunc(func(request micro.Request) {
		ctx := propagator.Extract(context.Background(), headerCarrier(request.Headers()))
		ctx, span := startSpan(ctx, "process", subject, trace.SpanKindServer)
		var in Req
		if err := json.Unmarshal(request.Data(), &in); err != nil {
			endSpan(span, err)
			_ = request.Error(strconv.Itoa(statusInvalid), "malformed request: "+err.Error(), nil)
			return
		}
		out, err := handle(ctx, in)
		endSpan(span, err)
		if err != nil {
			status := statusInternal
			var coded statusCoder
			if errors.As(err, &coded) {
				status = coded.StatusCode()
			}
			_ = request.Error(strconv.Itoa(status), err.Error(), nil)
			return
		}
		_ = request.RespondJSON(out)
	}), micro.WithEndpointSubject(subject))
}
