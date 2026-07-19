//go:build integration

package store

import (
	"os"
	"testing"
	"time"
)

// Run: AI_CLOUDHUB_TEST_PG=postgres://... CGO_ENABLED=0 go test -tags=integration ./internal/store/ -count=1
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("AI_CLOUDHUB_TEST_PG")
	if dsn == "" {
		t.Skip("set AI_CLOUDHUB_TEST_PG to run")
	}
	st, err := OpenPostgres(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Ping(); err != nil {
		t.Fatal(err)
	}
	u := &User{ID: "it-u1", Username: "it_user_" + time.Now().Format("150405"), Password: "x", Role: "admin"}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetUserByUsername(u.Username)
	if err != nil || got.Role != "admin" {
		t.Fatalf("get user: %v %+v", err, got)
	}
}
