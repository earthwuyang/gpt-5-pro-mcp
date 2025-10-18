package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	contextpkg "github.com/lox/gpt-5-pro-mcp/internal/context"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
)

func init() {
	// Configure logging to stderr
	log.SetOutput(os.Stderr)
	log.SetPrefix("[gpt-5-pro-mcp] ")
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
}

const (
	defaultModel  = "gpt-5-pro"
	maxIterations = 10 // Limit function call iterations
)

// FileOps defines the interface for file operations
type FileOps interface {
	ReadFile(ctx context.Context, path string) (string, error)
	GrepFiles(ctx context.Context, pattern, path string, ignoreCase bool) (string, error)
}

// GPT5ProClient handles communication with OpenAI's Responses API
type GPT5ProClient struct {
	client     *openai.Client
	fileOps    FileOps
	responseID string
	baseURL    string
	mu         sync.RWMutex
	chatClient *ChatCompletionsClient
	useResponsesAPI bool
}

// New creates a new GPT5ProClient instance
// If useResponsesAPI is false, it will use Chat Completions API instead
func New(apiKey string, baseURL string, fileOps FileOps, useResponsesAPI bool) *GPT5ProClient {
	opts := []option.RequestOption{option.WithAPIKey(apiKey)}

	// Add custom base URL if provided (for OpenRouter or other providers)
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
		log.Printf("Initializing client with custom base URL: %s", baseURL)
		if useResponsesAPI {
			log.Printf("WARNING: Using Responses API which may not be compatible with all providers")
		}
	}

	client := openai.NewClient(opts...)

	gpt5ProClient := &GPT5ProClient{
		client:          &client,
		fileOps:         fileOps,
		baseURL:         baseURL,
		useResponsesAPI: useResponsesAPI,
	}

	// If not using Responses API, create Chat Completions client
	if !useResponsesAPI {
		log.Printf("Using Chat Completions API for compatibility")
		gpt5ProClient.chatClient = NewChatCompletions(&client, baseURL, fileOps)
	}

	return gpt5ProClient
}

