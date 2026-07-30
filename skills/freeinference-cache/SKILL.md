---
name: freeinference-cache
description: This skill performs cache analysis for FreeInference Companion. Use when the user asks about cache performance, cache hit rates, context reuse, or why their cache is cold. Shows cache-read tokens, cache-write tokens, fresh input tokens, classification, evidence, and possible causes. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.
allowed-tools: Bash
---

# FreeInference Cache Analysis

Analyze cache performance from structured data. Community-built and unofficial. Not affiliated with or endorsed by FreeInference.

## Usage

Run `freeinference cache --json` to get structured cache data:

```bash
freeinference cache --json
```

The output includes:

### Latest request breakdown

- Fresh tokens: tokens that were not served from any cache layer
- Cache read tokens: tokens served from key-value cache (KV cache hits)
- Cache write tokens: tokens that were written to cache (not yet reusable)
- Output tokens: tokens generated as model output

### Cache analysis

- Read share: percentage of tokens served from cache reads
- Creation share: percentage of tokens written to cache but not yet read
- Fresh share: percentage of tokens that required full model evaluation
- Trend: stable, warming, cooling, or volatile

### Diagnosis confidence

The skill distinguishes between:

- **Provider-confirmed**: FreeInference API returned cache metrics directly (authoritative)
- **Locally inferred**: Metrics derived from local state only (lower confidence)

### Evidence and missing evidence

The analysis lists what evidence supports the diagnosis and what evidence is missing:

- Evidence present: "KV cache read metrics available", "Multiple samples collected", "Stable pattern across N requests"
- Evidence missing: "No cache write records found", "TTL not yet expired", "Provider API not returning cache fields"

### Ranked possible causes

Present causes in ranked order (highest probability first) using honest language:

- Use "likely cause: ..." not "root cause: ..."
- Use "evidence supports: ..." not "proves: ..."
- List multiple plausible explanations when data is insufficient

### Example output format

```
Cache Analysis (8 unique samples, provider-confirmed)

Latest request breakdown:
  Fresh:        12K  (2.3%)
  Cache read:  480K  (92.1%)
  Cache new:    30K  (5.7%)
  Output:        8K  (1.5%)

  Read share:  92.1%
  New share:   5.7%
  Fresh share:  2.3%
  Trend:       stable

Diagnosis: provider-confirmed

Evidence:
  [OK] KV cache read metrics from provider
  [OK] 8 unique samples (threshold: 5)
  [OK] Stable pattern over 20 requests

Possible causes (cached is working well):
  1. Likely cause: Large context windows are being reused effectively
     Evidence: 92.1% read share, stable trend over 8 samples
  2. Possible cause: Some prompt variation in system instructions
     Evidence: 2.3% fresh share not zero — minor prompt drift detected

Cache TTL: 300s (writes become readable after 5 minutes)
```

### Linking to freeinference-support

When cache issues are persistent or unexplained, link to the support skill:

```
freeinference doctor --json       # full diagnostic
freeinference report --json       # exportable support bundle
```

### Notes

- Cache TTL determines how long written tokens remain usable. A short TTL causes the cache to go cold more frequently.
- A fresh share above 20% may indicate prompt drift, new session, or insufficient context reuse.
- Cache metrics are only available when the FreeInference API returns them. Without provider metrics, diagnosis is locally inferred and less reliable.
