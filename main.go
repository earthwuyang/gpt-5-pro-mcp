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

		log.Printf("Using OpenRouter API with base URL: %s", baseURL)
	} else {
		log.Printf("Using OpenAI API")
	}

	f := fileops.New()
	c := client.New(apiKey, baseURL, f)
	s := server.New(c)

	if err := mcpserver.ServeStdio(s); err != nil {
		log.Fatal(err)
	}
}
