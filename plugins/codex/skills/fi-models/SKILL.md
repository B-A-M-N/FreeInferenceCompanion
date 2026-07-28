---
name: fi-models
description: This skill is automatically triggered when the user asks about available FreeInference models, model details, pricing, model health, or which models are available. Use when the user wants to browse the model catalog, check model capabilities, or see provider model status. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
argument-hint: "[--model <name>] [--refresh]"
allowed-tools: Bash
---

# FreeInference Models

List and inspect FreeInference models from the cached catalog. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `fi models` to show all available models with:
- Context window size
- Maximum output length
- Access state (? unknown unless verified — catalog presence does not confirm access)
- Supported features (tools, json_mode, structured_outputs, streaming)

### Options

- `--model <name>` — Show detailed information for a specific model
- `--refresh` — Force refresh the model catalog from the API

### Notes

- Model catalog is cached from the authenticated `/v1/models` endpoint
- Stale cache is refreshed on session start by a detached background process (coalesced across terminals)
- Access is only confirmed by an explicit probe (`fi doctor --probe --model <name>`)
