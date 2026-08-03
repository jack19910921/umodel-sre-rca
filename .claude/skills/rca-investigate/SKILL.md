---
name: rca-investigate
description: Produce a strictly evidence-backed RCA result for one SRE incident.
disable-model-invocation: true
---

The customer-hosted RCA runner supplies the following four fixed, already
sanitized evidence results in the prompt, in this order:

```text
/opt/sre-rca/bin/sre-evidence incident context <incident-id>
/opt/sre-rca/bin/sre-evidence metrics query <incident-id>
/opt/sre-rca/bin/sre-evidence logs query <incident-id>
/opt/sre-rca/bin/sre-evidence changes query <incident-id>
```

Analyse only those supplied results. Do not invoke a command, URL, API, shell,
filesystem, or write action. Do not use Feishu, cloud credentials, browser
tools, MCP, remediation, or any network side effect. The four retrieval forms
above are the complete evidence surface; do not substitute, extend, or request
another source.
Do not invent evidence. If ActionTrail data is unavailable, return the evidence
you have and set `pending_audit` to `true`.

The prompt includes `ALLOWED_EVIDENCE_IDS`. `evidence_ids` must be a non-empty
subset of those exact strings. Copy an ID verbatim; never hash, transform,
infer, or invent an ID.

Use Simplified Chinese for `summary`, `root_cause`, and every item in
`next_actions`. Keep only opaque IDs, URLs, and machine field names unchanged.

Return only this JSON object, with no markdown or explanation:

```json
{
  "summary": "short evidence-backed conclusion",
  "confidence": 0.0,
  "root_cause": "specific causal change or condition",
  "evidence_ids": ["evidence-id"],
  "next_actions": ["manual safe next action"],
  "pending_audit": false
}
```
