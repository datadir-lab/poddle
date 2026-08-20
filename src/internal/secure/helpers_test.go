package secure

import "testing"

func TestCredRule_Matches(t *testing.T) {
	creds := []string{
		".env", ".git-credentials", ".npmrc", ".pypirc", ".netrc", ".dockercfg",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		".env.local", ".env.production", // .env.<anything> except the sample forms
		"server.pem", "tls.key", "service-account-prod.json",
	}
	for _, n := range creds {
		if credRule(n) == "" {
			t.Errorf("credRule(%q) = empty, want a credential match", n)
		}
	}
}

func TestCredRule_Safe(t *testing.T) {
	safe := []string{
		"README.md", "main.go", "config.json", "package.json",
		".env.example", ".env.sample", ".env.template", ".env.dist",
	}
	for _, n := range safe {
		if r := credRule(n); r != "" {
			t.Errorf("credRule(%q) = %q, want no match", n, r)
		}
	}
}

func TestSkipDir(t *testing.T) {
	for _, n := range []string{".git", "node_modules", "vendor", ".venv", "venv", "dist", "build", "target"} {
		if !skipDir(n) {
			t.Errorf("skipDir(%q) = false, want true (noisy dir)", n)
		}
	}
	for _, n := range []string{"src", "lib", "app", "internal"} {
		if skipDir(n) {
			t.Errorf("skipDir(%q) = true, want false", n)
		}
	}
}
