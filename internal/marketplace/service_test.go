package marketplace

import (
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestListSystemAndInstall(t *testing.T) {
	s := New(store.NewMemory())
	items, err := s.List("u1", false)
	if err != nil || len(items) < 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	var created string
	res, err := s.InstallAgentTemplate("u1", "sys.agent.readonly", func(name, desc string, scopes []string) (string, error) {
		created = "agent-1"
		if name == "" || len(scopes) == 0 {
			t.Fatal(name, scopes)
		}
		return created, nil
	})
	if err != nil || res.AgentID != "agent-1" {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestPublish(t *testing.T) {
	s := New(store.NewMemory())
	it, err := s.Publish("u1", PublishInput{
		Name: "my-tpl", Kind: KindAgentTemplate, Public: true,
		Payload: map[string]interface{}{"name": "x", "default_scopes": []interface{}{"drive.read"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(it.ID)
	if err != nil || got.Name != "my-tpl" {
		t.Fatal(err, got)
	}
}
