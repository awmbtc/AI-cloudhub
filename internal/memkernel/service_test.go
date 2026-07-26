package memkernel

import (
	"strings"
	"testing"
	"time"

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
	if _, err := s.Get("u1", e.ID); err == nil {
		t.Fatal("expected not found after delete")
	}
}

func TestListLayerFilter(t *testing.T) {
	s := New(store.NewMemory())
	if _, err := s.Put("u1", PutInput{Layer: LayerWorking, Content: "w"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("u1", PutInput{Layer: LayerSemantic, Content: "s"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("u1", PutInput{Layer: LayerEpisodic, Content: "e"}); err != nil {
		t.Fatal(err)
	}
	sem, err := s.List("u1", store.MemoryFilter{Layer: LayerSemantic})
	if err != nil || len(sem) != 1 || sem[0].Content != "s" {
		t.Fatalf("semantic: %v %+v", err, sem)
	}
	all, err := s.List("u1", store.MemoryFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("all: %v %d", err, len(all))
	}
}

func TestTTLExpiry(t *testing.T) {
	s := New(store.NewMemory())
	e, err := s.Put("u1", PutInput{
		Layer:   LayerWorking,
		Content: "ephemeral",
		TTL:     30 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if e.ExpiresAt.IsZero() {
		t.Fatal("expected expires_at")
	}
	// Still visible immediately.
	if _, err := s.Get("u1", e.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := s.Get("u1", e.ID); err == nil {
		t.Fatal("expected expired get to fail")
	}
	list, err := s.List("u1", store.MemoryFilter{Layer: LayerWorking})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range list {
		if it.ID == e.ID {
			t.Fatal("expired entry should not appear in list")
		}
	}
	// Absolute expires_at
	e2, err := s.Put("u1", PutInput{
		Layer:     LayerEpisodic,
		Content:   "past",
		ExpiresAt: time.Now().UTC().Add(-time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("u1", e2.ID); err == nil {
		t.Fatal("already-expired put should not be gettable")
	}
}

func TestContentAndEmbeddingGuards(t *testing.T) {
	s := New(store.NewMemory())
	big := strings.Repeat("x", MaxContentBytes+1)
	if _, err := s.Put("u1", PutInput{Content: big}); err == nil {
		t.Fatal("expected content too large")
	}
	emb := make([]float32, MaxEmbeddingDims+1)
	if _, err := s.Put("u1", PutInput{Content: "ok", Embedding: emb}); err == nil {
		t.Fatal("expected embedding too large")
	}
	// boundary OK
	okEmb := make([]float32, MaxEmbeddingDims)
	okEmb[0] = 1
	if _, err := s.Put("u1", PutInput{Content: "ok", Embedding: okEmb}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put("u1", PutInput{Content: strings.Repeat("y", MaxContentBytes)}); err != nil {
		t.Fatal(err)
	}
}

func TestSearchVectorKHonesty(t *testing.T) {
	s := New(store.NewMemory())
	for i, vec := range [][]float32{{1, 0, 0}, {0, 1, 0}, {0.7, 0.7, 0}} {
		c := string(rune('a' + i))
		if _, err := s.Put("u1", PutInput{
			Layer: LayerSemantic, Content: c, Embedding: vec,
		}); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := s.SearchVector("u1", []float32{1, 0, 0}, 2, LayerSemantic)
	if err != nil || len(hits) != 2 {
		t.Fatalf("k=2: %v %d", err, len(hits))
	}
	if hits[0].Score < hits[1].Score {
		t.Fatalf("scores not desc: %v %v", hits[0].Score, hits[1].Score)
	}
	// k=0 → default
	hits, err = s.SearchVector("u1", []float32{1, 0, 0}, 0, "")
	if err != nil || len(hits) == 0 {
		t.Fatalf("default k: %v %d", err, len(hits))
	}
	// k > MaxSearchK → error (honest)
	if _, err := s.SearchVector("u1", []float32{1, 0, 0}, MaxSearchK+1, ""); err == nil {
		t.Fatal("expected k too large")
	}
	// query dims guard
	q := make([]float32, MaxEmbeddingDims+1)
	if _, err := s.SearchVector("u1", q, 1, ""); err == nil {
		t.Fatal("expected query embedding too large")
	}
	// expired skipped in search
	_, err = s.Put("u1", PutInput{
		Layer: LayerSemantic, Content: "gone",
		Embedding: []float32{1, 0, 0},
		TTL:       20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	hits, err = s.SearchVector("u1", []float32{1, 0, 0}, 10, LayerSemantic)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.Entry != nil && h.Entry.Content == "gone" {
			t.Fatal("expired should not appear in search")
		}
	}
}

func TestInvalidLayer(t *testing.T) {
	s := New(store.NewMemory())
	if _, err := s.Put("u1", PutInput{Layer: "quantum", Content: "x"}); err == nil {
		t.Fatal("expected layer error")
	}
}
