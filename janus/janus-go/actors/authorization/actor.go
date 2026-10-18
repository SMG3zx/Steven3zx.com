package authorization

import (
	"embed"
	"fmt"

	"github.com/open-policy-agent/opa/v1/rego"
	goakt "github.com/tochemey/goakt/v4/actor"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
)

//go:embed policy.rego
var policyFS embed.FS

// Actor evaluates authorization checks using the embedded policy.
type Actor struct{ query rego.PreparedEvalQuery }

var _ goakt.Actor = (*Actor)(nil)

// New creates an authorization actor. Its policy is prepared when the actor starts.
func New() *Actor { return &Actor{} }

func (a *Actor) PreStart(ctx *goakt.Context) error {
	policy, err := policyFS.ReadFile("policy.rego")
	if err != nil {
		return err
	}
	a.query, err = rego.New(rego.Query("data.janus.authz.allow"), rego.Module("janus.rego", string(policy))).PrepareForEval(ctx.Context())
	if err != nil {
		return fmt.Errorf("prepare authorization policy: %w", err)
	}
	return nil
}

func (a *Actor) Receive(rctx *goakt.ReceiveContext) {
	ctx, span := actortrace.StartSpan(rctx.Context(), "actor.authorization.receive", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	span.SetAttributes(attribute.String("actor.type", "authorization"))
	msg, ok := rctx.Message().(*Check)
	if !ok {
		rctx.Unhandled()
		return
	}
	input := map[string]any{"subject": msg.Subject, "action": msg.Action, "resource": msg.Resource}
	results, err := a.query.Eval(ctx, rego.EvalInput(input))
	allowed := false
	if err == nil && len(results) > 0 && len(results[0].Expressions) > 0 {
		allowed, _ = results[0].Expressions[0].Value.(bool)
	}
	if err != nil {
		actortrace.RecordError(span, err)
	}
	span.SetAttributes(attribute.String("authorization.action", msg.Action), attribute.Bool("authorization.allowed", allowed))
	rctx.Response(&Decision{Allowed: allowed, Err: err})
}

func (*Actor) PostStop(*goakt.Context) error { return nil }
