package context

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ContextRequest represents a request for specific code context
type ContextRequest struct {
	Type     string `json:"type"`     // "file_content", "function_implementation", "call_graph"
	Path     string `json:"path"`     // File path
	Function string `json:"function,omitempty"` // Function/method name
	Lines    string `json:"lines,omitempty"`    // Line range like "116-230"
	Reason   string `json:"reason"`   // Why this context is needed
}

// ContextRequirements holds all detected code references
type ContextRequirements struct {
	Files     []string          // File paths mentioned
	Functions map[string]string // function name -> file path
	LineRefs  map[string]string // file path -> line ranges
	HasCodeRefs bool             // Whether any code references were found
}

// ContextResponse is returned when context is needed
type ContextResponse struct {
	Status          string            `json:"status"`  // "context_needed"
	Message         string            `json:"message"` // Human-readable message
	ContextRequests []ContextRequest  `json:"context_requests"`
}

// GatheredContext holds the code context provided by Claude Code
type GatheredContext struct {
	Files     map[string]string `json:"files"`     // path -> content
	Functions map[string]string `json:"functions"` // name -> implementation
	Metadata  map[string]string `json:"metadata"`  // logs, errors, etc.
}

var (
	// Regex patterns for detecting code references
	filePathPattern    = regexp.MustCompile(`\b([a-zA-Z0-9_/\-\.]+\.(py|js|ts|go|java|rb|php|cpp|c|h|rs|kt|swift|tsx|jsx))\b`)
	functionCallPattern = regexp.MustCompile(`\b([a-zA-Z_][a-zA-Z0-9_]*)\s*\(`)
	lineNumberPattern  = regexp.MustCompile(`(?:lines?|line)\s+(\d+(?:-\d+)?)|:(\d+)`)
	classNamePattern   = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9_]*)\b`)
)

// AnalyzePromptForReferences extracts code references from a prompt
func AnalyzePromptForReferences(prompt string) *ContextRequirements {
	req := &ContextRequirements{
		Files:     []string{},
		Functions: make(map[string]string),
		LineRefs:  make(map[string]string),
		HasCodeRefs: false,
	}

	// Extract file paths
	fileMatches := filePathPattern.FindAllStringSubmatch(prompt, -1)
	for _, match := range fileMatches {
		if len(match) > 1 {
			filePath := match[1]
			if !contains(req.Files, filePath) {
				req.Files = append(req.Files, filePath)
				req.HasCodeRefs = true
			}
		}
	}

	// Extract function calls
	funcMatches := functionCallPattern.FindAllStringSubmatch(prompt, -1)
	for _, match := range funcMatches {
		if len(match) > 1 {
			funcName := match[1]
			// Common words that aren't functions
			if !isCommonWord(funcName) {
				req.Functions[funcName] = "" // Will be mapped to file later
				req.HasCodeRefs = true
			}
		}
	}

	// Extract line number references
	lineMatches := lineNumberPattern.FindAllStringSubmatch(prompt, -1)
	if len(lineMatches) > 0 && len(req.Files) > 0 {
		// Associate line numbers with the most recently mentioned file
		lastFile := req.Files[len(req.Files)-1]
		for _, match := range lineMatches {
			lineRef := ""
			if match[1] != "" {
				lineRef = match[1]
			} else if match[2] != "" {
				lineRef = match[2]
			}
			if lineRef != "" {
				req.LineRefs[lastFile] = lineRef
				req.HasCodeRefs = true
			}
		}
	}

	// Check for code-related keywords
	codeKeywords := []string{"function", "class", "method", "implementation", "code", "file", "module"}
	lowerPrompt := strings.ToLower(prompt)
	for _, keyword := range codeKeywords {
		if strings.Contains(lowerPrompt, keyword) {
			req.HasCodeRefs = true
			break
		}
	}

	return req
}

// BuildContextRequest creates a structured context request
func BuildContextRequest(requirements *ContextRequirements) *ContextResponse {
	requests := []ContextRequest{}

	// Request file contents
	for _, filePath := range requirements.Files {
		request := ContextRequest{
			Type:   "file_content",
			Path:   filePath,
			Reason: "file mentioned in prompt",
		}

		// Add line range if specified
		if lineRange, ok := requirements.LineRefs[filePath]; ok {
			request.Lines = lineRange
			request.Type = "file_section"
			request.Reason = fmt.Sprintf("lines %s mentioned in prompt", lineRange)
		}

		requests = append(requests, request)
	}

	// Request function implementations
	for funcName, filePath := range requirements.Functions {
		request := ContextRequest{
			Type:     "function_implementation",
			Function: funcName,
			Reason:   "function referenced in prompt",
		}
		if filePath != "" {
			request.Path = filePath
		}
		requests = append(requests, request)
	}

	return &ContextResponse{
		Status: "context_needed",
		Message: "To provide accurate analysis, I need to see the actual code. Please gather the following context and re-call with gathered_context parameter:",
		ContextRequests: requests,
	}
}

// EnrichPromptWithContext merges gathered context into the original prompt
func EnrichPromptWithContext(prompt string, contextJSON string) (string, error) {
	if contextJSON == "" {
		return prompt, nil
	}

	var context GatheredContext
	if err := json.Unmarshal([]byte(contextJSON), &context); err != nil {
		return "", fmt.Errorf("invalid gathered_context JSON: %w", err)
	}

	var enriched strings.Builder

	// Original question at top
	enriched.WriteString("# ORIGINAL QUESTION\n\n")
	enriched.WriteString(prompt)
	enriched.WriteString("\n\n")

	// File contents section
	if len(context.Files) > 0 {
		enriched.WriteString("# RELEVANT CODE CONTEXT\n\n")
		for path, content := range context.Files {
			enriched.WriteString(fmt.Sprintf("## File: %s\n\n", path))
			enriched.WriteString("```\n")
			enriched.WriteString(content)
			enriched.WriteString("\n```\n\n")
		}
	}

	// Function implementations section
	if len(context.Functions) > 0 {
		enriched.WriteString("# FUNCTION IMPLEMENTATIONS\n\n")
		for name, impl := range context.Functions {
			enriched.WriteString(fmt.Sprintf("## Function: %s\n\n", name))
			enriched.WriteString("```\n")
			enriched.WriteString(impl)
			enriched.WriteString("\n```\n\n")
		}
	}

	// Metadata section (logs, errors, etc.)
	if len(context.Metadata) > 0 {
		enriched.WriteString("# ADDITIONAL CONTEXT\n\n")
		for key, value := range context.Metadata {
			enriched.WriteString(fmt.Sprintf("## %s\n\n", key))
			enriched.WriteString("```\n")
			enriched.WriteString(value)
			enriched.WriteString("\n```\n\n")
		}
	}

	// Restate the question
	enriched.WriteString("# ANALYSIS REQUEST\n\n")
	enriched.WriteString("Given the code and context above, please answer the original question:\n\n")
	enriched.WriteString(prompt)

	return enriched.String(), nil
}

