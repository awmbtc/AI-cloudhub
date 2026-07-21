package sandbox

import "testing"

func TestFilterEnvBlocksSecrets(t *testing.T) {
	base := []string{
		"PATH=/bin",
		"AWS_SECRET_ACCESS_KEY=sekrit",
		"AI_CLOUDHUB_WORKSPACE=/workspace",
		"AI_CLOUDHUB_TOKEN=parent-token",
		"HOME=/home/u",
		"EVIL_KEY=1",
	}
	extra := map[string]string{
		"AI_CLOUDHUB_DRIVE_ID": "d1",
		"AWS_ACCESS_KEY_ID":    "should-block",
	}
	got := FilterEnv(base, extra, EnvFilter{})
	m := map[string]string{}
	for _, e := range got {
		i := indexEq(e)
		m[e[:i]] = e[i+1:]
	}
	if m["PATH"] == "" || m["AI_CLOUDHUB_WORKSPACE"] == "" || m["AI_CLOUDHUB_DRIVE_ID"] != "d1" {
		t.Fatalf("missing allow: %v", m)
	}
	if _, ok := m["AWS_SECRET_ACCESS_KEY"]; ok {
		t.Fatal("secret leaked")
	}
	if _, ok := m["AI_CLOUDHUB_TOKEN"]; ok {
		t.Fatal("parent token leaked")
	}
	if _, ok := m["EVIL_KEY"]; ok {
		t.Fatal("unknown key leaked")
	}
	if _, ok := m["AWS_ACCESS_KEY_ID"]; ok {
		t.Fatal("aws key leaked via extra")
	}
}

func TestFilterEnvPassToken(t *testing.T) {
	got := FilterEnv([]string{"AI_CLOUDHUB_TOKEN=t"}, nil, EnvFilter{PassToken: true})
	if len(got) != 1 || got[0] != "AI_CLOUDHUB_TOKEN=t" {
		t.Fatalf("%v", got)
	}
}

func TestFilterEnvDenyNetwork(t *testing.T) {
	base := []string{"PATH=/bin", "HTTP_PROXY=http://proxy:8080", "AI_CLOUDHUB_WORKSPACE=/w"}
	got := FilterEnv(base, nil, EnvFilter{DenyNetwork: true})
	m := map[string]string{}
	for _, e := range got {
		i := indexEq(e)
		m[e[:i]] = e[i+1:]
	}
	if _, ok := m["HTTP_PROXY"]; ok {
		t.Fatal("proxy should be stripped")
	}
	if m["AI_CLOUDHUB_NETWORK"] != "deny" {
		t.Fatalf("want network deny marker, got %v", m)
	}
}

func indexEq(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			return i
		}
	}
	return -1
}
