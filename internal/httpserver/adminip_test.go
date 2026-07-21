package httpserver

import "testing"

func TestIPAllowed(t *testing.T) {
	if !ipAllowed("1.2.3.4", nil) {
		t.Fatal("empty allow = all")
	}
	if !ipAllowed("127.0.0.1", []string{"127.0.0.1"}) {
		t.Fatal("exact")
	}
	if ipAllowed("10.0.0.1", []string{"127.0.0.1"}) {
		t.Fatal("deny")
	}
	if !ipAllowed("10.1.2.3", []string{"10.0.0.0/8"}) {
		t.Fatal("cidr")
	}
	if ipAllowed("11.0.0.1", []string{"10.0.0.0/8"}) {
		t.Fatal("cidr deny")
	}
}