// Handle processes a consultation request using appropriate API (Responses or Chat Completions)
func (c *GPT5ProClient) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Route to Chat Completions if configured
	if !c.useResponsesAPI && c.chatClient != nil {
		return c.chatClient.Handle(ctx, request)
	}

	// Otherwise use Responses API
	prompt, err := request.RequireString("prompt")
	if err != nil {
		log.Printf("ERROR: Failed to get prompt: %v", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	continueConversation := request.GetBool("continue", true)
	gatheredContext := request.GetString("gathered_context", "")
	autoGatherContext := request.GetBool("auto_gather_context", true)

	log.Printf("[ResponsesAPI] Received request: prompt_len=%d continue=%v auto_gather=%v has_context=%v",
		len(prompt), continueConversation, autoGatherContext, gatheredContext != "")

	// Phase 1: Context gathering logic
	if autoGatherContext && gatheredContext == "" {
		log.Printf("[ResponsesAPI] Analyzing prompt for code references...")
		requirements := contextpkg.AnalyzePromptForReferences(prompt)

		if requirements.HasCodeRefs {
			log.Printf("[ResponsesAPI] Found code references: files=%d functions=%d",
				len(requirements.Files), len(requirements.Functions))

			contextRequest := contextpkg.BuildContextRequest(requirements)
			responseText := contextpkg.FormatContextRequestAsText(contextRequest)

			log.Printf("[ResponsesAPI] Returning context request to Claude Code")
			return mcp.NewToolResultText(responseText), nil
		}

		log.Printf("[ResponsesAPI] No code references found, proceeding without context")
	}

	// Phase 2: Enrich prompt with gathered context if provided
	if gatheredContext != "" {
		log.Printf("[ResponsesAPI] Enriching prompt with gathered context: len=%d", len(gatheredContext))
		enrichedPrompt, err := contextpkg.EnrichPromptWithContext(prompt, gatheredContext)
		if err != nil {
			log.Printf("[ResponsesAPI] ERROR: Failed to enrich prompt: %v", err)
			return mcp.NewToolResultError(fmt.Sprintf("Failed to process gathered_context: %v", err)), nil
		}
		prompt = enrichedPrompt
		log.Printf("[ResponsesAPI] Prompt enriched: new_len=%d", len(prompt))
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Start fresh if continue is false
	if !continueConversation {
		log.Printf("Starting fresh conversation")
		c.responseID = ""
	} else if c.responseID != "" {
		log.Printf("Continuing conversation: response_id=%s", c.responseID)
	}

	// Build the request parameters
	params := responses.ResponseNewParams{
		Model:        defaultModel,
		Instructions: openai.Opt(buildSystemPrompt()),
		Tools:        c.buildTools(),
	}

	// Add input message
	inputItems := responses.ResponseInputParam{
		responses.ResponseInputItemParamOfMessage(prompt, responses.EasyInputMessageRoleUser),
	}
	params.Input = responses.ResponseNewParamsInputUnion{
		OfInputItemList: inputItems,
	}

	// Add previous response ID if continuing
	if continueConversation && c.responseID != "" {
		params.PreviousResponseID = openai.Opt(c.responseID)
	}

	// Call OpenAI Responses API
	log.Printf("Calling OpenAI Responses API: model=%s", defaultModel)
	response, err := c.client.Responses.New(ctx, params)
	if err != nil {
		log.Printf("ERROR: OpenAI API call failed: %v", err)

		// Provide helpful error message for OpenRouter users
		if c.baseURL != "" {
			return mcp.NewToolResultError(fmt.Sprintf(
				"API error: %v\n\n"+
					"COMPATIBILITY NOTE: This MCP server uses OpenAI's Responses API (/v1/responses) which is NOT supported by OpenRouter.\n"+
					"OpenRouter only supports the Chat Completions API (/v1/chat/completions).\n\n"+
					"Solutions:\n"+
					"1. Use OpenAI API directly (set OPENAI_API_KEY instead of OPENROUTER_API_KEY)\n"+
					"2. Wait for a future version with Chat Completions support\n"+
					"3. Use a provider that supports the Responses API",
				err,
			)), nil
		}

		return mcp.NewToolResultError(fmt.Sprintf("OpenAI API error: %v", err)), nil
	}

	// Save the response ID for conversation continuity
	c.responseID = response.ID
	log.Printf("Received response: id=%s status=%s", response.ID, response.Status)

	// Handle tool calls in a loop
	for i := 0; i < maxIterations; i++ {
		// Check if there are tool calls to execute
		toolCalls := extractToolCalls(response)
		log.Printf("Iteration %d: found %d tool calls", i+1, len(toolCalls))

		if len(toolCalls) == 0 {
			// No more tool calls, extract and return final text response
			text := extractTextContent(response)
			log.Printf("No tool calls, returning text response: len=%d", len(text))
			if text == "" {
				log.Printf("ERROR: No text content in response")
				return mcp.NewToolResultError("No text content in response"), nil
			}
			return mcp.NewToolResultText(text), nil
		}

		// Execute tool calls
		toolOutputs := make(responses.ResponseInputParam, 0, len(toolCalls))
		for _, toolCall := range toolCalls {
			log.Printf("Executing tool: name=%s id=%s args_len=%d", toolCall.Name, toolCall.ID, len(toolCall.Arguments))
			result, err := c.executeFunction(ctx, toolCall.Name, toolCall.Arguments)
			if err != nil {
				log.Printf("Tool execution error: %v", err)
				result = fmt.Sprintf("Error: %v", err)
			} else {
				log.Printf("Tool execution success: result_len=%d", len(result))
			}

			toolOutputs = append(toolOutputs, responses.ResponseInputItemParamOfFunctionCallOutput(toolCall.ID, result))
		}

		// Continue the response with tool outputs
		log.Printf("Continuing with %d tool outputs", len(toolOutputs))
		params = responses.ResponseNewParams{
			Model:              defaultModel,
			PreviousResponseID: openai.Opt(response.ID),
			Input: responses.ResponseNewParamsInputUnion{
				OfInputItemList: toolOutputs,
			},
			Tools: c.buildTools(),
		}

		response, err = c.client.Responses.New(ctx, params)
		if err != nil {
			log.Printf("ERROR: Follow-up API call failed: %v", err)
			return mcp.NewToolResultError(fmt.Sprintf("OpenAI API error: %v", err)), nil
		}

		// Update response ID
		c.responseID = response.ID
		log.Printf("Updated response: id=%s status=%s", response.ID, response.Status)
	}

	log.Printf("ERROR: Max iterations (%d) reached", maxIterations)
	return mcp.NewToolResultError("Max function call iterations reached"), nil
}

// buildTools defines the tools available to the model
func (c *GPT5ProClient) buildTools() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		responses.ToolParamOfFunction(
			"read_file",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "Path to the file to read (supports ~ for home directory)",
					},
				},
				"required": []string{"path"},
			},
			false, // strict
		),
		responses.ToolParamOfFunction(
			"grep_files",
			map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pattern": map[string]any{
						"type":        "string",
						"description": "Regular expression pattern to search for",
					},
					"path": map[string]any{
						"type":        "string",
						"description": "File path or glob pattern (e.g., '*.go', 'src/**/*.js')",
					},
					"ignore_case": map[string]any{
						"type":        "boolean",
						"description": "Perform case-insensitive search (default: false)",
					},
				},
				"required": []string{"pattern", "path"},
			},
			false, // strict
		),
	}
}

