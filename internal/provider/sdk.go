package provider

import (
	"github.com/aliyun/credentials-go/credentials"
	"github.com/jack/umodel-sre-rca/internal/aliyunauth"
)

// NewECSRAMRoleCredential obtains short-lived credentials from the RAM role
// attached to the customer ECS instance. It deliberately never reads an
// Alibaba Cloud CLI profile or shells out to the aliyun binary.
func NewECSRAMRoleCredential(roleName string) (credentials.Credential, error) {
	return aliyunauth.NewECSRAMRoleCredential(roleName)
}
