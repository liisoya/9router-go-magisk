package dashboard

import (
	"testing"

	"9router/proxy/internal/models"
)

func TestSortProviderConnections_GroupsByProviderAndKeepsNullPriority(t *testing.T) {
	p1 := 1
	p2 := 2

	nameA := "Account A"
	nameB := "Account B"
	nameNew := "Account New"

	conns := []*models.ProviderConnection{
		{ID: "c1", Provider: "antigravity", Name: &nameA, Priority: &p1},
		{ID: "c2", Provider: "codex", Name: &nameB, Priority: &p2},
		{ID: "c3", Provider: "antigravity", Name: &nameNew, Priority: nil}, // New connection with NULL priority
	}

	sortProviderConnections(conns, "")

	if len(conns) != 3 {
		t.Fatalf("expected 3 connections, got %d", len(conns))
	}

	// Antigravity connections must remain together
	if conns[0].Provider != "antigravity" || conns[1].Provider != "antigravity" {
		t.Errorf("antigravity accounts separated: got %s, %s, %s", conns[0].Provider, conns[1].Provider, conns[2].Provider)
	}
	if conns[2].Provider != "codex" {
		t.Errorf("expected codex last, got %s", conns[2].Provider)
	}
}
