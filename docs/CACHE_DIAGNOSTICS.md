# Cache diagnostics

Cache attribution is a local heuristic over observed token counters. It is
not provider-confirmed causality and its scores are not probabilities.

Candidate causes are deduplicated and sorted by `heuristic_score`. Confidence
is likewise a heuristic evidence-strength label derived from sample count and
telemetry completeness. Missing provider fields remain unknown; they are not
converted to zero.

Useful commands:

```bash
freeinference cache
freeinference status --level detailed
freeinference report --format json
```

The report contains bounded observations, freshness metadata, and advisory
diagnoses. It never contains prompts, responses, credentials, transcript
paths, or raw error bodies.

## Pattern meanings

| Pattern | Meaning | Example cause |
| --- | --- | --- |
| Thrashing | High cache creation and low cache read | Dynamic content at the start of the system prompt keeps rewriting the prefix |
| No caching | Almost all fresh input with negligible cache activity | The client is not using cache breakpoints |
| Decay | Read share was initially good but is declining | The conversation is growing beyond the cached prefix |
| Intermittent | Good and bad observations alternate | Tool results appear before the cached prefix on some turns |

The same likely diagnosis may appear inline with a cache-low warning. These
are suggestions for investigation, not claims about provider internals.

Projection and cache-TTL warnings have separate gates. Projection confidence
is capped at `medium` because the companion cannot inspect the full request;
TTL expiry is reported only when the provider confirms a TTL. See
[Observability](OBSERVABILITY.md) for the thresholds and freshness rules.
