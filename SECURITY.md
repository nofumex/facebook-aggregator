# Security policy

Please report vulnerabilities privately through GitHub Security Advisories for
this repository. Do not open a public issue containing Facebook cookies,
Telegram bot tokens, database URLs, API keys, exploit details, or user data.

Supported security baseline:

- Go 1.25.13 or newer security patch release;
- PostgreSQL 16 or newer supported release;
- dependencies without reachable findings from `govulncheck ./...`.

Facebook cookies and Telegram/LLM credentials must be supplied through the
runtime environment. Real `.env` files, private keys and common credential
files are excluded from both Git and Docker build contexts.
