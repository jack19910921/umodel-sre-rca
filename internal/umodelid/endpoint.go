// Package umodelid contains stable, SDK-free EntityStore identifiers shared by
// model synchronization and read-only evidence providers.
package umodelid

import (
	"crypto/md5"
	"encoding/hex"
)

// EndpointEntityID returns the EntityStore ID for an SRE service endpoint.
func EndpointEntityID(endpointID string) string {
	sum := md5.Sum([]byte("sre:sre.service_endpoint:" + endpointID))
	return hex.EncodeToString(sum[:])
}
