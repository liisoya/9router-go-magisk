package chat

import (
	"testing"

	"github.com/samber/lo"

	"9router/proxy/internal/models"
)

func TestConnIdentityKV(t *testing.T) {
	tests := []struct {
		name string
		conn *models.ProviderConnection
		want []any
	}{
		{
			name: "name, email and top-level project id",
			conn: &models.ProviderConnection{
				Name:  lo.ToPtr("AG Main"),
				Email: lo.ToPtr("main@example.com"),
				Data:  `{"accessToken":"ya29.x","projectId":"mega-rainfall-szp2g"}`,
			},
			want: []any{"connName", "AG Main", "email", "main@example.com", "projectId", "mega-rainfall-szp2g"},
		},
		{
			name: "project id from providerSpecificData",
			conn: &models.ProviderConnection{
				Email: lo.ToPtr("nested@example.com"),
				Data:  `{"providerSpecificData":{"projectId":"nifty-journal-rjgl4"}}`,
			},
			want: []any{"email", "nested@example.com", "projectId", "nifty-journal-rjgl4"},
		},
		{
			name: "top-level project id wins over providerSpecificData",
			conn: &models.ProviderConnection{
				Data: `{"projectId":"top","providerSpecificData":{"projectId":"nested"}}`,
			},
			want: []any{"projectId", "top"},
		},
		{
			name: "absent fields are omitted",
			conn: &models.ProviderConnection{
				Name: lo.ToPtr("Only Name"),
				Data: `{"apiKey":"sk-test"}`,
			},
			want: []any{"connName", "Only Name"},
		},
		{
			name: "unparseable data blob yields no project id",
			conn: &models.ProviderConnection{
				Email: lo.ToPtr("broken@example.com"),
				Data:  `not-json`,
			},
			want: []any{"email", "broken@example.com"},
		},
		{
			name: "nil connection yields no fields",
			conn: nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := connIdentityKV(tt.conn)
			if len(got) != len(tt.want) {
				t.Fatalf("connIdentityKV() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("connIdentityKV()[%d] = %v, want %v (full: %v vs %v)", i, got[i], tt.want[i], got, tt.want)
				}
			}
		})
	}
}

func TestConnIdentityKVByID_MissingConnection(t *testing.T) {
	// A handler without a Repo (unit tests construct NewChatHandler(nil)) must
	// not panic on the failure logging path.
	h := NewChatHandler(nil)
	if got := h.connIdentityKVByID("conn-does-not-exist"); len(got) != 0 {
		t.Fatalf("expected no identity fields without a repo, got %v", got)
	}
}
