package umodelid

import "testing"

func TestEndpointEntityIDMatchesPersistedSREEndpointIdentity(t *testing.T) {
	if got, want := EndpointEntityID("blog-http"), "b0ccfdf2b903a8279c8a109aec71cba2"; got != want {
		t.Fatalf("EndpointEntityID() = %q, want %q", got, want)
	}
}
