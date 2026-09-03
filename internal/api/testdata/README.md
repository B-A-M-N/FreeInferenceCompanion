# Public status fixture

`current.json` is a sanitized contract fixture captured from
`https://status.freeinference.org/api/status` on 2026-09-02 UTC. It retains
all nine public model records, the actual `uptimeRatio`, `history`, and
`spark` fields, with only the most recent history points kept to bound the
fixture size. It contains no credentials or private session data.
