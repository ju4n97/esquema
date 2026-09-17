# Security policy

If you find a security vulnerability in hclapi, please report it privately. Don't open a public issue for security vulnerabilities.

## Supported versions

hclapi follows trunk-based development and doesn't maintain previous release branches.

Security fixes are provided for the latest release. Older releases should be upgraded to the latest version.

## Reporting a vulnerability

Report vulnerabilities through [GitHub Security Advisories](https://github.com/ju4n97/hclapi/security/advisories).

Include the following information:

- A summary of the vulnerability.
- A minimal HCL manifest, `curl` command, or reproduction.
- The expected and observed impact.

hclapi doesn't operate a bug bounty program. No monetary reward is offered for security reports.

## Security model

hclapi uses several defensive measures by default:

- **SQL parameters:** SQL steps use parameterized queries rather than string interpolation.
- **Request limits:** HTTP request bodies are bounded to prevent excessive memory use.
- **Starlark sandboxing:** Starlark execution is restricted and subject to an execution limit.
- **Header safety:** Dynamic HTTP headers are sanitized before being written to the response or outbound request.
- **Manifest validation:** Manifests are validated before the runtime starts.
