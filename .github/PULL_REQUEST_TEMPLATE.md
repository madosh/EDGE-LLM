## What this changes

<!-- A clear, short description of the change and the motivation for it. -->

## Type of change

<!-- Check what applies. See CONTRIBUTING.md for commit conventions. -->

- [ ] `feat` — new functionality
- [ ] `fix` — bug fix
- [ ] `docs` — documentation only
- [ ] `test` — tests only
- [ ] `refactor` / `chore` — no behaviour change

## Components touched

- [ ] `control-plane/` (Go)
- [ ] `agent/` (Rust)
- [ ] `web/` (SvelteKit)
- [ ] `monitoring/` / `docker-compose.yml`
- [ ] Docs / CI

## Checks

<!-- Only the suites relevant to the components you touched. -->

- [ ] `cd control-plane && go vet ./... && go test ./...`
- [ ] `cd agent && cargo fmt --check && cargo clippy -- -D warnings && cargo test`
- [ ] `cd web && npm run lint && npm run check && npm test`

## Notes for reviewers

<!-- Anything non-obvious: trade-offs, follow-up work, things you deliberately
     left out, or areas where you'd like a closer look. -->
