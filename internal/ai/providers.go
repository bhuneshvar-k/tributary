package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// OpenAI (also used by OpenRouter, which is OpenAI-compatible)
// ---------------------------------------------------------------------------

type openaiProvider struct {
	apiKey  string
	baseURL string
	model   string
	name    string
}

// NewOpenAI creates a provider for the OpenAI API.
func NewOpenAI(apiKey, model, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if model == "" {
		model = "gpt-4o"
	}
	return &openaiProvider{apiKey: apiKey, model: model, baseURL: baseURL, name: "openai"}
}

func (p *openaiProvider) Name() string            { return p.name }
func (p *openaiProvider) SupportsStreaming() bool { return true }

func (p *openaiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": req.SystemPrompt},
			{"role": "user", "content": req.UserPrompt},
		},
		"temperature": req.Temperature,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}

	return doOpenAICompatible(ctx, p.apiKey, p.baseURL+"/chat/completions", body, p.name, model)
}

// ---------------------------------------------------------------------------
// OpenRouter (OpenAI-compatible, different base URL)
// ---------------------------------------------------------------------------

// NewOpenRouter creates a provider for OpenRouter's OpenAI-compatible API.
func NewOpenRouter(apiKey, model string) Provider {
	if model == "" {
		model = "anthropic/claude-sonnet-4-20250514"
	}
	p := NewOpenAI(apiKey, model, "https://openrouter.ai/api/v1")
	// Override name after creation.
	return &openRouterAlias{Provider: p}
}

type openRouterAlias struct {
	Provider
}

func (p *openRouterAlias) Name() string { return "openrouter" }

// ---------------------------------------------------------------------------
// Claude (Anthropic Messages API)
// ---------------------------------------------------------------------------

type claudeProvider struct {
	apiKey  string
	model   string
	baseURL string
}

// NewClaude creates a provider for the Anthropic Messages API.
func NewClaude(apiKey, model, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &claudeProvider{apiKey: apiKey, model: model, baseURL: baseURL}
}

func (p *claudeProvider) Name() string            { return "claude" }
func (p *claudeProvider) SupportsStreaming() bool { return true }