// FormatContextRequestAsText formats the context request as readable text for returning to Claude Code
func FormatContextRequestAsText(response *ContextResponse) string {
	var builder strings.Builder

	builder.WriteString(response.Message)
	builder.WriteString("\n\n")
	builder.WriteString("CONTEXT REQUESTS:\n")

	for i, req := range response.ContextRequests {
		builder.WriteString(fmt.Sprintf("\n%d. %s\n", i+1, req.Type))
		if req.Path != "" {
			builder.WriteString(fmt.Sprintf("   Path: %s\n", req.Path))
		}
		if req.Function != "" {
			builder.WriteString(fmt.Sprintf("   Function: %s\n", req.Function))
		}
		if req.Lines != "" {
			builder.WriteString(fmt.Sprintf("   Lines: %s\n", req.Lines))
		}
		builder.WriteString(fmt.Sprintf("   Reason: %s\n", req.Reason))
	}

	builder.WriteString("\n\nTo provide this context, please use the Read tool to gather file contents,")
	builder.WriteString(" then re-call this tool with the gathered_context parameter containing:")
	builder.WriteString("\n{\n")
	builder.WriteString("  \"files\": {\"path\": \"content\", ...},\n")
	builder.WriteString("  \"functions\": {\"name\": \"implementation\", ...},\n")
	builder.WriteString("  \"metadata\": {\"key\": \"value\", ...}\n")
	builder.WriteString("}\n")

	return builder.String()
}

// Helper functions

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func isCommonWord(word string) bool {
	commonWords := map[string]bool{
		"if": true, "for": true, "while": true, "do": true, "then": true,
		"and": true, "or": true, "not": true, "is": true, "the": true,
		"a": true, "an": true, "to": true, "of": true, "in": true,
		"on": true, "at": true, "by": true, "from": true, "with": true,
	}
	return commonWords[strings.ToLower(word)]
}
