---
name: fi-models
description: This skill is automatically triggered when the user asks about available FreeInference models, model details, pricing, model health, or which models are available. Use when the user wants to browse the model catalog, check model capabilities, or see provider model status.
argument-hint: [--model <name>] [--refresh]
allowed-tools: Bash
---

# FreeInference Models

List and inspect FreeInference models from the cached catalog.

## Usage

Run `fi models` to show all available models with:
- Context window size
- Maximum output length
- Access state (✓ available / ⊘ restricted / ? unknown)
- Supported features (tools, json_mode, structured_outputs, streaming)

### Options

- `--model <name>` — Show detailed information for a specific model
- `--refresh` — Force refresh the model catalog from the API

### Model details

Running `fi models --model minimax-m3` shows:

```
Model: minimax-m3
Context Window: 1.0M
Max Output:     128K
Access:         available
Features:       tools, json_mode, structured_outputs
Pricing (per MTok):
  prompt: $2.8
  completion: $6.4
  input_cache_reads: $0.8
```

### Notes

- Model catalog is cached from the authenticated `/v1/models` endpoint
- Stale cache is automatically refreshed on session start
- `restricted` models appear in the catalog but are not usable with your API key
- Pricing is in micro-dollars (e.g., 2.8 = $2.80 per million tokens)