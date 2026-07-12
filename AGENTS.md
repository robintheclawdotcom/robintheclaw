# Repository instructions

- Use `robintheclaw` as the Git author name. Use the configured neutral noreply address for new commits.
- Treat this repository as a public trust surface. Do not commit personal names, usernames, email addresses, home-directory paths, hostnames, private keys, credentials, or live trading parameters.
- Store the Cloudflare API token only in the ignored, mode-0600 `keys/cloudflare-api-token` file. Never put it in source, documentation, environment examples, or Git configuration.
- Store the Render API key only in the ignored, mode-0600 `keys/render-api-token` file. Never put it in source, documentation, environment examples, or Git configuration.
- Keep comments sparse and purposeful. Explain non-obvious constraints, invariants, or trade-offs; do not narrate code.
- Write production-grade code that is direct, tested, and consistent with the surrounding implementation. Avoid placeholder abstractions, dead code, and generated-sounding prose.
- Run the repository checks before committing. Do not bypass the identity hooks.