type claudeRequest struct {
	Model     string          `json:"model"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (p *claudeProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	cr := claudeRequest{
		Model:     model,
		System:    req.SystemPrompt,
		Messages:  []claudeMessage{{Role: "user", Content: req.UserPrompt}},
		MaxTokens: maxTokens,
	}

	payload, err := json.Marshal(cr)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.baseURL + "/v1/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	start := time.Now()
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: "claude", Message: "request failed", Err: err}
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &ProviderError{Provider: "claude", Message: "read response", Err: err}
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, &ProviderError{
			Provider:   "claude",
			StatusCode: httpResp.StatusCode,
			Message:    extractErrorMessage(raw),
		}
	}

	var cr2 claudeResponse
	if err := json.Unmarshal(raw, &cr2); err != nil {
		return nil, &ProviderError{Provider: "claude", Message: "decode response", Err: err}
	}

	content := ""
	for _, c := range cr2.Content {
		if c.Type == "text" {
			content += c.Text
		}
	}

	return &Response{
		Content: content,
		Model:   cr2.Model,
		Tokens: TokenUsage{
			PromptTokens:     cr2.Usage.InputTokens,
			CompletionTokens: cr2.Usage.OutputTokens,
			TotalTokens:      cr2.Usage.InputTokens + cr2.Usage.OutputTokens,
		},
		Latency: time.Since(start),
		Raw:     raw,
	}, nil
}

// ---------------------------------------------------------------------------
// Gemini (Google Generative Language API)
// ---------------------------------------------------------------------------

type geminiProvider struct {
	apiKey  string
	model   string
	baseURL string
}

// NewGemini creates a provider for Google's Generative Language API.
func NewGemini(apiKey, model, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	if model == "" {
		model = "gemini-2.0-flash"
	}
	return &geminiProvider{apiKey: apiKey, model: model, baseURL: baseURL}
}

func (p *geminiProvider) Name() string            { return "gemini" }
func (p *geminiProvider) SupportsStreaming() bool { return true }

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent        `json:"systemInstruction,omitempty"`
	GenerationConfig *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float32 `json:"temperature,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
			Role  string       `json:"role"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	ModelVersion string `json:"modelVersion"`
}

func (p *geminiProvider) Complete(ctx context.Context, req *Request) (*Response, error) {
	model := p.model
	if req.Model != "" {
		model = req.Model
	}

	gr := geminiRequest{
		Contents: []geminiContent{
			{Parts: []geminiPart{{Text: req.UserPrompt}}},
		},
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		},
	}
	if req.MaxTokens > 0 || req.Temperature > 0 {
		gr.GenerationConfig = &geminiGenerationConfig{
			MaxOutputTokens: req.MaxTokens,
			Temperature:     req.Temperature,
		}
	}

	payload, err := json.Marshal(gr)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", p.baseURL, model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: "gemini", Message: "request failed", Err: err}
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &ProviderError{Provider: "gemini", Message: "read response", Err: err}
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, &ProviderError{
			Provider:   "gemini",
			StatusCode: httpResp.StatusCode,
			Message:    extractErrorMessage(raw),
		}
	}

	var gr2 geminiResponse
	if err := json.Unmarshal(raw, &gr2); err != nil {
		return nil, &ProviderError{Provider: "gemini", Message: "decode response", Err: err}
	}

	content := ""
	for _, c := range gr2.Candidates {
		for _, p := range c.Content.Parts {
			content += p.Text
		}
	}

	return &Response{
		Content: content,
		Model:   gr2.ModelVersion,
		Tokens: TokenUsage{
			PromptTokens:     gr2.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr2.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr2.UsageMetadata.TotalTokenCount,
		},
		Latency: time.Since(start),
		Raw:     raw,
	}, nil
}

// ---------------------------------------------------------------------------
// OpenCode (OpenAI-compatible local proxy)
// ---------------------------------------------------------------------------

// NewOpenCode creates a provider for the OpenCode local proxy.
// The base URL defaults to http://localhost:11434/v1 if not set.
func NewOpenCode(apiKey, model, baseURL string) Provider {
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	if model == "" {
		model = "claude-sonnet-4-20250514"
	}
	return &openaiProvider{apiKey: apiKey, model: model, baseURL: baseURL, name: "opencode"}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// doOpenAICompatible handles the OpenAI chat-completions wire format used by
// OpenAI, OpenRouter, and OpenCode.
func doOpenAICompatible(ctx context.Context, apiKey, url string, body map[string]any, providerName, model string) (*Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	start := time.Now()
	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, &ProviderError{Provider: providerName, Message: "request failed", Err: err}
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, &ProviderError{Provider: providerName, Message: "read response", Err: err}
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, &ProviderError{
			Provider:   providerName,
			StatusCode: httpResp.StatusCode,
			Message:    extractErrorMessage(raw),
		}
	}

	var ocr openaiChatResponse
	if err := json.Unmarshal(raw, &ocr); err != nil {
		return nil, &ProviderError{Provider: providerName, Message: "decode response", Err: err}
	}

	content := ""
	if len(ocr.Choices) > 0 {
		content = ocr.Choices[0].Message.Content
	}

	actualModel := ocr.Model
	if actualModel == "" {
		actualModel = model
	}

	return &Response{
		Content: content,
		Model:   actualModel,
		Tokens: TokenUsage{
			PromptTokens:     ocr.Usage.PromptTokens,
			CompletionTokens: ocr.Usage.CompletionTokens,
			TotalTokens:      ocr.Usage.TotalTokens,
		},
		Latency: time.Since(start),
		Raw:     raw,
	}, nil
}

// openaiChatResponse is the wire format for OpenAI-compatible chat completions.
type openaiChatResponse struct {
 Choices []struct {
 	Message struct {
 		Content string `json:"content"`
 	} `json:"message"`
 } `json:"choices"`
 Usage struct {
 	PromptTokens     int `json:"prompt_tokens"`
 	CompletionTokens int `json:"completion_tokens"`
 	TotalTokens      int `json:"total_tokens"`
 } `json:"usage"`
	Model string `json:"model"`
}

// extractErrorMessage tries to pull a human-readable error from a JSON body.
func extractErrorMessage(raw []byte) string {
	// Try common error response shapes.
	var errResp1 struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var errResp2 struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}

	if json.Unmarshal(raw, &errResp1) == nil && errResp1.Error.Message != "" {
		return errResp1.Error.Message
	}
	if json.Unmarshal(raw, &errResp2) == nil {
		if errResp2.Error != "" {
			return errResp2.Error
		}
		if errResp2.Message != "" {
			return errResp2.Message
		}
	}

	// Fallback: first 200 chars of raw body.
	s := string(raw)
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return strings.TrimSpace(s)
}

// NewProvider creates a provider from a config tuple. This is the single
// entry point the CLI uses — it validates the provider name and returns
// the correct constructor.
func NewProvider(name, apiKey, model, baseURL string) (Provider, error) {
	switch strings.ToLower(name) {
	case "claude", "anthropic":
		return NewClaude(apiKey, model, baseURL), nil
	case "openai":
		return NewOpenAI(apiKey, model, baseURL), nil
	case "gemini", "google":
		return NewGemini(apiKey, model, baseURL), nil
	case "openrouter":
		return NewOpenRouter(apiKey, model), nil
	case "opencode":
		return NewOpenCode(apiKey, model, baseURL), nil
	default:
		return nil, fmt.Errorf("unknown AI provider %q (valid: claude, openai, gemini, openrouter, opencode)", name)
	}
}
