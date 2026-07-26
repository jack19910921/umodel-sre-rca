package provider

import (
	"context"
	"strings"
	"testing"
)

func TestNewECSRAMRoleCredentialUsesInstanceTemporaryCredentials(t *testing.T) {
	credential, err := NewECSRAMRoleCredential("sre-rca")
	if err != nil {
		t.Fatalf("NewECSRAMRoleCredential() error = %v", err)
	}
	if got := credential.GetType(); got == nil || *got != "ecs_ram_role" {
		t.Fatalf("credential type = %v, want ecs_ram_role", got)
	}
}

func TestSDKAliyunClientRejectsNonAllowlistedOperation(t *testing.T) {
	client := &SDKAliyunClient{}
	_, err := client.Call(context.Background(), AliyunRequest{Service: "ecs", Operation: "RebootInstance"})
	if err == nil || !strings.Contains(err.Error(), "not allowlisted") {
		t.Fatalf("Call() error = %v, want non-allowlisted operation rejection", err)
	}
}
