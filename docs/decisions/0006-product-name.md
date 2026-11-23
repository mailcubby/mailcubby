# ADR 0006: Product name "Mailcubby"

- Status: Accepted
- Date: 2026-11-23

## Context

The product needs a short, memorable name that contains "Mail" (like Mailpit and Mailtrap), works as a CLI and Docker image name, evokes "one compartment per project", and does not collide with existing email or developer tools.

About 60 Mail+X candidates were screened on 2026-11-23 against GitHub repository names, npm, PyPI, Docker Hub, web search and DNS for .com/.dev/.io. Many were rejected for direct collisions (for example Mailcage and Mailbin are existing email testing tools, Mailtray is one letter from Mailtrap, Mailslot is a Windows IPC API). Shortlisted: Mailcubby, Mailnook, Mailperch, Mailshelf.

## Decision

Use **Mailcubby**: a cubby is a small personal compartment, one per project.

- Binary, CLI and Docker image: `mailcubby` (short alias `cubby` may be added to the CLI later).
- Display name: `Mailcubby`. Env var prefix: `MAILCUBBY_`.
- Go module: `github.com/mailcubby/mailcubby`. Image: `ghcr.io/mailcubby/mailcubby`.
- Tagline: "A cubby for every project's email."

## Consequences

- No repository with this name on GitHub, and npm/PyPI/Docker Hub names were free at the time of checking; `.dev` and `.io` appeared unregistered (`.com` is registered but unused).
- Three syllables is longer than Mailnook, which was rejected because it is easily misheard as "Mailbook".
- If the GitHub org `mailcubby` cannot be obtained, update the module path and image names in one commit and amend this ADR.
