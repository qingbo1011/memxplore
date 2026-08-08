# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| `0.1.x` | Yes |
| Development revisions on `main` | No |

MemXplore is a research reference rather than a production service. Do not use even a supported release as the sole store for sensitive or irreplaceable data.

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting for this repository. Do not open a public issue containing exploit details, secrets, personal data, or a working attack against a deployed system.

Include the affected revision, threat scenario, minimal reproduction, and expected boundary. Particularly relevant classes include namespace/scope bypass, persistent prompt injection, private-to-shared consolidation, token disclosure, subject-export omissions, and residual data after purge.

## Security model summary

- Stored content is untrusted evidence by default.
- Non-loopback HTTP requires a scoped API token; stored token material is hashed.
- TLS termination belongs at a reverse proxy.
- Forget and purge are distinct; purge is explicit, irreversible, and audited without retaining deleted content.
- Cloud credentials are never auto-discovered for release tests.
