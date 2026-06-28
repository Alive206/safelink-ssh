package store_test

import (
	"testing"

	"github.com/example/safelink/client/internal/store"
)

func TestSSHConnectionsCanBeSavedListedAndDeleted(t *testing.T) {
	st := store.New(t.TempDir())

	saved, err := st.SaveSSHConnection(store.SSHConnection{
		Name:     "prod",
		Addr:     "prod.example.com:22",
		User:     "root",
		Password: "secret",
	})
	if err != nil {
		t.Fatalf("SaveSSHConnection returned error: %v", err)
	}
	if saved.ID == "" {
		t.Fatalf("saved connection ID is empty")
	}

	conns, err := st.LoadSSHConnections()
	if err != nil {
		t.Fatalf("LoadSSHConnections returned error: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("len(conns) = %d, want 1", len(conns))
	}
	if conns[0].Name != "prod" || conns[0].Password != "secret" {
		t.Fatalf("unexpected saved connection: %+v", conns[0])
	}

	saved.User = "admin"
	if _, err := st.SaveSSHConnection(saved); err != nil {
		t.Fatalf("SaveSSHConnection update returned error: %v", err)
	}
	conns, _ = st.LoadSSHConnections()
	if len(conns) != 1 || conns[0].User != "admin" {
		t.Fatalf("connection was not updated: %+v", conns)
	}

	if err := st.DeleteSSHConnection(saved.ID); err != nil {
		t.Fatalf("DeleteSSHConnection returned error: %v", err)
	}
	conns, _ = st.LoadSSHConnections()
	if len(conns) != 0 {
		t.Fatalf("len(conns) after delete = %d, want 0", len(conns))
	}
}
