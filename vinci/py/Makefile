# vinci (Python). Every tool runs through uv, so the pinned versions in pyproject.toml apply.
# Usage: make test | lint | gate
UV := uv run
.DEFAULT_GOAL := test

.PHONY: test lint gate

test:
	$(UV) --group dev python -m pytest -q tests

lint:
	$(UV) --group dev ruff check vinci_core vinci_cli vinci_edge vinci_infer tests vinci_eval

# The regression gate: the tests, then the eval thresholds. See scripts/gate.sh.
gate:
	sh scripts/gate.sh
