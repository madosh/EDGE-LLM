# Contributing to LiteOps

LiteOps is built in phases. Each phase has a clear goal, defined files, tests, and docs.
All contributions should map to a phase or open a new one.

---

## Project phases

| Phase | Module | Status |
|---|---|---|
| 1 | `liteops.observer` — memory metrics, latency, tokens | ✅ Complete |
| 2 | `liteops.evaluator` — quality gates, rollback on regression | 🔜 Next |
| 3 | `liteops.connectors.servicenow` — enterprise IT bridge | 📋 Planned |
| 4 | `liteops.router` — LangGraph hub routing | 📋 Planned |
| 5 | `liteops_esp32` — MicroPython MQTT leaf agent | 📋 Planned |
| 6 | LiteRT upstream PRs | 📋 Planned |

---

## Development setup

```bash
git clone https://github.com/{you}/liteops
cd liteops
python -m venv .venv
source .venv/bin/activate
pip install -e ".[dev]"
```

## Run tests

```bash
pytest                    # all tests
pytest tests/test_observer.py -v  # one module
pytest --cov=liteops --cov-report=term-missing  # with coverage
```

## Code style

```bash
black liteops tests       # format
isort liteops tests       # sort imports
ruff check liteops tests  # lint
mypy liteops              # type-check
```

All four must pass before a PR is opened. Run them together:

```bash
black liteops tests && isort liteops tests && ruff check liteops tests && mypy liteops
```

## Commit message format

Follow Conventional Commits:

```
feat(observer): add snapshot() for passive memory polling
fix(observer): handle PID reuse after lit serve restart
test(observer): add test for peak memory monotonicity
docs(phase-1): document PID resolution order
```

## PR checklist

- [ ] New code has tests (aim for 90 %+ coverage on new files)
- [ ] `pytest` passes locally
- [ ] `black`, `isort`, `ruff`, `mypy` pass
- [ ] Phase doc updated if behaviour changed
- [ ] `CHANGELOG.md` entry added under `[Unreleased]`

## Opening a new phase

1. Create `docs/phases/phase-N-name.md` describing goal, deliverables, and tests.
2. Create `liteops/new_module.py` and `tests/test_new_module.py`.
3. Open a GitHub issue titled `[Phase N] Module name — goal` before writing code.
4. Link the issue in the PR.
