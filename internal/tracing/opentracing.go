package tracing

import (
	"github.com/Jeffail/benthos/v3/lib/message"
	"github.com/Jeffail/benthos/v3/lib/types"
	"github.com/opentracing/opentracing-go"
)

// GetSpan returns a span attached to a message part. Returns nil if the part
// doesn't have a span attached.
func GetSpan(p types.Part) *Span {
	return openTracingSpan(opentracing.SpanFromContext(message.GetContext(p)))
}

// CreateChildSpan takes a message part, extracts an existing span if there is
// one and returns child span.
func CreateChildSpan(operationName string, part types.Part) *Span {
	span := GetSpan(part)
	if span == nil {
		span = openTracingSpan(opentracing.StartSpan(operationName))
	} else {
		span = openTracingSpan(opentracing.StartSpan(
			operationName,
			opentracing.ChildOf(span.unwrap().Context()),
		))
	}
	return span
}

// CreateChildSpans takes a message, extracts spans per message part and returns
// a slice of child spans. The length of the returned slice is guaranteed to
// match the message size.
func CreateChildSpans(operationName string, msg types.Message) []*Span {
	spans := make([]*Span, msg.Len())
	msg.Iter(func(i int, part types.Part) error {
		spans[i] = CreateChildSpan(operationName, part)
		return nil
	})
	return spans
}

// PartsWithChildSpans takes a slice of message parts, extracts spans per part,
// creates new child spans, and returns a new slice of parts with those spans
// embedded. The original parts are unchanged.
func PartsWithChildSpans(operationName string, parts []types.Part) ([]types.Part, []*Span) {
	spans := make([]*Span, 0, len(parts))
	newParts := make([]types.Part, len(parts))
	for i, part := range parts {
		if part == nil {
			continue
		}

		ctx := message.GetContext(part)
		otSpan := opentracing.SpanFromContext(ctx)
		if otSpan == nil {
			otSpan = opentracing.StartSpan(operationName)
		} else {
			otSpan = opentracing.StartSpan(
				operationName,
				opentracing.ChildOf(otSpan.Context()),
			)
		}
		ctx = opentracing.ContextWithSpan(ctx, otSpan)

		newParts[i] = message.WithContext(ctx, part)
		spans = append(spans, openTracingSpan(otSpan))
	}
	return newParts, spans
}

// WithChildSpans takes a message, extracts spans per message part, creates new
// child spans, and returns a new message with those spans embedded. The
// original message is unchanged.
func WithChildSpans(operationName string, msg types.Message) (types.Message, []*Span) {
	parts := make([]types.Part, 0, msg.Len())
	msg.Iter(func(i int, p types.Part) error {
		parts = append(parts, p)
		return nil
	})

	newParts, spans := PartsWithChildSpans(operationName, parts)
	newMsg := message.New(nil)
	newMsg.SetAll(newParts)

	return newMsg, spans
}

// WithSiblingSpans takes a message, extracts spans per message part, creates
// new sibling spans, and returns a new message with those spans embedded. The
// original message is unchanged.
func WithSiblingSpans(operationName string, msg types.Message) types.Message {
	parts := make([]types.Part, msg.Len())
	msg.Iter(func(i int, part types.Part) error {
		ctx := message.GetContext(part)
		otSpan := opentracing.SpanFromContext(ctx)
		if otSpan == nil {
			otSpan = opentracing.StartSpan(operationName)
		} else {
			otSpan = opentracing.StartSpan(
				operationName,
				opentracing.FollowsFrom(otSpan.Context()),
			)
		}
		ctx = opentracing.ContextWithSpan(ctx, otSpan)
		parts[i] = message.WithContext(ctx, part)
		return nil
	})

	newMsg := message.New(nil)
	newMsg.SetAll(parts)
	return newMsg
}

//------------------------------------------------------------------------------

// IterateWithChildSpans iterates all the parts of a message and, for each part,
// creates a new span from an existing span attached to the part and calls a
// func with that span before finishing the child span.
func IterateWithChildSpans(operationName string, msg types.Message, iter func(int, *Span, types.Part) error) error {
	return msg.Iter(func(i int, p types.Part) error {
		otSpan, _ := opentracing.StartSpanFromContext(message.GetContext(p), operationName)
		err := iter(i, openTracingSpan(otSpan), p)
		otSpan.Finish()
		return err
	})
}

// InitSpans sets up OpenTracing spans on each message part if one does not
// already exist.
func InitSpans(operationName string, msg types.Message) {
	tracedParts := make([]types.Part, msg.Len())
	msg.Iter(func(i int, p types.Part) error {
		tracedParts[i] = InitSpan(operationName, p)
		return nil
	})
	msg.SetAll(tracedParts)
}

// InitSpan sets up an OpenTracing span on a message part if one does not
// already exist.
func InitSpan(operationName string, part types.Part) types.Part {
	if GetSpan(part) != nil {
		return part
	}
	otSpan := opentracing.StartSpan(operationName)
	ctx := opentracing.ContextWithSpan(message.GetContext(part), otSpan)
	return message.WithContext(ctx, part)
}

// InitSpansFromParent sets up OpenTracing spans as children of a parent span on
// each message part if one does not already exist.
func InitSpansFromParent(operationName string, parent *Span, msg types.Message) {
	tracedParts := make([]types.Part, msg.Len())
	msg.Iter(func(i int, p types.Part) error {
		tracedParts[i] = InitSpanFromParent(operationName, parent, p)
		return nil
	})
	msg.SetAll(tracedParts)
}

// InitSpanFromParent sets up an OpenTracing span as children of a parent
// span on a message part if one does not already exist.
func InitSpanFromParent(operationName string, parent *Span, part types.Part) types.Part {
	if GetSpan(part) != nil {
		return part
	}
	span := opentracing.StartSpan(operationName, opentracing.ChildOf(parent.unwrap().Context()))
	ctx := opentracing.ContextWithSpan(message.GetContext(part), span)
	return message.WithContext(ctx, part)
}

// InitSpansFromParentTextMap obtains a span parent reference from a text map
// and creates child spans for each message.
func InitSpansFromParentTextMap(operationName string, textMapGeneric map[string]interface{}, msg types.Message) error {
	textMap := make(opentracing.TextMapCarrier, len(textMapGeneric))
	for k, v := range textMapGeneric {
		if vStr, ok := v.(string); ok {
			textMap[k] = vStr
		}
	}

	parentCtx, err := opentracing.GlobalTracer().Extract(opentracing.TextMap, textMap)
	if err != nil {
		return err
	}

	tracedParts := make([]types.Part, msg.Len())
	msg.Iter(func(i int, p types.Part) error {
		otSpan := opentracing.StartSpan(
			operationName,
			opentracing.ChildOf(parentCtx),
		)
		ctx := opentracing.ContextWithSpan(message.GetContext(p), otSpan)
		tracedParts[i] = message.WithContext(ctx, p)
		return nil
	})

	msg.SetAll(tracedParts)
	return nil
}

// FinishSpans calls Finish on all message parts containing a span.
func FinishSpans(msg types.Message) {
	msg.Iter(func(i int, p types.Part) error {
		span := GetSpan(p)
		if span == nil {
			return nil
		}
		span.unwrap().Finish()
		return nil
	})
}
