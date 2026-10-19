package authorization

import (
	"context"
	"embed"
	"fmt"

	"github.com/open-policy-agent/opa/v1/rego"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	actortrace "janus/actors/telemetry"
)

//go:embed policy.rego
var policyFS embed.FS

// Policy evaluates authorization checks against the embedded policy. The
// prepared query is safe for concurrent use, so checks never wait on each other.
type Policy struct{ query rego.PreparedEvalQuery }

// New prepares the embedded policy.
func New(ctx context.Context) (*Policy, error) {
	source, err := policyFS.ReadFile("policy.rego")
	if err != nil {
		return nil, err
	}
	query, err := rego.New(rego.Query("data.janus.authz.allow"), rego.Module("janus.rego", string(source))).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare authorization policy: %w", err)
	}
	return &Policy{query: query}, nil
}

// Allowed reports whether subject may perform action on resource. The policy
// denies anything without an explicit allow rule.
func (p *Policy) Allowed(ctx context.Context, subject, action string, resource map[string]any) (bool, error) {
	ctx, span := actortrace.StartSpan(ctx, "authorization.check", trace.WithSpanKind(trace.SpanKindInternal))
	defer span.End()
	input := map[string]any{"subject": subject, "action": action, "resource": resource}
	results, err := p.query.Eval(ctx, rego.EvalInput(input))
	allowed := false
	if err == nil && len(results) > 0 && len(results[0].Expressions) > 0 {
		allowed, _ = results[0].Expressions[0].Value.(bool)
	}
	actortrace.RecordError(span, err)
	span.SetAttributes(attribute.String("authorization.action", action), attribute.Bool("authorization.allowed", allowed))
	return allowed, err
}
