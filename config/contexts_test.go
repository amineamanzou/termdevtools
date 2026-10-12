package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseContextsStrict(t *testing.T) {
	valid := "contexts:\n  - name: prod-eu\n    url: https://example.net/tenant/search\n    labels:\n      env: prod\n"
	inv, err := ParseContexts([]byte(valid), "contexts.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := inv.Lookup("prod-eu")
	if !ok || d.ID() != "named:prod-eu" || !d.Cluster.TLS.Verify || d.Cluster.AuthType != AuthNone {
		t.Fatalf("unexpected profile: %#v", d)
	}
	for _, data := range []string{"", "# empty\n", "contexts: []\n"} {
		i, e := ParseContexts([]byte(data), "contexts.yaml")
		if e != nil || len(i.Definitions()) != 0 {
			t.Fatal(e)
		}
	}
	cases := map[string]string{
		"missing-name":   "url: http://localhost",
		"long-name":      "name: " + strings.Repeat("a", 65) + "\n    url: http://localhost",
		"path-name":      "name: ../prod\n    url: http://localhost",
		"unknown":        "name: a\n    url: http://localhost\n    unexpected: value",
		"plaintext":      "name: a\n    url: http://localhost\n    password: DO_NOT_LEAK",
		"duplicate-key":  "name: a\n    name: b\n    url: http://localhost",
		"labels-type":    "name: a\n    url: http://localhost\n    labels: {env: 123}",
		"url-user":       "name: a\n    url: https://user:DO_NOT_LEAK@localhost",
		"url-query":      "name: a\n    url: https://localhost?secret=DO_NOT_LEAK",
		"url-fragment":   "name: a\n    url: https://localhost#DO_NOT_LEAK",
		"auth":           "name: a\n    url: http://localhost\n    auth_type: other",
		"basic-user":     "name: a\n    url: http://localhost\n    auth_type: basic",
		"foreign-secret": "name: a\n    url: http://localhost\n    password_env: PASSWORD",
		"bad-env":        "name: a\n    url: http://localhost\n    auth_type: bearer\n    bearer_token_env: BAD-ENV",
		"mtls-http":      "name: a\n    url: http://localhost\n    auth_type: mtls\n    tls: {client_cert: cert.pem, client_key: key.pem}",
		"mtls-files":     "name: a\n    url: https://localhost\n    auth_type: mtls",
		"bad-verify":     "name: a\n    url: https://localhost\n    tls: {verify: 1}",
		"tls-field":      "name: a\n    url: https://localhost\n    tls: {unexpected: true}",
		"proxy-user":     "name: a\n    url: http://localhost\n    proxy: http://user:DO_NOT_LEAK@proxy",
		"merge":          "<<: {name: a, url: http://localhost}\n    name: b",
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			i, e := ParseContexts([]byte("contexts:\n  - "+fields+"\n"), "contexts.yaml")
			if e == nil || len(i.Definitions()) != 0 {
				t.Fatal("accepted invalid inventory")
			}
			if strings.Contains(e.Error(), "DO_NOT_LEAK") || !strings.Contains(e.Error(), "contexts.yaml") {
				t.Fatal(e)
			}
		})
	}
	for _, data := range []string{
		"contexts:\n  - {name: a, url: http://localhost}\n  - {name: a, url: http://localhost}\n",
		"contexts: &cycle [*cycle]\n", "contexts: []\n---\ncontexts: []\n", "other: []\n",
	} {
		i, e := ParseContexts([]byte(data), "contexts.yaml")
		if e == nil || len(i.Definitions()) != 0 {
			t.Fatalf("accepted: %s", data)
		}
	}
	i, e := ParseContexts([]byte("contexts:\n  - &a {name: a, url: https://localhost, tls: {verify: false}}\n"), "contexts.yaml")
	if e != nil {
		t.Fatal(e)
	}
	a, _ := i.Lookup("a")
	if a.Cluster.TLS.Verify {
		t.Fatal("ignored explicit false")
	}
	absent := filepath.Join(t.TempDir(), "missing.yaml")
	if i, e := LoadContexts(absent); e != nil || len(i.Definitions()) != 0 {
		t.Fatal(e)
	}
}

func TestContextProxyKeepsSupportedSchemes(t *testing.T) {
	_, err := ParseContexts([]byte("contexts:\n  - {name: a, url: http://localhost, proxy: socks5h://proxy:1080}\n"), "contexts.yaml")
	if err != nil {
		t.Fatal(err)
	}
}

func TestContextFingerprint(t *testing.T) {
	d := ContextDefinition{Name: "a", Cluster: Cluster{URL: "https://localhost", AuthType: AuthBasic, Username: "user", TLS: TLS{Verify: true}}, Secrets: SecretRefs{Password: "PASSWORD"}, Labels: map[string]string{"env": "prod"}}
	original := d.Fingerprint()
	d.Labels["env"] = "test"
	d.Name = "b"
	if d.Fingerprint() != original {
		t.Fatal("label/name affects connection")
	}
	for _, change := range []func(*ContextDefinition){func(d *ContextDefinition) { d.Cluster.URL = "https://other" }, func(d *ContextDefinition) { d.Secrets.Password = "NEW_PASSWORD" }, func(d *ContextDefinition) { d.Cluster.TLS.Verify = false }, func(d *ContextDefinition) { d.Cluster.Proxy = "none" }} {
		copy := d
		change(&copy)
		if copy.Fingerprint() == original {
			t.Fatal("ignored connection change")
		}
	}
}
func TestContextWorkPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir, _ := ConfigDir()
	seen := map[string]bool{}
	for _, name := range []string{"prod", "PROD", "CON", "a..b"} {
		p, e := ContextWorkPaths(name)
		if e != nil {
			t.Fatal(e)
		}
		parent := filepath.Dir(p.Queries)
		if !strings.HasPrefix(parent, filepath.Join(dir, "contexts")+string(os.PathSeparator)) || len(filepath.Base(parent)) != 64 || filepath.Base(p.Queries) != "queries.txt" || filepath.Base(p.Variables) != "variables.txt" || seen[parent] {
			t.Fatal(p)
		}
		seen[parent] = true
	}
	if _, e := ContextWorkPaths("../prod"); e == nil {
		t.Fatal("traversal accepted")
	}
}
func TestInventoryReturnsCopies(t *testing.T) {
	i, e := ParseContexts([]byte("contexts:\n  - {name: a, url: http://localhost, labels: {env: prod}}\n"), "contexts.yaml")
	if e != nil {
		t.Fatal(e)
	}
	defs := i.Definitions()
	defs[0].Name = "changed"
	defs[0].Labels["env"] = "changed"
	d, _ := i.Lookup("a")
	d.Labels["env"] = "also changed"
	again, _ := i.Lookup("a")
	if again.Name != "a" || again.Labels["env"] != "prod" {
		t.Fatal(again)
	}
}
