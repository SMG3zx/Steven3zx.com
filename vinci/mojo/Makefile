# vinci Mojo core. Every tool runs through uv, so the pinned versions in pyproject.toml apply.
# Usage: make build | test | fmt | check | gate
SHELL := /bin/sh
.DEFAULT_GOAL := check

# Inherited VIRTUAL_ENV (from another project's shell) makes uv warn; unset it.
unexport VIRTUAL_ENV

UV := uv run
MOJO := $(UV) mojo

# Mojo programs that get built into build/. Add a new main program here.
BIN_SRCS := src/vinci.mojo
BINS := $(patsubst src/%.mojo,build/%,$(BIN_SRCS))
MOJO_SRCS := $(wildcard src/*.mojo)
TEST_SRCS := $(wildcard tests/test_*.mojo)

.PHONY: build test fmt fmt-check py-check check gate

build: $(BINS)

build/%: src/%.mojo $(MOJO_SRCS)
	@mkdir -p build
	$(MOJO) build -I src $< -o $@

# Mojo 1.x has no `mojo test`. Each tests/test_*.mojo is a program with `def main()`
# that raises on failure; `-I src` makes `from hello import add` resolve.
test:
	@set -e; for t in $(TEST_SRCS); do \
		echo "== $$t"; \
		$(MOJO) run -I src $$t; \
	done

# `mojo format` rewrites in place and has no check mode, so fmt-check formats a copy.
fmt:
	$(MOJO) format src tests

fmt-check:
	@sh scripts/fmt-check.sh

py-check:
	$(UV) pyright py/
	$(UV) ruff check py/
	$(UV) ruff format --check py/

check: fmt-check build test py-check

# The regression gate: check, the golden comparisons against a fresh build, and the eval
# thresholds. One line per stage, then GATE PASS or GATE FAIL (stages). See docs/development.md.
gate:
	@sh scripts/gate.sh
