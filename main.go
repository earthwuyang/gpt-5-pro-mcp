package main

import (
	"log"
	"os"

	"github.com/lox/gpt-5-pro-mcp/internal/client"
	"github.com/lox/gpt-5-pro-mcp/internal/fileops"
	"github.com/lox/gpt-5-pro-mcp/internal/server"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

func main() {
	// Check for OPENAI_API_KEY first, fall back to OPENROUTER_API_KEY
	apiKey := os.Getenv("OPENAI_API_KEY")
	baseURL := ""
	useResponsesAPI := true // Default to Responses API

	if apiKey == "" {
		// Fall back to OpenRouter configuration
		apiKey = os.Getenv("OPENROUTER_API_KEY")
		baseURL = os.Getenv("OPENROUTER_BASE_URL")

		if apiKey == "" {
			log.Fatal("Either OPENAI_API_KEY or OPENROUTER_API_KEY environment variable is required")
		}

		if baseURL == "" {
			baseURL = "https://openrouter.ai/api/v1"
		}

		log.Printf("Using OpenRouter with Chat Completions API at: %s", baseURL)
		useResponsesAPI = false // OpenRouter uses Chat Completions
	} else {
		// Check for custom OpenAI base URL (for aihubmix, etc.)
		baseURL = os.Getenv("OPENAI_BASE_URL")
		if baseURL != "" {
			log.Printf("Using custom OpenAI-compatible API with base URL: %s", baseURL)
			log.Printf("Custom base URL detected - using Chat Completions API (/v1/chat/completions)")
			useResponsesAPI = false // Custom endpoints use Chat Completions
		} else {
			log.Printf("Using official OpenAI API with Responses API (/v1/responses)")
		}
	}

	f := fileops.New()
	c := client.New(apiKey, baseURL, f, useResponsesAPI)
	s := server.New(c)

	if err := mcpserver.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
