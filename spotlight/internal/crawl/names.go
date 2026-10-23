package crawl

import (
	"path"
	"strings"
)

// BuiltinIgnoreDirs are directory names that are never indexed or watched:
// dependency trees, build output, caches, and editor or tool state. They always
// apply; the ignore_dirs config option adds to this list.
var BuiltinIgnoreDirs = []string{
	".git", "node_modules", "bower_components", "vendor",
	"target", "dist", "build", "coverage",
	".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache", ".tox",
	".gradle", ".idea", ".vs", ".terraform",
	".next", ".nuxt", ".svelte-kit", ".turbo", ".parcel-cache", ".cache",
	".zig-cache", "zig-cache", "zig-out", "zig-pkg",
	".obsidian", ".smart-env", // plugin code and embedding stores, not notes
	"$RECYCLE.BIN", "System Volume Information",
}

// BuiltinIgnorePaths are slash-separated directory paths that are never
// indexed. Claude Code keeps full duplicate checkouts of a repo here.
var BuiltinIgnorePaths = []string{
	".claude/worktrees",
}

// BuiltinIgnoreFiles are base-name globs (matched case-insensitively) for files
// that are never indexed: lock files, minified bundles, key material, and
// service config known to hold credentials.
var BuiltinIgnoreFiles = []string{
	// dependency lock files
	"*.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "go.sum",
	// generated bundles and source maps
	"*.min.js", "*.min.css", "*.map",
	// private keys and credential stores
	"*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx", "*.ppk", "*.gpg",
	"id_rsa*", "id_dsa*", "id_ecdsa*", "id_ed25519*",
	".netrc", ".npmrc", ".pgpass", ".htpasswd", "kubeconfig",
	"app.ini", // Gitea: holds SECRET_KEY and INTERNAL_TOKEN
	"*.tfstate", "*.tfstate.*",
	// databases and OS clutter
	"*.sqlite", "*.sqlite3", "*.db", ".ds_store", "thumbs.db",
}

var (
	secretPrefixes = []string{
		"credential", "secret", "token", "apikey", "api_key", "api-key",
		"client_secret", "client-secret", "service-account", "serviceaccount",
	}
	// Data-like extensions where a secret-ish file name is treated as a secret.
	// Source files such as token.go or secrets_test.go are unaffected.
	secretDataExts = map[string]bool{
		"": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
		".conf": true, ".cfg": true, ".txt": true, ".xml": true, ".properties": true, ".env": true,
	}
)

// isSecretName reports whether a lower-cased base name looks like a file of
// credentials, such as .env, secrets.json or credentials.yaml.
func isSecretName(name string) bool {
	if name == ".env" {
		return true
	}
	if strings.HasPrefix(name, ".env.") {
		for _, ok := range []string{".example", ".sample", ".template", ".dist"} {
			if strings.HasSuffix(name, ok) {
				return false
			}
		}
		return true
	}
	ext := path.Ext(name)
	if !secretDataExts[ext] {
		return false
	}
	stem := strings.TrimSuffix(name, ext)
	for _, p := range secretPrefixes {
		if strings.HasPrefix(stem, p) {
			return true
		}
	}
	return false
}
