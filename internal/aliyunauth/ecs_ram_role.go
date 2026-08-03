// Package aliyunauth creates Alibaba Cloud credentials that are safe for
// customer-hosted workloads.  It deliberately contains no service SDK client
// imports, so binaries can compose independently-versioned service SDKs.
package aliyunauth

import "github.com/aliyun/credentials-go/credentials"

// NewECSRAMRoleCredential obtains short-lived credentials from the RAM role
// attached to the current ECS instance. It deliberately never reads an
// Alibaba Cloud CLI profile or shells out to the aliyun binary.
func NewECSRAMRoleCredential(roleName string) (credentials.Credential, error) {
	config := new(credentials.Config).
		SetType("ecs_ram_role").
		SetDisableIMDSv1(true)
	if roleName != "" {
		config.SetRoleName(roleName)
	}
	return credentials.NewCredential(config)
}
