package aliyunauth

import "testing"

func TestNewECSRAMRoleCredentialUsesInstanceTemporaryCredentials(t *testing.T) {
	credential, err := NewECSRAMRoleCredential("sre-rca")
	if err != nil {
		t.Fatalf("NewECSRAMRoleCredential() error = %v", err)
	}
	if got := credential.GetType(); got == nil || *got != "ecs_ram_role" {
		t.Fatalf("credential type = %v, want ecs_ram_role", got)
	}
}
