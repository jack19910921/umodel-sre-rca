---
name: rca-investigate
description: Produce a strictly evidence-backed RCA result for one SRE incident.
disable-model-invocation: true
---

Given an incident ID, run exactly these commands in this order:

```text
/opt/sre-rca/bin/sre-evidence incident context <incident-id>
/opt/sre-rca/bin/sre-evidence metrics query <incident-id>
/opt/sre-rca/bin/sre-evidence logs query <incident-id>
/opt/sre-rca/bin/sre-evidence changes query <incident-id>
```

Do not invoke any other command, URL, API, shell, filesystem, or write action.
Do not use Feishu, cloud credentials, browser tools, MCP, remediation, or any
network side effect. The four commands above are the entire allowed tool
surface; do not substitute or extend them.
Do not invent evidence. If ActionTrail data is unavailable, return the evidence
you have and set `pending_audit` to `true`.

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
