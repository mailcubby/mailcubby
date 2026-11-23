# Contributing to Mailcubby

Thanks for your interest. Mailcubby is pre-alpha; expect large changes.

## Before you start

- Read `AGENTS.md` (conventions, commands, security rules; it applies to humans too), `docs/SPEC.md` and `docs/ARCHITECTURE.md`.
- For anything beyond a small fix, open an issue first to agree on the approach. Architectural changes need an ADR in `docs/decisions/`.
- Security issues: do not open a public issue. Use GitHub private vulnerability reporting (repository Security tab, "Report a vulnerability").

## Development setup

Requirements: Go 1.27.4, Node.js 26 LTS, pnpm 12.21, a C compiler for `make test-race`, Docker (optional). Test mail can be sent with the built-in `mailcubby sendmail` (`make seed`) or `swaks`.

```sh
make tools   # pinned sqlc and golangci-lint into ./bin
make deps    # Go modules, web packages, Playwright Chromium
make dev     # Go on :8025/:1025, Vite on :5173
make seed    # send testdata/mime/*.eml to the running instance
make check   # must pass before you open a pull request (plus make e2e for UI changes)
```

## Pull requests

- One logical change per PR, with tests. Reference the requirement IDs (`FR-xxx`) and the `docs/PLAN.md` task you are completing.
- Conventional Commit messages (`feat(api): ...`, `fix(smtpd): ...`).
- Update docs (`SPEC`, `ARCHITECTURE`, `UI`, `openapi.yaml`) in the same PR when behaviour changes.
- New dependencies need a justification in the PR description and an entry in `AGENTS.md` section 3.
- AI-assisted contributions are welcome; you are responsible for reviewing and understanding every line you submit.

## License

By contributing you certify that you have the right to submit your contribution, and you grant NOOP a perpetual, worldwide, non-exclusive, royalty-free, irrevocable license to use, modify, sublicense and distribute it under the PolyForm Shield License 1.0.0 (see `LICENSE`) or any other terms, including commercial licenses.
