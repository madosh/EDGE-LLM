# Contributing to Cami Fleet

Thanks for your interest in contributing! This document covers the development
workflow and conventions used in the project.

## Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Docker + Compose | v2+ | Run the full stack locally |
| Go | 1.23+ | Control plane development |
| Rust | stable | Agent development |
| Node.js | 20+ | Web UI development |
| protoc | 3.21+ | Regenerate gRPC stubs (optional) |

## Getting started

```bash
# Clone and start everything
git clone https://github.com/madosh/EDGE-LLM.git
cd EDGE-LLM
cp .env.example .env
docker compose up --build
```

Open http://localhost:5173 — two simulated devices should appear within 60 seconds.

## Project structure

```
├── control-plane/   Go 1.23 — REST + gRPC server
├── agent/           Rust — edge device agent
├── web/             SvelteKit — operator dashboard
├── monitoring/      Prometheus + Grafana configs
├── scripts/         Certificate generation
├── artifacts/       Model artifacts (generated at runtime)
└── .github/         CI workflows
```

## Development workflow

### Control plane (Go)

```bash
cd control-plane
go vet ./...
go test ./...
```

### Agent (Rust)

```bash
cd agent
cargo fmt --check
cargo clippy -- -D warnings
cargo test
```

### Web UI (SvelteKit)

```bash
cd web
npm install
npm run lint
npm run check
npm test
```

## Commit conventions

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(agent): integrate Ollama for real inference
fix(control-plane): handle partial deployment failures
docs: update architecture diagram
test(web): add API client unit tests
```

## Pull requests

1. Fork the repo and create a feature branch from `main`
2. Make your changes with tests
3. Ensure CI passes (`go test`, `cargo test`, `npm test`)
4. Open a PR with a clear description of the change

## Code style

- **Go**: `gofmt` + `golangci-lint` (config in `.golangci.yml`)
- **Rust**: `rustfmt` + `clippy` (config in `rustfmt.toml`)
- **TypeScript**: ESLint + Prettier (config in `web/`)

## License

By contributing, you agree that your contributions will be licensed under the
MIT License.
