# Security

Do not publish exploitable security reports, credentials or private telemetry in
public issues. When the repository is published, use its **Security → Report a
vulnerability** feature. The maintainer must enable private vulnerability reporting
before advertising a public release. If that facility is unavailable, request a
private contact channel without disclosing exploit details publicly.

Include the affected version, deployment mode, impact, minimal reproduction and
whether special permissions are required. Reports should avoid unrelated personal
data. This new project has no guaranteed security response SLA.

The hub is an administrator interface, not a public telemetry endpoint for arbitrary
senders. Keep it on a trusted network or authenticated reverse-proxy path and retain
TLS verification. Agents have scoped ingestion credentials. Anyone holding the real
Docker socket can potentially control Docker: a read-only mount is not an API policy.

The optional proxy exposes selected read endpoints over a shared Unix socket only.
It still belongs to the trusted computing base. Inspect responses can contain
sensitive values; the agent decodes only allowlisted fields and never forwards raw
responses. Review proxy policy changes as security-sensitive changes.

Only v0.1 development is currently maintained. No production-support or NAS
certification claim is made by this repository.
