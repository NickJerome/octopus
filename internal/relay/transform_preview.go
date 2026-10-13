package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/requesttransform"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/sjson"
)

type TransformPreviewInput struct {
	Stage            string                   `json:"stage"`
	Body             string                   `json:"body"` // Text keeps large integers intact through the browser.
	Context          requesttransform.Context `json:"context"`
	ParamOverride    string                   `json:"param_override"`
	RequestTransform json.RawMessage          `json:"request_transform"`
}

type TransformPreview struct {
	Before  string                   `json:"before"`
	After   string                   `json:"after"`
	Trace   []requesttransform.Trace `json:"trace"`
	Context requesttransform.Context `json:"context"`
}

func transformProtocol(protocol model.Protocol) string {
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		return requesttransform.Chat
	case model.ProtocolOpenAIResponse:
		return requesttransform.Responses
	case model.ProtocolAnthropicMessage:
		return requesttransform.Anthropic
	default:
		return ""
	}
}

func previewProtocol(protocol string) (model.Protocol, transformer.Inbound) {
	switch protocol {
	case requesttransform.Chat:
		return model.ProtocolOpenAIChatCompletion, openai.NewInboundTransformer()
	case requesttransform.Responses:
		return model.ProtocolOpenAIResponse, responses.NewInboundTransformer()
	case requesttransform.Anthropic:
		return model.ProtocolAnthropicMessage, anthropic.NewInboundTransformer()
	default:
		return 0, nil
	}
}

// PreviewRequestTransform constructs and edits a request locally; no HTTP client or channel credentials are used.
func PreviewRequestTransform(ctx context.Context, input TransformPreviewInput) (*TransformPreview, error) {
	if input.Stage != "client" && input.Stage != "upstream" {
		return nil, fmt.Errorf("stage must be client or upstream")
	}
	if len(input.Body) > requesttransform.MaxBodyBytes {
		return nil, fmt.Errorf("sample body is too large")
	}
	if _, err := requesttransform.Compile(input.RequestTransform); err != nil {
		return nil, err
	}
	if err := requesttransform.ValidateOverride(input.ParamOverride); err != nil {
		return nil, err
	}
	var metadata struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if err := json.Unmarshal([]byte(input.Body), &metadata); err != nil {
		return nil, fmt.Errorf("invalid sample body: %w", err)
	}
	if metadata.Model == "" {
		return nil, fmt.Errorf("sample body requires model")
	}
	meta := input.Context
	targetProtocol, _ := previewProtocol(meta.TargetProtocol)
	if targetProtocol == 0 {
		return nil, fmt.Errorf("invalid target protocol")
	}
	meta.Stream = metadata.Stream
	body := []byte(input.Body)
	if input.Stage == "upstream" {
		meta.TargetModel = metadata.Model
		if meta.SourceProtocol == "" {
			meta.SourceProtocol = meta.TargetProtocol
		}
		if meta.SourceModel == "" {
			meta.SourceModel = metadata.Model
		}
	} else {
		sourceProtocol, inbound := previewProtocol(meta.SourceProtocol)
		if sourceProtocol == 0 {
			return nil, fmt.Errorf("invalid source protocol")
		}
		if meta.TargetModel == "" {
			return nil, fmt.Errorf("target_model is required")
		}
		meta.SourceModel = metadata.Model
		var err error
		body, err = sjson.SetBytes(body, "model", meta.TargetModel)
		if err != nil {
			return nil, err
		}
		if meta.Stream && sourceProtocol == model.ProtocolOpenAIChatCompletion {
			body, err = sjson.SetBytes(body, "stream_options.include_usage", true)
			if err != nil {
				return nil, err
			}
		}
		if sourceProtocol != targetProtocol {
			// Only transformer construction is shared with forwarding; the placeholder endpoint is never contacted.
			channel := model.Channel{ChannelConfig: model.ChannelConfig{
				BaseURL: "https://preview.invalid", OpenAIChatCompletionPath: "/v1/chat/completions",
				OpenAIResponsePath: "/v1/responses", AnthropicMessagePath: "/v1/messages",
			}}
			outbound, _, _, err := buildOutbound(channel, model.ChannelGrant{Protocols: targetProtocol}, model.ChannelKey{}, targetProtocol)
			if err != nil {
				return nil, err
			}
			parsed, err := inbound.TransformRequest(ctx, &httpclient.Request{Method: http.MethodPost, Headers: http.Header{"Content-Type": {"application/json"}}, Body: body})
			if err != nil {
				return nil, fmt.Errorf("convert sample input: %w", err)
			}
			converted, err := outbound.TransformRequest(ctx, parsed)
			if err != nil {
				return nil, fmt.Errorf("convert sample output: %w", err)
			}
			body = converted.Body
		}
	}
	if !requesttransform.ValidProtocol(meta.SourceProtocol) {
		return nil, fmt.Errorf("invalid source protocol")
	}
	before := string(body)
	after, trace, err := requesttransform.Rewrite(ctx, input.ParamOverride, input.RequestTransform, body, meta)
	if err != nil {
		return nil, err
	}
	pretty := func(body string) string {
		var result bytes.Buffer
		if json.Indent(&result, []byte(body), "", "  ") == nil {
			return result.String()
		}
		return body
	}
	return &TransformPreview{Before: pretty(before), After: pretty(string(after)), Trace: trace, Context: meta}, nil
}
