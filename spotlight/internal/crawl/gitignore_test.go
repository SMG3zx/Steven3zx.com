package crawl

import "testing"

func TestGitignorePatterns(t *testing.T) {
	cases := []struct {
		name     string
		patterns string
		path     string
		isDir    bool
		want     bool // ignored?
	}{
		// unanchored names match at any depth
		{"name at root", "build", "build", true, true},
		{"name nested", "build", "a/b/build", true, true},
		{"name matches files too", "build", "a/build", false, true},
		{"not a substring", "build", "a/rebuild", false, false},

		// trailing slash: directories only
		{"dir-only matches dir", "node_modules/", "node_modules", true, true},
		{"dir-only nested dir", "node_modules/", "x/node_modules", true, true},
		{"dir-only skips file", "node_modules/", "node_modules", false, false},

		// leading slash anchors to the .gitignore's directory
		{"anchored root hit", "/CADDY/", "CADDY", true, true},
		{"anchored nested miss", "/CADDY/", "sub/CADDY", true, false},
		{"anchored file", "/index.md", "index.md", false, true},
		{"anchored file nested miss", "/index.md", "docs/index.md", false, false},

		// a slash in the middle also anchors
		{"mid-slash hit", "docs/*.md", "docs/a.md", false, true},
		{"mid-slash nested miss", "docs/*.md", "x/docs/a.md", false, false},
		{"star does not cross slash", "docs/*.md", "docs/sub/a.md", false, false},

		// globs
		{"star extension", "*.log", "a/b/x.log", false, true},
		{"question mark", "foo?.txt", "foo1.txt", false, true},
		{"question mark needs a char", "foo?.txt", "foo.txt", false, false},
		{"char class", "[ab].txt", "a.txt", false, true},
		{"char class miss", "[ab].txt", "c.txt", false, false},
		{"negated class", "[!ab].txt", "c.txt", false, true},

		// double star
		{"leading **/ at root", "**/zig-pkg/", "zig-pkg", true, true},
		{"leading **/ deep", "**/zig-pkg/", "a/b/zig-pkg", true, true},
		{"middle ** zero dirs", "a/**/b", "a/b", false, true},
		{"middle ** many dirs", "a/**/b", "a/x/y/b", false, true},
		{"trailing /** inside", "abc/**", "abc/x/y", false, true},
		{"trailing /** not the dir", "abc/**", "abc", true, false},

		// negation and ordering
		{"negation re-includes", "*.log\n!keep.log", "keep.log", false, false},
		{"negation leaves others", "*.log\n!keep.log", "other.log", false, true},
		{"later rule wins", "!keep.log\n*.log", "keep.log", false, true},

		// escapes, comments, blanks
		{"comment ignored", "# build\nother", "build", true, false},
		{"escaped hash", `\#file`, "#file", false, true},
		{"blank lines ignored", "\n\nfoo\n", "foo", false, true},
		{"crlf endings", "foo\r\nbar\r\n", "bar", false, true},
		{"trailing spaces trimmed", "foo   ", "foo", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := parseGitignore([]byte(c.patterns), false)
			got, _ := g.match(c.path, c.isDir)
			if got != c.want {
				t.Fatalf("patterns %q, path %q (dir=%v): ignored=%v, want %v", c.patterns, c.path, c.isDir, got, c.want)
			}
		})
	}
}

func TestGitignoreUnicodeAndCaseFolding(t *testing.T) {
	g := parseGitignore([]byte("café/\nREADME.MD\n"), true)
	if ig, _ := g.match("café", true); !ig {
		t.Fatal("non-ASCII pattern should match")
	}
	if ig, _ := g.match("readme.md", false); !ig {
		t.Fatal("case-insensitive mode should match regardless of case")
	}
	g = parseGitignore([]byte("README.MD\n"), false)
	if ig, _ := g.match("readme.md", false); ig {
		t.Fatal("case-sensitive mode must not fold case")
	}
}

func TestIsSecretName(t *testing.T) {
	secret := []string{".env", ".env.local", ".env.production", "secrets.json", "credentials.yaml",
		"credentials", "tokens.json", "token.txt", "client_secret_123.json", "service-account.json", "apikey.toml"}
	fine := []string{"token.go", "secrets_test.go", "tokenizer.rs", ".env.example", ".env.sample", "readme.md",
		"credentials.go", "main.go", "secret.ts"}
	for _, n := range secret {
		if !isSecretName(n) {
			t.Errorf("%q should be treated as a secret file", n)
		}
	}
	for _, n := range fine {
		if isSecretName(n) {
			t.Errorf("%q should not be treated as a secret file", n)
		}
	}
}
