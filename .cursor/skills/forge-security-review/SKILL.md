---
name: forge-security-review
description: Reviews Forge changes for untrusted-code and infrastructure security issues. Use when reviewing security, auth, containers, webhooks, secrets, or when the user asks for a security review.
---

# Forge security review

Report findings only. Do not "fix" during a review unless the developer asks.

Treat user code and all external input as untrusted.

## Checklist

- Command injection: user-controlled strings must not reach a host shell
- Container isolation: user workloads must not run on the Forge host
- Authentication and authorization: verify ownership server-side; no client-supplied IDs as auth
- IDOR: changing an ID must not yield another user's resource
- Secret exposure: no secrets in source, logs, images, or client responses
- Webhook security: verify signatures; do not trust unauthenticated callbacks
- Resource exhaustion: timeouts, size limits, concurrency limits
- Supply chain: justify new dependencies; pin when adding them
- Network: user code must not get ambient access to the control plane or other tenants

## Output

For each confirmed finding:

- File and location
- Severity: Critical / High / Medium / Low
- Attack in plain language
- A concrete fix (description, not a drive-by rewrite)

If nothing is confirmed, say so. Do not pad with theoretical issues.

Do not bypass security boundaries for convenience. Do not authenticate production systems during review.
