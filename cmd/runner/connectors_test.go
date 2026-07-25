package main

import (
	"strings"
	"testing"
)

func TestApplyPostgresEnv(t *testing.T) {
	extra, note, err := applyPostgresEnv(map[string]interface{}{
		"host": "db.example.com", "database": "app", "user": "ro", "port": "5433",
	})
	if err != nil {
		t.Fatal(err)
	}
	if extra["AI_CLOUDHUB_PG_HOST"] != "db.example.com" || extra["AI_CLOUDHUB_PG_DATABASE"] != "app" {
		t.Fatalf("%v", extra)
	}
	if extra["AI_CLOUDHUB_PG_PORT"] != "5433" {
		t.Fatalf("port %v", extra)
	}
	if !strings.Contains(extra["AI_CLOUDHUB_PG_DSN_TEMPLATE"], "ro@") {
		t.Fatalf("dsn %v", extra["AI_CLOUDHUB_PG_DSN_TEMPLATE"])
	}
	if !strings.Contains(note, "pg ready") {
		t.Fatalf("note %q", note)
	}
	_, _, err = applyPostgresEnv(map[string]interface{}{"host": "h"})
	if err == nil {
		t.Fatal("want missing database")
	}
	_, _, err = applyPostgresEnv(map[string]interface{}{
		"host": "h", "database": "d",
		"dsn_template": "postgres://u:secret@h/d",
	})
	if err == nil {
		t.Fatal("want reject secret dsn")
	}
}

func TestDSNLooksSecret(t *testing.T) {
	if !dsnLooksSecret("postgres://u:p@h/d") {
		t.Fatal("want secret")
	}
	if dsnLooksSecret("postgres://u@h:5432/d?sslmode=require") {
		t.Fatal("passwordless should be ok")
	}
}
