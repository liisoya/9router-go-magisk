package chat

import (
	json "encoding/json/v2"

	"9router/proxy/internal/models"
)

// connProjectRef mirrors the two shapes a project ID takes inside the
// providerConnections.data blob: top-level `projectId` (written by
// storeAntigravityProjectID on onboarding) and `providerSpecificData.projectId`
// (written by the dashboard when the connection is created).
type connProjectRef struct {
	ProjectID            string `json:"projectId"`
	ProviderSpecificData struct {
		ProjectID string `json:"projectId"`
	} `json:"providerSpecificData"`
}

// connIdentityKV returns the log key/value pairs identifying a connection by
// name, email and project ID. Upstream errors such as Antigravity's
// "Verify your account to continue" name neither the Google account nor the
// Cloud project, so without these fields a failure is only traceable by opening
// the dashboard. Empty fields are omitted to keep log lines readable.
func connIdentityKV(conn *models.ProviderConnection) []any {
	if conn == nil {
		return nil
	}
	kv := make([]any, 0, 6)
	if conn.Name != nil && *conn.Name != "" {
		kv = append(kv, "connName", *conn.Name)
	}
	if conn.Email != nil && *conn.Email != "" {
		kv = append(kv, "email", *conn.Email)
	}
	if pid := projectIDFromConnData(conn.Data); pid != "" {
		kv = append(kv, "projectId", pid)
	}
	return kv
}

// connIdentityKVByID looks a connection up by ID and returns its identity
// fields. Used on failure paths only, so the query never lands on the hot path.
func (h *ChatHandler) connIdentityKVByID(connectionID string) []any {
	if h.Repo == nil || connectionID == "" {
		return nil
	}
	conn, err := h.Repo.GetProviderConnectionByID(connectionID)
	if err != nil || conn == nil {
		return nil
	}
	return connIdentityKV(conn)
}

func projectIDFromConnData(data string) string {
	if data == "" {
		return ""
	}
	var ref connProjectRef
	if err := json.Unmarshal([]byte(data), &ref); err != nil {
		return ""
	}
	if ref.ProjectID != "" {
		return ref.ProjectID
	}
	return ref.ProviderSpecificData.ProjectID
}