// executeFunction executes a function call requested by the model
func (c *GPT5ProClient) executeFunction(ctx context.Context, name, argsJSON string) (string, error) {
	switch name {
	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		return c.fileOps.ReadFile(ctx, args.Path)

	case "grep_files":
		var args struct {
			Pattern    string `json:"pattern"`
			Path       string `json:"path"`
			IgnoreCase bool   `json:"ignore_case"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("invalid arguments: %w", err)
		}
		return c.fileOps.GrepFiles(ctx, args.Pattern, args.Path, args.IgnoreCase)

	default:
		return "", fmt.Errorf("unknown function: %s", name)
	}
}

// ToolCall represents a function tool call
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// extractToolCalls extracts tool calls from a response
func extractToolCalls(response *responses.Response) []ToolCall {
	var toolCalls []ToolCall

	log.Printf("Extracting tool calls from %d output items", len(response.Output))
	for i, item := range response.Output {
		log.Printf("Output item %d: type=%s", i, item.Type)
		if item.Type == "function_call" {
			toolCalls = append(toolCalls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: item.Arguments,
			})
			log.Printf("Found function call: name=%s id=%s", item.Name, item.CallID)
		}
	}

	return toolCalls
}

// extractTextContent extracts text content from a response
func extractTextContent(response *responses.Response) string {
	var textParts []string

	log.Printf("Extracting text content from %d output items", len(response.Output))
	for i, item := range response.Output {
		log.Printf("Output item %d: type=%s content_items=%d", i, item.Type, len(item.Content))
		if item.Type == "message" {
			for j, contentItem := range item.Content {
				log.Printf("  Content item %d: type=%s", j, contentItem.Type)
				// The Responses API uses "output_text" not "text"
				if contentItem.Type == "text" || contentItem.Type == "output_text" {
					textParts = append(textParts, contentItem.Text)
					log.Printf("  Found text: len=%d", len(contentItem.Text))
				}
			}
		}
	}

	result := ""
	for _, part := range textParts {
		if result != "" {
			result += "\n"
		}
		result += part
	}

	log.Printf("Extracted %d text parts, total length=%d", len(textParts), len(result))
	return result
}

// buildSystemPrompt creates the system prompt
func buildSystemPrompt() string {
	return `You are a GPT-5-Pro powered assistant - an expert problem-solving AI consulted for the most challenging and complex problems.

Your role is to provide deep, systematic analysis through multi-step reasoning:

1. **Problem Decomposition**: Break down complex problems into manageable components
2. **Hypothesis Generation**: Form clear theories about root causes or solutions
3. **Evidence Gathering**: Identify what information is needed and what conclusions can be drawn
4. **Systematic Investigation**: Work through problems methodically, step by step
5. **Confidence Assessment**: Honestly evaluate certainty levels at each stage
6. **Iterative Refinement**: Build on previous findings to reach comprehensive understanding

When analyzing problems:
- Think deeply and systematically
- Question assumptions
- Consider multiple perspectives
- Identify gaps in understanding
- Provide clear, actionable insights
- Acknowledge uncertainty when appropriate
- Suggest concrete next steps

Your responses should be:
- **Thorough**: Cover all relevant aspects
- **Clear**: Easy to understand and act upon
- **Structured**: Organized logically
- **Evidence-based**: Grounded in facts and reasoning
- **Actionable**: Include concrete recommendations

**Available Tools**:
You have access to the following tools to gather information:
- read_file: Read the contents of any file from the filesystem
- grep_files: Search for patterns in files using regex and glob patterns

Use these tools proactively to gather evidence and verify your hypotheses. Don't hesitate to read files or search codebases when it helps your analysis.

You are being consulted because standard approaches have proven insufficient. Bring your full analytical capabilities to bear on each problem.`
}
