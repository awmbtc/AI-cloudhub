package connector

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestCreateStripsSecretsAndPostgresRequiresHostDB(t *testing.T) {
	s := New(store.NewMemory())
	c, err := s.Create("u1", CreateInput{
		Type: "postgres",
		Name: "db",
		Config: map[string]interface{}{
			"host": "db.example.com", "database": "app", "user": "ro",
			"password": "SECRET", "dsn": "postgres://x:y@h/db",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(c.ConfigJSON, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["password"]; ok {
		t.Fatalf("password not stripped: %v", cfg)
	}
	if _, ok := cfg["dsn"]; ok {
		t.Fatalf("dsn not stripped: %v", cfg)
	}
	if cfg["host"] != "db.example.com" {
		t.Fatalf("%v", cfg)
	}
	// API-shaped JSON must be object not base64
	b, _ := json.Marshal(c)
	var wire map[string]interface{}
	_ = json.Unmarshal(b, &wire)
	if _, ok := wire["config"].(map[string]interface{}); !ok {
		t.Fatalf("config should be object in JSON, got %T %v", wire["config"], wire["config"])
	}

	_, err = s.Create("u1", CreateInput{Type: "postgres", Config: map[string]interface{}{"host": "h"}})
	if err == nil || !strings.Contains(err.Error(), "database") {
		t.Fatalf("want require database, got %v", err)
	}
}

func TestRejectPasswordfulDSNTemplate(t *testing.T) {
	s := New(store.NewMemory())
	_, err := s.Create("u1", CreateInput{
		Type: "postgres",
		Config: map[string]interface{}{
			"host": "h", "database": "d",
			"dsn_template": "postgres://u:pass@h:5432/d",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "dsn_template") {
		t.Fatalf("got %v", err)
	}
}

func TestCatalogPostgresFields(t *testing.T) {
	var found bool
	for _, m := range Catalog() {
		if m.Type == "postgres" {
			found = true
			joined := strings.Join(m.Fields, ",")
			if !strings.Contains(joined, "host") || !strings.Contains(joined, "dsn_template") {
				t.Fatalf("fields %v", m.Fields)
			}
		}
	}
	if !found {
		t.Fatal("postgres missing")
	}
}

func TestCreateMysqlStripsAndFields(t *testing.T) {
	s := New(store.NewMemory())
	c, err := s.Create("u1", CreateInput{
		Type: "mysql",
		Name: "mdb",
		Config: map[string]interface{}{
			"host": "mysql.example.com", "database": "app", "user": "ro",
			"password": "SECRET", "mysql_pwd": "also",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(c.ConfigJSON, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["password"]; ok {
		t.Fatalf("password not stripped: %v", cfg)
	}
	if cfg["host"] != "mysql.example.com" {
		t.Fatalf("%v", cfg)
	}
	var found bool
	for _, m := range Catalog() {
		if m.Type == "mysql" {
			found = true
			joined := strings.Join(m.Fields, ",")
			if !strings.Contains(joined, "dsn_template") {
				t.Fatalf("fields %v", m.Fields)
			}
		}
	}
	if !found {
		t.Fatal("mysql missing")
	}
}
