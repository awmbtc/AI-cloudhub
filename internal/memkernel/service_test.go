package memkernel

import (
	"testing"

	"github.com/awmbtc/AI-cloudhub/internal/store"
)

func TestPutListDelete(t *testing.T) {
	s := New(store.NewMemory())
	e, err := s.Put("u1", PutInput{Layer: LayerWorking, Content: "hello", Key: "k1"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("u1", e.ID)
	if err != nil || got.Content != "hello" {
		t.Fatalf("%v %+v", err, got)
	}
	list, err := s.List("u1", store.MemoryFilter{Layer: LayerWorking})
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %d", err, len(list))
	}
	if err := s.Delete("u1", e.ID); err != nil {
		t.Fatal(err)
	}
}
