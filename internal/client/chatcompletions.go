package client

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	contextpkg "github.com/lox/gpt-5-pro-mcp/internal/context"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// ChatCompletionsClient handles communication with OpenAI Chat Completions API
// Used for custom endpoints (aihubmix, etc.) that don't support Responses API
type ChatCompletionsClient struct {
	client          *openai.Client
	fileOps         FileOps
	conversationHistory []openai.ChatCompletionMessageParamUnion
	baseURL         string
}

// NewChatCompletions creates a new ChatCompletionsClient instance
func NewChatCompletions(client *openai.Client, baseURL string, fileOps FileOps) *ChatCompletionsClient {
	return &ChatCompletionsClient{
		client:              client,
		fileOps:             fileOps,
		conversationHistory: []openai.ChatCompletionMessageParamUnion{},
		baseURL:             baseURL,
	}
}

// Handle processes a consultation request using Chat Completions API
func (c *ChatCompletionsClient) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt, err := request.RequireString("prompt")
	if err != nil {
		log.Printf("ERROR: Failed to get prompt: %v", err)
		return mcp.NewToolResultError(err.Error()), nil
	}

	continueConversation := request.GetBool("continue", true)
	gatheredContext := request.GetString("gathered_context", "")
	autoGatherContext := request.GetBool("auto_gather_context", true)

	log.Printf("[ChatCompletions] Received request: prompt_len=%d continue=%v auto_gather=%v has_context=%v",
		len(prompt), continueConversation, autoGatherContext, gatheredContext != "")

	// Phase 1: Context gathering logic
	if autoGatherContext && gatheredContext == "" {
		log.Printf("[ChatCompletions] Analyzing prompt for code references...")
		requirements := contextpkg.AnalyzePromptForReferences(prompt)

		if requirements.HasCodeRefs {
			log.Printf("[ChatCompletions] Found code references: files=%d functions=%d",
				len(requirements.Files), len(requirements.Functions))

			contextRequest := contextpkg.BuildContextRequest(requirements)
			responseText := contextpkg.FormatContextRequestAsText(contextRequest)

			log.Printf("[ChatCompletions] Returning context request to Claude Code")
			return mcp.NewToolResultText(responseText), nil
		}

		log.Printf("[ChatCompletions] No code references found, proceeding without context")
	}

	// Phase 2: Enrich prompt with gathered context if provided
	if gatheredContext != "" {
		log.Printf("[ChatCompletions] Enriching prompt with gathered context: len=%d", len(gatheredContext))
		enrichedPrompt, err := contextpkg.EnrichPromptWithContext(prompt, gatheredContext)
		if err != nil {
			log.Printf("[ChatCompletions] ERROR: Failed to enrich prompt: %v", err)
			return mcp.NewToolResultError(fmt.Sprintf("Failed to process gathered_context: %v", err)), nil
		}
		prompt = enrichedPrompt
		log.Printf("[ChatCompletions] Prompt enriched: new_len=%d", len(prompt))
	}

	// Start fresh if continue is false
	if !continueConversation {
		log.Printf("[ChatCompletions] Starting fresh conversation")
		c.conversationHistory = []openai.ChatCompletionMessageParamUnion{}
	} else if len(c.conversationHistory) > 0 {
		log.Printf("[ChatCompletions] Continuing conversation: history_len=%d", len(c.conversationHistory))
	}

	// Build messages array
	messages := []openai.ChatCompletionMessageParamUnion{}

	// Add system message if starting fresh or first message
	if len(c.conversationHistory) == 0 {
		messages = append(messages, openai.SystemMessage(buildSystemPrompt()))
	} else {
		// Add conversation history
		messages = append(messages, c.conversationHistory...)
	}

	// Add current user message
	messages = append(messages, openai.UserMessage(prompt))

	// Build tools
	tools := c.buildChatTools()

	// Call Chat Completions API with tool support
	log.Printf("[ChatCompletions] Calling Chat Completions API: model=%s", defaultModel)

	for iteration := 0; iteration < maxIterations; iteration++ {
		params := openai.ChatCompletionNewParams{
			Model:    defaultModel,
			Messages: messages,
		}

		if len(tools) > 0 {
			params.Tools = tools
		}

		completion, err := c.client.Chat.Completions.New(ctx, params)
		if err != nil {
			log.Printf("[ChatCompletions] ERROR: API call failed: %v", err)
			return mcp.NewToolResultError(fmt.Sprintf("Chat Completions API error: %v", err)), nil
		}

		if len(completion.Choices) == 0 {
			log.Printf("[ChatCompletions] ERROR: No choices in response")
			return mcp.NewToolResultError("No response from API"), nil
		}

		choice := completion.Choices[0]
		message := choice.Message

		// Add assistant message to history
		messages = append(messages, openai.AssistantMessage(message.Content))

		// Check if there are tool calls
		if len(message.ToolCalls) == 0 {
			// No tool calls, return the response
			log.Printf("[ChatCompletions] No tool calls, returning response: len=%d", len(message.Content))

			// Save conversation history
			c.conversationHistory = messages

			return mcp.NewToolResultText(message.Content), nil
		}

		// Execute tool calls
		log.Printf("[ChatCompletions] Iteration %d: found %d tool calls", iteration+1, len(message.ToolCalls))

		for _, toolCall := range message.ToolCalls {
			log.Printf("[ChatCompletions] Executing tool: name=%s id=%s", toolCall.Function.Name, toolCall.ID)

			result, err := c.executeFunction(ctx, toolCall.Function.Name, toolCall.Function.Arguments)
			if err != nil {
				log.Printf("[ChatCompletions] Tool execution error: %v", err)
				result = fmt.Sprintf("Error: %v", err)
			} else {
				log.Printf("[ChatCompletions] Tool execution success: result_len=%d", len(result))
			}

			// Add tool response to messages
			messages = append(messages, openai.ToolMessage(toolCall.ID, result))
		}

		// Continue loop to get next response
	}

	log.Printf("[ChatCompletions] ERROR: Max iterations (%d) reached", maxIterations)
	return mcp.NewToolResultError("Max function call iterations reached"), nil
}

// buildChatTools defines the tools for Chat Completions API
func (c *ChatCompletionsClient) buildChatTools() []openai.ChatCompletionToolParam {
	return []openai.ChatCompletionToolParam{
		{
			Type: "function",
			Function: shared.FunctionDefinitionParam{
				Name:        "read_file",
				Description: openai.Opt("Read the contents of any file from the filesystem"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{
							"type":        "string",
							"description": "Path to the file to read (supports ~ for home directory)",
						},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: shared.FunctionDefinitionParam{
				Name:        "grep_files",
				Description: openai.Opt("Search for patterns in files using regex and glob patterns"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]interface{}{
						"pattern": map[string]interface{}{
							"type":        "string",
							"description": "Regular expression pattern to search for",
						},
						"path": map[string]interface{}{
							"type":        "string",
							"description": "File path or glob pattern (e.g., '*.go', 'src/**/*.js')",
						},
						"ignore_case": map[string]interface{}{
							"type":        "boolean",
							"description": "Perform case-insensitive search (default: false)",
						},
					},
					"required": []string{"pattern", "path"},
				},
			},
		},
	}
}

// executeFunction executes a function call requested by the model
func (c *ChatCompletionsClient) executeFunction(ctx context.Context, name, argsJSON string) (string, error) {
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
