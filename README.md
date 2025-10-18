# GPT-5-Pro MCP

An MCP (Model Context Protocol) server that provides access to OpenAI's GPT-5-Pro for solving complex problems. GPT-5-Pro can read files and search codebases to gather context for analysis.

## Features

- **GPT-5-Pro Access**: Uses OpenAI's GPT-5-Pro model via the Responses API
- **File Operations**: GPT-5-Pro can read files and search with grep to gather information
- **Automatic Conversation Continuity**: Server-side conversation state via response IDs
- **Comprehensive Logging**: Stderr logging for debugging and monitoring

## Prerequisites

- Go 1.25.1 or later
- [OpenAI API Key](https://platform.openai.com/) with access to GPT-5-Pro
  - **Note**: OpenRouter and other providers are currently NOT supported (see Configuration section)
- [Hermit](https://cashapp.github.io/hermit/) (optional, for environment management)

## Installation

### Using Hermit (Recommended)

```bash
# Activate hermit environment
. bin/activate-hermit

# Install dependencies
go mod download

# Build
task build
```

### Without Hermit

```bash
# Install dependencies
go mod download

# Build
go build -o dist/gpt-5-pro-mcp .
```

## Configuration

### OpenAI Configuration

Set your OpenAI API key as an environment variable:

```bash
export OPENAI_API_KEY="your-api-key-here"
```

#### Using Custom OpenAI-Compatible APIs (e.g., aihubmix)

✅ **NOW SUPPORTED!** You can use any OpenAI-compatible API endpoint by setting `OPENAI_BASE_URL`:

```bash
export OPENAI_API_KEY="your-api-key-here"
export OPENAI_BASE_URL="https://aihubmix.com/v1"  # or your custom endpoint
```

**How it works:**
- When `OPENAI_BASE_URL` is set, the server automatically uses the **Chat Completions API** (`/v1/chat/completions`)
- This works with aihubmix, Azure OpenAI, and other OpenAI-compatible providers
- All features work identically: conversation continuity, tool calling, context gathering
- Without `OPENAI_BASE_URL`, the server uses the official OpenAI **Responses API** (`/v1/responses`)

### OpenRouter Configuration

✅ **NOW SUPPORTED!** You can use OpenRouter with the Chat Completions API:

```bash
export OPENROUTER_API_KEY="your-openrouter-key"
export OPENROUTER_BASE_URL="https://openrouter.ai/api/v1"  # Optional, defaults to this
```

**How it works:**
- The server automatically detects OpenRouter configuration
- Uses Chat Completions API for full compatibility
- Supports all features: conversation continuity, tool calling, context gathering

**API Support Summary:**
- ✅ **Official OpenAI**: Responses API (`/v1/responses`) - Default when no custom URL
- ✅ **Custom Endpoints** (aihubmix, etc.): Chat Completions API - When `OPENAI_BASE_URL` is set
- ✅ **OpenRouter**: Chat Completions API - When `OPENROUTER_API_KEY` is set

### Using direnv

You can also configure with `.envrc`:

```bash
export OPENAI_API_KEY="your-api-key-here"
```

## Usage

### Install to Claude Code

```bash
task install:claude-code
```

This installs the MCP server to your user-level Claude Code configuration with the `OPENAI_API_KEY` from your environment.

### Manual Installation

Add to your MCP client configuration (e.g., `~/.claude.json`):

```json
{
  "mcpServers": {
    "gpt-5-pro": {
      "command": "/path/to/gpt-5-pro-mcp/dist/gpt-5-pro-mcp",
      "env": {
        "OPENAI_API_KEY": "your-api-key-here"
      }
    }
  }
}
```

### Testing

Test the server directly using mcp-tester:

```bash
mcp-tester call --tool=gpt-5-pro --json='{"prompt":"What is 2+2?"}' dist/gpt-5-pro-mcp
```

## The `gpt-5-pro` Tool

### Parameters

- **prompt** (required): The question or problem to analyze
- **continue** (optional, default: `true`): Continue previous conversation or start fresh
- **gathered_context** (optional): JSON string containing code context gathered by Claude Code
- **auto_gather_context** (optional, default: `true`): Enable automatic context gathering when code references are detected

### Available Tools for GPT-5-Pro

GPT-5-Pro has access to these tools to gather information:

- **read_file**: Read contents of any file from the filesystem
- **grep_files**: Search for regex patterns in files matching glob patterns

GPT-5-Pro will automatically use these tools when it needs to examine code or gather context.

### Conversation Flow

Conversation state is managed server-side using OpenAI's Responses API:

- **continue: true** (default) - Continues from the previous response ID
- **continue: false** - Starts a fresh conversation

Conversation history persists for the lifetime of the MCP server process.

### Examples

**Single Query:**
```json
{
  "prompt": "Explain the performance implications of using channels vs mutexes in Go"
}
```

**Multi-Turn Conversation:**

Query 1:
```json
{
  "prompt": "I'm investigating a memory leak in a Go application. The heap grows continuously."
}
```

Query 2:
```json
{
  "prompt": "pprof shows 10,000+ goroutines blocked on channel receive.",
  "continue": true
}
```

Query 3 (continue defaults to true):
```json
{
  "prompt": "The work channel is never closed. How should I fix this?"
}
```

**Starting Fresh:**
```json
{
  "prompt": "New question about database indexing strategies",
  "continue": false
}
```

**With File Access:**

GPT-5-Pro will automatically read files when needed:

```json
{
  "prompt": "Review the error handling in internal/client/gpt5pro.go and suggest improvements"
}
```

GPT-5-Pro will use `read_file` to examine the code and provide specific recommendations.

## Intelligent Context Gathering

The MCP server includes an intelligent context-gathering system that enhances GPT-5-Pro's analysis by ensuring it has access to relevant code before providing advice.

### How It Works

**Two-Phase Protocol:**

1. **Phase 1 - Context Analysis**: When you ask a question mentioning files, functions, or line numbers, the MCP automatically detects these references and returns a structured request for context.

2. **Phase 2 - Enriched Analysis**: Claude Code gathers the requested context (reads files, extracts functions) and re-calls the MCP. The prompt is enriched with all relevant code, and GPT-5-Pro provides accurate, code-aware advice.

### Automatic Context Detection

The system automatically detects:
- File paths (e.g., `tree_publisher.py`, `src/client/api.ts`)
- Function calls (e.g., `get_tree_state()`, `updateUserProfile()`)
- Line number references (e.g., `lines 94-99`, `line 230`, `:1686`)
- Code-related keywords (function, class, method, implementation, etc.)

### Example Workflow

**User Question:**
```
Why is get_tree_state() in tree_publisher.py lines 116-230 returning empty data?
The orchestrator calls update_tree_state() at line 1686.
```

**Phase 1 - MCP Response:**
```
To provide accurate analysis, I need to see the actual code. Please gather the following context:

1. file_content
   Path: tree_publisher.py
   Reason: file mentioned in prompt

2. function_implementation
   Function: get_tree_state
   Lines: 116-230
   Reason: key function in analysis

3. function_implementation
   Function: update_tree_state
   Reason: function referenced in prompt
```

**Phase 2 - Claude Code Actions:**
- Reads `tree_publisher.py`
- Extracts function implementations
- Re-calls MCP with gathered context

**Phase 2 - MCP Enriches Prompt:**
```
# ORIGINAL QUESTION
Why is get_tree_state() returning empty data?

# RELEVANT CODE CONTEXT
## File: tree_publisher.py
[full file contents]

## Function: get_tree_state
[lines 116-230]

## Function: update_tree_state
[implementation]

# ANALYSIS REQUEST
Given the code above, please answer the original question.
```

**Result:** GPT-5-Pro sees the actual code and provides accurate, specific advice instead of generic suggestions.

### Context JSON Format

When providing context manually or via automation:

```json
{
  "files": {
    "path/to/file.py": "file contents...",
    "another/file.go": "file contents..."
  },
  "functions": {
    "function_name": "function implementation...",
    "ClassName.method": "method implementation..."
  },
  "metadata": {
    "logs": "relevant log output...",
    "error_messages": "stack traces...",
    "call_graph": "function relationships..."
  }
}
```

### Disabling Context Gathering

To skip automatic context gathering for simple questions:

```json
{
  "prompt": "What are the best practices for error handling?",
  "auto_gather_context": false
}
```

## How It Works

This MCP uses OpenAI's Responses API with GPT-5-Pro. The system prompt guides the model to:

- Break down complex problems systematically
- Question assumptions and consider multiple perspectives
- Use file operations to gather evidence when analyzing code
- Provide clear, actionable insights
- Acknowledge uncertainty when appropriate

GPT-5-Pro can proactively read files and search codebases using its built-in tools without requiring explicit user requests.

## Development

```bash
# Build
task build

# Run tests
task test

# Run linter
task lint

# Clean build artifacts
task clean

# Tidy dependencies
task tidy
```

## Architecture

```
.
├── main.go                      # MCP server initialization
├── internal/
│   ├── client/
│   │   └── gpt5pro.go          # OpenAI Responses API client
│   ├── server/
│   │   └── mcp.go              # MCP server setup and tool registration
│   └── fileops/
│       └── fileops.go          # File operation handlers (read, grep)
└── Taskfile.yaml               # Build and development tasks
```

## Model Information

- **Model**: `gpt-5-pro`
- **Provider**: OpenAI
- **API**: OpenAI Responses API (`/v1/responses`)
- **Capabilities**: Advanced reasoning, function calling, extended context

## API Compatibility

This MCP server supports **both** OpenAI API types:

### Responses API (Official OpenAI)
Used by default when no custom base URL is set:
- Server-side conversation state management via response IDs
- Native tool calling with automatic iteration
- Structured reasoning output
- Best for official OpenAI GPT-5-Pro access

### Chat Completions API (Custom Endpoints & OpenRouter)
Automatically used when custom endpoints detected:
- Client-side conversation history management
- Standard tool calling with function definitions
- Compatible with aihubmix, Azure OpenAI, OpenRouter, and other providers
- Same features and capabilities as Responses API

**Automatic Detection:**
- Official OpenAI (no `OPENAI_BASE_URL`) → Responses API
- Custom endpoint (`OPENAI_BASE_URL` set) → Chat Completions API
- OpenRouter (`OPENROUTER_API_KEY` set) → Chat Completions API

## Logging

The server logs to stderr with timestamps. Logs include:

- Request details (prompt length, continue flag)
- API calls and responses
- Tool executions (file reads, grep operations)
- Response processing details

View logs when testing with mcp-tester or check Claude Code logs at `~/Library/Logs/Claude/`.

## Pricing

Pricing is determined by OpenAI. Check current rates at https://platform.openai.com/docs/models/gpt-5-pro

## License

MIT

## Contributing

Issues and pull requests welcome.
