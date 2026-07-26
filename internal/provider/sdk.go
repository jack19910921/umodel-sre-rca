package provider

import "github.com/aliyun/credentials-go/credentials"

// NewECSRAMRoleCredential obtains short-lived credentials from the RAM role
// attached to the customer ECS instance. It deliberately never reads an
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
