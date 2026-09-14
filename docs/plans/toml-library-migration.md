# TOML Library Migration Implementation Plan

> **For Hermes:** Use subagent-driven-development to execute this plan task-by-task. No pull request is to be created automatically.

**Goal:** Replace cliamp's hand-written TOML parsing with `github.com/pelletier/go-toml/v2` where it is semantically safe, while preserving existing configuration behavior, file formats, ordering, defaults, validation, atomic writes, and environment interpolation.

**Architecture:** Use `go-toml/v2` as the syntax parser and decoder, not as a new global domain model. Each TOML-backed subsystem retains its own domain structs and validation. The main config loader preserves cliamp-specific defaults, provider-section activation, and environment substitution as an explicit post-decode step. Existing writers remain unchanged unless tests prove that the library is required for their read path; this avoids rewriting user comments and formatting as an accidental side effect.

**Tech Stack:** Go 1.26.6, `github.com/pelletier/go-toml/v2`, Nix dev shell (`nix develop .#default`), Go tests, `go vet`.

---

## Scope and non-goals

In scope:

- Main `config.toml` parsing.
- Embedded and user theme TOML parsing.
- Local playlist `[[track]]`/`[[dir]]` parsing.
- Favorites and history parsing where the current custom parser is replaced safely.
- Regression coverage for comments, quoting, arrays, repeated sections, unknown fields, and existing cliamp-specific behavior.
- Removing obsolete parser code only after all callers and tests are migrated.

Non-goals:

- Do not redesign the config file format.
- Do not change config save formatting or comment preservation unless explicitly required by tests.
- Do not change provider behavior, playlist semantics, or validation policy unrelated to parsing.
- Do not create or update a PR.

## Acceptance criteria

- Inline comments and quoted `#` values parse according to TOML semantics in every migrated reader.
- Existing config defaults, provider activation, env interpolation, and key mappings remain unchanged.
- Existing playlist ordering and `[[dir]]` expansion behavior remain unchanged.
- Malformed TOML is reported or ignored exactly according to the current subsystem's documented contract; no silent behavior change without a test.
- `go test ./...`, `go vet ./...`, and `git diff --check` pass in the Nix dev shell.
- The branch contains local commits only; no PR is created.

---

## Task 1: Dependency and parser contract

**Objective:** Add and pin the TOML dependency, define the decode conventions, and add focused library-behavior tests before replacing readers.

**Files:**
- Modify: `go.mod`, `go.sum`
- Test: `internal/tomlutil/` or the relevant package tests

**Steps:**

1. Add `github.com/pelletier/go-toml/v2` using the repository's Nix dev shell.
2. Verify the dependency resolves with the existing Go toolchain.
3. Add tests demonstrating library handling of trailing comments, quoted `#`, escaped quotes, arrays, and array-of-table documents.
4. Run the focused tests and confirm the new tests are meaningful before migrating production readers.
5. Commit as `build: add TOML decoder dependency`.

**Verification:** `nix develop .#default --command go test ./internal/tomlutil -count=1` or the package containing the focused tests.

---

## Task 2: Migrate main configuration loading

**Objective:** Decode `config.toml` with the standard TOML parser while preserving cliamp's defaults and post-processing.

**Files:**
- Modify: `config/config.go`
- Modify or add: `config/*_test.go`

**Steps:**

1. Add explicit `toml` tags or a dedicated raw config type for snake_case keys.
2. Preserve `defaultConfig()` and decode into an already-defaulted value where supported, or merge decoded values without changing omission semantics.
3. Preserve provider-section activation and explicit `enabled`/`disabled` handling.
4. Preserve exact env interpolation behavior after decode, including literal dollar signs.
5. Preserve clamping and validation behavior.
6. Add regression tests for inline comments, quoted hashes, arrays, malformed syntax, omitted defaults, provider section activation, and environment variables.
7. Remove the temporary manual inline-comment workaround only if the library-backed reader covers it.
8. Commit as `refactor(config): decode TOML with standard parser`.

**Verification:**

```sh
nix develop .#default --command go test ./config -count=1
```

---

## Task 3: Migrate theme loading

**Objective:** Use the standard parser for embedded and user theme files while preserving theme validation and override precedence.

**Files:**
- Modify: `theme/theme.go`
- Test: `theme/load_test.go`, `theme/theme_test.go`

**Steps:**

1. Decode the flat theme document into a small TOML-tagged struct.
2. Preserve embedded-theme loading, user-theme override precedence, sorting, and `Theme.Validate()`.
3. Preserve behavior for unknown keys and invalid themes.
4. Add tests for trailing comments, quoted hashes, malformed values, validation failures, and user-overrides-built-in behavior.
5. Commit as `refactor(theme): decode theme TOML with standard parser`.

**Verification:** `nix develop .#default --command go test ./theme -count=1`.

---

## Task 4: Migrate structured playlist, favorites, and history readers

**Objective:** Replace the shared minimal section parser only where the standard decoder preserves the existing data model and ordering guarantees.

**Files:**
- Modify: `external/local/dirs.go` and related local playlist parser files
- Modify: `favorites/favorites.go`
- Modify: `history/history.go`
- Modify or remove: `internal/tomlutil/sections.go`, `internal/tomlutil/unquote.go`
- Test: corresponding package tests

**Steps:**

1. Model `[[track]]` and `[[dir]]` as typed TOML array-of-table structs.
2. Preserve document order between track and directory entries; if direct decoding cannot preserve interleaving, use the library's parse/edit/AST capability or retain a narrowly scoped ordered representation.
3. Preserve unknown-field tolerance and last-value behavior where currently relied upon.
4. Preserve environment expansion and path validation at their existing layer.
5. Preserve atomic writers and on-disk compatibility.
6. Add tests for inline comments, quoted hashes, escaped values, interleaved sections, repeated keys, unknown sections, empty sections, and round-trip loading of existing fixtures.
7. Remove `internal/tomlutil` only when no callers remain; otherwise reduce it to non-parser domain helpers.
8. Commit as `refactor(storage): decode playlist history and favorites TOML safely`.

**Verification:**

```sh
nix develop .#default --command go test ./external/local ./favorites ./history ./internal/tomlutil -count=1
```

---

## Task 5: Integration cleanup and documentation

**Objective:** Ensure all TOML readers have consistent behavior and document the supported syntax without changing the file format.

**Files:**
- Modify: relevant package docs and `config.toml.example` only if behavior/documentation needs correction
- Test: package tests and any parser inventory tests

**Steps:**

1. Search for remaining hand-written TOML readers (`Scanner`, `strings.Cut(line, "=")`, `tomlutil` parser calls).
2. Classify every remaining parser as intentional non-TOML handling or migrate it.
3. Add documentation for inline comments and quoted values if needed.
4. Run formatting, vet, focused tests, and the complete suite.
5. Review the final diff for scope creep and obsolete workaround code.
6. Commit as `docs: document TOML parsing behavior` only if documentation changed; otherwise fold cleanup into the relevant refactor commit.

**Verification:**

```sh
nix develop .#default --command go test ./... -count=1
nix develop .#default --command go vet ./...
git diff --check
```

## Final gate

Before reporting completion:

- Confirm the feature branch and clean working tree.
- Confirm all commits are GPG-signed.
- Confirm no PR was created.
- Report exact test commands and results.
- Do not push additional changes unless explicitly authorized.
